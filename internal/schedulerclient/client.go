// Package schedulerclient consumes Tessera alpha.36's public administration
// contract. Authority is supplied per request; no credential is retained.
package schedulerclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid scheduler response")
var ErrUnknown = errors.New("settings outcome uncertain")
var scopePattern = regexp.MustCompile(`^[0-9a-f]{32}\.[0-9a-f]{32}$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{32}\.[0-9a-f]{32}\.[0-9a-f]{32}$`)
var requestPattern = regexp.MustCompile(`^[0-9a-f]{32}\.[0-9a-f]{32}\.[0-9]{1,12}-[0-9a-f]{32}$`)
var tokenPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var acquisitionPattern = regexp.MustCompile(`^[0-9]{1,12}-[0-9a-f]{32}$`)

func ValidScope(s string) bool    { return scopePattern.MatchString(s) }
func ValidRevision(s string) bool { return revisionPattern.MatchString(s) }
func ValidID(s string) bool       { return requestPattern.MatchString(s) }
func Scope(revision string) string {
	if !ValidRevision(revision) {
		return ""
	}
	return revision[:65]
}
func NewID(scope string, now time.Time) (string, error) {
	if !ValidScope(scope) {
		return "", ErrInvalid
	}
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", ErrUnknown
	}
	id := fmt.Sprintf("%s.%d-%s", scope, now.Unix(), hex.EncodeToString(b[:]))
	if !ValidID(id) {
		return "", ErrInvalid
	}
	return id, nil
}

type Settings struct {
	EveryHours int    `json:"every_hours"`
	Keep       int    `json:"keep"`
	Revision   string `json:"revision"`
}
type Payload struct {
	ExpectedRevision string `json:"expected_revision"`
	EveryHours       int    `json:"every_hours"`
	Keep             int    `json:"keep"`
}

func (p Payload) Valid() bool {
	return ValidRevision(p.ExpectedRevision) && p.EveryHours >= 1 && p.EveryHours <= 8760 && p.Keep >= 1 && p.Keep <= 1000
}

type Operation struct {
	ID string `json:"id"`
	Payload
	State       string     `json:"state"`
	AcceptedAt  time.Time  `json:"accepted_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	EffectiveAt *time.Time `json:"effective_at,omitempty"`
	Revision    string     `json:"revision,omitempty"`
	Failure     string     `json:"failure,omitempty"`
}

func (o Operation) Validate(id string, p Payload) error {
	if !ValidID(id) || o.ID != id || o.Payload != p || !p.Valid() || !strings.HasPrefix(id, Scope(p.ExpectedRevision)+".") || o.AcceptedAt.IsZero() || o.ExpiresAt.Before(o.AcceptedAt) {
		return ErrInvalid
	}
	switch o.State {
	case "pending":
		if o.Revision != "" || o.EffectiveAt != nil || o.Failure != "" {
			return ErrInvalid
		}
	case "effective":
		if Scope(o.Revision) != Scope(p.ExpectedRevision) || o.EffectiveAt == nil || o.EffectiveAt.Before(o.AcceptedAt) || o.Failure != "" {
			return ErrInvalid
		}
	case "failed":
		if !one(o.Failure, "stale_revision", "settings_pending") || o.Revision != "" || o.EffectiveAt != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

type Export struct {
	Version    int       `json:"version"`
	Deployment string    `json:"deployment"`
	EveryHours int       `json:"every_hours"`
	Keep       int       `json:"keep"`
	Revision   string    `json:"revision"`
	ExportedAt time.Time `json:"exported_at"`
}

func (e Export) Valid() bool {
	return e.Version == 1 && tokenPattern.MatchString(e.Deployment) && ValidRevision(e.Revision) && strings.HasPrefix(e.Revision, e.Deployment+".") && e.EveryHours >= 0 && e.EveryHours <= 87600 && e.Keep >= 1 && e.Keep <= 1024 && !e.ExportedAt.IsZero()
}

type Attempt struct {
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	Outcome     string     `json:"outcome"`
	OperationID string     `json:"operation_id,omitempty"`
}
type Active struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
}
type Retention struct {
	Retained int    `json:"retained"`
	Target   int    `json:"target"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
}
type Observation struct {
	InstanceID       string     `json:"instance_id"`
	ObservedAt       time.Time  `json:"observed_at"`
	HistoryScope     string     `json:"history_scope"`
	Configured       bool       `json:"configured"`
	Available        bool       `json:"available"`
	Reason           string     `json:"reason"`
	Enabled          bool       `json:"enabled"`
	Settings         *Settings  `json:"settings"`
	SettingsScope    *string    `json:"settings_scope"`
	PendingRequestID *string    `json:"pending_request_id"`
	Last             *Attempt   `json:"last_scheduled_attempt"`
	Active           *Active    `json:"active_scheduled_operation"`
	Next             *time.Time `json:"next_attempt_at"`
	NextReason       string     `json:"next_reason"`
	Retention        *Retention `json:"retention"`
	Export           *Export    `json:"settings_export"`
}

func one(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func (o Observation) Validate(now time.Time) error {
	if !tokenPattern.MatchString(o.InstanceID) || o.HistoryScope != "process_instance" || o.ObservedAt.IsZero() || o.ObservedAt.Before(now.Add(-30*time.Second)) || o.ObservedAt.After(now.Add(30*time.Second)) {
		return ErrInvalid
	}
	if !o.Configured {
		if o.Available || o.Enabled || o.Reason != "not_configured" || o.NextReason != "not_configured" || o.Settings != nil || o.SettingsScope != nil || o.Export != nil || o.Retention != nil || o.PendingRequestID != nil || o.Last != nil || o.Active != nil || o.Next != nil {
			return ErrInvalid
		}
		return nil
	}
	if !o.Available && o.Reason == "settings_unavailable" {
		if o.Enabled || o.Last != nil || o.Active != nil || o.PendingRequestID != nil || o.Settings != nil || o.Export != nil || o.SettingsScope != nil || o.Retention != nil || o.Next != nil || o.NextReason != "settings_unavailable" {
			return ErrInvalid
		}
		return nil
	}
	s := o.Settings
	e := o.Export
	if o.Available != (o.Reason == "ready") || !one(o.Reason, "ready", "acquisition_unavailable") || s == nil || e == nil || o.SettingsScope == nil || !ValidScope(*o.SettingsScope) || Scope(s.Revision) != *o.SettingsScope || !e.Valid() || s.Revision != e.Revision || s.EveryHours != e.EveryHours || s.Keep != e.Keep || !e.ExportedAt.Equal(o.ObservedAt) || o.Enabled != (s.EveryHours > 0) {
		return ErrInvalid
	}
	if o.PendingRequestID != nil && (!ValidID(*o.PendingRequestID) || !strings.HasPrefix(*o.PendingRequestID, *o.SettingsScope+".")) {
		return ErrInvalid
	}
	if !one(o.NextReason, "scheduled", "in_progress", "disabled", "not_started", "stopped", "settings_pending", "settings_unavailable", "acquisition_unavailable") || (o.Next != nil) != (o.NextReason == "scheduled") {
		return ErrInvalid
	}
	if a := o.Last; a != nil {
		if a.StartedAt.IsZero() || a.StartedAt.After(o.ObservedAt) || !one(a.Outcome, "in_progress", "succeeded", "failed", "cancelled", "refused_busy", "refused_capacity", "refused_settings_pending", "refused_unavailable") || (a.OperationID != "" && !acquisitionPattern.MatchString(a.OperationID)) || (a.FinishedAt == nil) != (a.Outcome == "in_progress") {
			return ErrInvalid
		}
		if a.FinishedAt != nil && (a.FinishedAt.Before(a.StartedAt) || a.FinishedAt.After(o.ObservedAt)) {
			return ErrInvalid
		}
	}
	if o.Last != nil && o.Last.Outcome == "in_progress" && (o.Active == nil || o.Last.OperationID == "") {
		return ErrInvalid
	}
	if a := o.Active; a != nil {
		if o.Last == nil || o.Last.Outcome != "in_progress" || a.ID != o.Last.OperationID || !acquisitionPattern.MatchString(a.ID) || !a.StartedAt.Equal(o.Last.StartedAt) || o.NextReason != "in_progress" {
			return ErrInvalid
		}
	}
	r := o.Retention
	if r == nil || r.Retained < 0 || r.Retained > 1025 || r.Target != s.Keep {
		return ErrInvalid
	}
	if !one(r.State+"/"+r.Reason, "converged/at_target", "pending/acquisition_in_progress", "pending/pruning", "blocked/leases", "blocked/transfers", "blocked/reserved", "degraded/cleanup_failed", "degraded/journal_unavailable") {
		return ErrInvalid
	}
	return nil
}

type Refusal struct {
	Code, Reason string
	Operation    *Operation
}

func (e *Refusal) Error() string { return "settings request refused: " + e.Code + "/" + e.Reason }

type Client struct {
	origin string
	http   *http.Client
}

func New(origin string, h *http.Client) (*Client, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || h == nil {
		return nil, ErrInvalid
	}
	c := *h
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Timeout = 15 * time.Second
	return &Client{strings.TrimSuffix(origin, "/"), &c}, nil
}
func decode(raw []byte, out any) error {
	if uniqueJSON(json.NewDecoder(bytes.NewReader(raw))) != nil {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (c *Client) request(ctx context.Context, method, path, token string, in any) (int, []byte, error) {
	if token == "" {
		return 0, nil, &Refusal{Code: "authentication_failed"}
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.origin+path, body)
	if e != nil {
		return 0, nil, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.http.Do(req)
	if e != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, nil, ErrUnknown
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil {
		return 0, nil, ErrUnknown
	}
	if len(b) > 65536 {
		return 0, nil, ErrInvalid
	}
	return resp.StatusCode, b, nil
}
func refusal(b []byte) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Reason    string     `json:"reason"`
				Operation *Operation `json:"operation"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &env) != nil {
		return ErrInvalid
	}
	c := env.Error.Code
	r := env.Error.Details.Reason
	if !one(c, "authentication_failed", "permission_denied", "not_supported", "invalid_request", "invalid_field", "conflict", "not_found", "expired", "limit_exceeded", "dataset_unavailable") {
		return ErrUnknown
	}
	if r != "" && !one(r, "settings_busy", "stale_revision", "settings_pending", "request_reused", "request_scope_mismatch", "settings_unavailable", "not_configured") {
		return ErrUnknown
	}
	return &Refusal{c, r, env.Error.Details.Operation}
}
func (c *Client) Observe(ctx context.Context, token string, now time.Time) (Observation, error) {
	status, b, e := c.request(ctx, "GET", "/admin/api/v1/backup-scheduler", token, nil)
	if e != nil {
		return Observation{}, e
	}
	if status != 200 {
		return Observation{}, refusal(b)
	}
	// Every nullable member is required. Missing booleans must not masquerade as false.
	var members map[string]json.RawMessage
	if json.Unmarshal(b, &members) != nil {
		return Observation{}, ErrInvalid
	}
	for _, k := range []string{"instance_id", "observed_at", "history_scope", "configured", "available", "reason", "enabled", "settings", "settings_scope", "pending_request_id", "last_scheduled_attempt", "active_scheduled_operation", "next_attempt_at", "next_reason", "retention", "settings_export"} {
		v, ok := members[k]
		if !ok || ((k == "configured" || k == "available" || k == "enabled") && bytes.Equal(v, []byte("null"))) {
			return Observation{}, ErrInvalid
		}
	}
	for object, keys := range map[string][]string{
		"settings":                   {"every_hours", "keep", "revision"},
		"settings_export":            {"version", "deployment", "every_hours", "keep", "revision", "exported_at"},
		"retention":                  {"retained", "target", "state", "reason"},
		"last_scheduled_attempt":     {"started_at", "finished_at", "outcome"},
		"active_scheduled_operation": {"id", "started_at"},
	} {
		raw := members[object]
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return Observation{}, ErrInvalid
		}
		for _, key := range keys {
			v, ok := fields[key]
			if !ok || (key != "finished_at" && bytes.Equal(bytes.TrimSpace(v), []byte("null"))) {
				return Observation{}, ErrInvalid
			}
		}
	}

	var o Observation
	if e = decode(b, &o); e == nil {
		e = o.Validate(now)
	}
	return o, e
}
func (c *Client) Operation(ctx context.Context, id string, p Payload, token string, submit bool) (Operation, error) {
	if !ValidID(id) || !p.Valid() {
		return Operation{}, ErrInvalid
	}
	method := "GET"
	var body any
	if submit {
		method = "PUT"
		body = p
	}
	status, b, e := c.request(ctx, method, "/admin/api/v1/backup-settings-operations/"+id, token, body)
	if e != nil {
		return Operation{}, e
	}
	if status != 200 && !(submit && status == 202) {
		e = refusal(b)
		var r *Refusal
		if errors.As(e, &r) && r.Operation != nil {
			o := *r.Operation
			if status != 409 || o.State != "failed" || o.Failure != r.Reason || o.Validate(id, p) != nil {
				return Operation{}, ErrInvalid
			}
			return o, e
		}
		return Operation{}, e
	}
	var o Operation
	if e = decode(b, &o); e == nil {
		e = o.Validate(id, p)
	}
	if e == nil && submit && ((status == 202) != (o.State == "pending")) {
		e = ErrInvalid
	}
	return o, e
}

func uniqueJSON(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return ErrInvalid
				}
				seen[s] = true
				if e = uniqueJSON(d); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e := uniqueJSON(d); e != nil {
					return e
				}
			}
		default:
			return ErrInvalid
		}
		_, e = d.Token()
		return e
	}
	return nil
}
