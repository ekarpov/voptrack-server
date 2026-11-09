// Package acquisitionclient implements the supported alpha.35 administrative
// transport. Credentials are supplied per call and are never owned or persisted
// by this package. It does not choose scheduling authority.
package acquisitionclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var operationID = regexp.MustCompile(`^[0-9]{1,12}-[0-9a-f]{32}$`)
var artifactID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var ErrUnknown = errors.New("backup request outcome unknown")
var ErrInvalid = errors.New("invalid backup acquisition data")
var ErrIntegrity = errors.New("backup artifact integrity failure")

type Refusal struct{ Code string }

func (e *Refusal) Error() string { return "backup acquisition refused: " + e.Code }

type Artifact struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Pair struct {
	Version    int        `json:"version"`
	Deployment string     `json:"deployment"`
	S          string     `json:"s,omitempty"`
	R          string     `json:"r"`
	Artifacts  []Artifact `json:"artifacts"`
}
type Operation struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"`
	Failure    string     `json:"failure,omitempty"`
	AcceptedAt time.Time  `json:"accepted_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	Pair       *Pair      `json:"pair,omitempty"`
	Artifacts  []Artifact `json:"artifacts,omitempty"`
}
type Client struct {
	origin string
	http   *http.Client
}

func New(origin string, transport *http.Client) (*Client, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || transport == nil {
		return nil, ErrInvalid
	}
	copy := *transport
	// A credential may never follow a redirect, including within the same origin.
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{strings.TrimSuffix(origin, "/"), &copy}, nil
}
func RandomID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", ErrUnknown
	}
	return hex.EncodeToString(b), nil
}
func NewID(now time.Time) (string, error) {
	r, e := RandomID()
	return strconv.FormatInt(now.Unix(), 10) + "-" + r, e
}
func ValidID(id string) bool { return operationID.MatchString(id) }
func path(id string) (string, error) {
	if !ValidID(id) {
		return "", ErrInvalid
	}
	return "/admin/api/v1/backup-operations/" + id, nil
}
func safeCode(code string) string {
	switch code {
	case "permission_denied", "authentication_failed", "invalid_field", "invalid_request", "not_supported", "limit_exceeded", "query_result_limit_exceeded", "dataset_unavailable", "store_unavailable", "conflict", "expired", "not_found", "unavailable", "invalid_cursor", "cursor_expired":
		return code
	}
	return "request_failed"
}
func (c *Client) request(ctx context.Context, method, p, token, lease string, body io.Reader) (*http.Response, error) {
	if token == "" {
		return nil, &Refusal{"authentication_failed"}
	}
	req, e := http.NewRequestWithContext(ctx, method, c.origin+p, body)
	if e != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if lease != "" {
		req.Header.Set("Tessera-Backup-Lease", lease)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, ErrUnknown
	}
	return resp, nil
}
func refused(resp *http.Response) error {
	var v struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v)
	return &Refusal{safeCode(v.Error.Code)}
}

func (c *Client) json(ctx context.Context, method, p, token string, body io.Reader, status int, out any) error {
	resp, e := c.request(ctx, method, p, token, "", body)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		return refused(resp)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil {
		return ErrUnknown
	}
	if len(raw) > 1<<20 {
		return ErrInvalid
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrInvalid
	}
	return nil
}
func (c *Client) Start(ctx context.Context, id, kind, token string) (Operation, error) {
	p, e := path(id)
	if e != nil {
		return Operation{}, e
	}
	if kind != "backup" && kind != "evidence" {
		return Operation{}, ErrInvalid
	}
	var op Operation
	e = c.json(ctx, "PUT", p, token, strings.NewReader(`{"kind":"`+kind+`"}`), 202, &op)
	if e == nil {
		e = validate(op, id, kind)
	}
	return op, e
}
func (c *Client) Status(ctx context.Context, id, kind, token string) (Operation, error) {
	return c.observe(ctx, "GET", id, kind, token)
}
func (c *Client) Cancel(ctx context.Context, id, kind, token string) (Operation, error) {
	return c.observe(ctx, "DELETE", id, kind, token)
}
func (c *Client) observe(ctx context.Context, method, id, kind, token string) (Operation, error) {
	p, e := path(id)
	if kind != "backup" && kind != "evidence" {
		return Operation{}, ErrInvalid
	}
	if e != nil {
		return Operation{}, e
	}
	var op Operation
	e = c.json(ctx, method, p, token, nil, 200, &op)
	if e == nil {
		e = validate(op, id, kind)
	}
	return op, e
}
func (c *Client) Lease(ctx context.Context, id, lease, token string, release bool) (time.Time, error) {
	p, e := path(id)
	if e != nil {
		return time.Time{}, e
	}
	if !artifactID.MatchString(lease) {
		return time.Time{}, ErrInvalid
	}
	method := "PUT"
	if release {
		method = "DELETE"
	}
	var v struct {
		ID     string    `json:"id"`
		Expiry time.Time `json:"expires_at"`
	}
	e = c.json(ctx, method, p+"/leases/"+lease, token, nil, 200, &v)
	if e == nil && (v.ID != lease || (!release && v.Expiry.IsZero())) {
		e = ErrInvalid
	}
	return v.Expiry, e
}
func validate(op Operation, id, kind string) error {
	if op.ID != id || op.Kind != kind {
		return ErrInvalid
	}
	switch op.State {
	case "running", "failed", "cancelled", "expired":
		return nil
	case "complete":
	default:
		return ErrInvalid
	}
	if op.Pair == nil || op.Pair.Version != 1 || op.Pair.Deployment == "" || op.Pair.R == "" || (kind == "backup" && op.Pair.S == "") || (kind == "evidence" && op.Pair.S != "") {
		return ErrInvalid
	}
	seen := map[string]Artifact{}
	kinds := map[string]bool{}
	for _, a := range op.Artifacts {
		if _, ok := seen[a.ID]; ok {
			return ErrInvalid
		}
		if kinds[a.Kind] {
			return ErrInvalid
		}
		if ValidateArtifact(a) != nil {
			return ErrInvalid
		}
		seen[a.ID] = a
		kinds[a.Kind] = true
	}
	if !kinds["metadata"] || !kinds["evidence"] || (kind == "backup") != kinds["archive"] {
		return ErrInvalid
	}
	expected := 2
	if kind == "backup" {
		expected = 3
	}
	if len(seen) != expected || len(op.Pair.Artifacts) != expected-1 {
		return ErrInvalid
	}
	paired := map[string]bool{}
	for _, a := range op.Pair.Artifacts {
		if got, ok := seen[a.ID]; !ok || got != a || a.Kind == "metadata" || paired[a.ID] {
			return ErrInvalid
		}
		paired[a.ID] = true
	}
	return nil
}
func ValidateArtifact(a Artifact) error {
	if !artifactID.MatchString(a.ID) || !digest.MatchString(a.SHA256) || a.Size < 0 {
		return ErrInvalid
	}
	var limit int64
	switch a.Kind {
	case "archive":
		limit = (256 << 20) + 512
	case "evidence":
		limit = 16 << 20
	case "metadata":
		limit = 1 << 20
	default:
		return ErrInvalid
	}
	if a.Size > limit {
		return ErrInvalid
	}
	return nil
}

// Download checks the exact descriptor before returning success. The caller must
// keep output private until authenticated metadata verification and publication.
func (c *Client) Download(ctx context.Context, id, lease, token string, a Artifact, out io.Writer) error {
	p, e := path(id)
	if e != nil {
		return e
	}
	if !artifactID.MatchString(lease) || ValidateArtifact(a) != nil {
		return ErrInvalid
	}
	resp, e := c.request(ctx, "GET", p+"/artifacts/"+a.ID, token, lease, nil)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return refused(resp)
	}
	if resp.ContentLength != a.Size {
		return ErrIntegrity
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, a.Size+1))
	if e != nil || n != a.Size || fmt.Sprintf("%x", h.Sum(nil)) != a.SHA256 {
		return ErrIntegrity
	}
	return nil
}
