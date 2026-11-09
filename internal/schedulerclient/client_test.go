package schedulerclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixture() Observation {
	now := time.Now().UTC()
	scope := strings.Repeat("a", 32) + "." + strings.Repeat("b", 32)
	revision := scope + "." + strings.Repeat("c", 32)
	return Observation{InstanceID: strings.Repeat("d", 32), ObservedAt: now, HistoryScope: "process_instance", Configured: true, Available: true, Enabled: true, Reason: "ready", Settings: &Settings{24, 7, revision}, SettingsScope: &scope, Next: &now, NextReason: "scheduled", Retention: &Retention{7, 7, "converged", "at_target"}, Export: &Export{1, strings.Repeat("a", 32), 24, 7, revision, now}}
}
func TestObservationShapeAndSanitization(t *testing.T) {
	base := fixture()
	if e := base.Validate(time.Now()); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*Observation){"stale": func(o *Observation) { o.ObservedAt = o.ObservedAt.Add(-time.Minute) }, "enum": func(o *Observation) { o.NextReason = "private/path" }, "scope": func(o *Observation) { *o.SettingsScope = strings.Repeat("e", 32) + "." + strings.Repeat("f", 32) }, "export": func(o *Observation) { o.Export.Keep = 3 }, "retention": func(o *Observation) { o.Retention.Reason = "secret error" }, "nullable": func(o *Observation) { o.Next = nil }} {
		t.Run(name, func(t *testing.T) {
			o := fixture()
			change(&o)
			if o.Validate(time.Now()) == nil {
				t.Fatal("malformed observation accepted")
			}
		})
	}
	for _, missing := range []string{"configured", "available", "enabled", "settings_export", "pending_request_id"} {
		t.Run(missing, func(t *testing.T) {
			o := fixture()
			b, _ := json.Marshal(o)
			var fields map[string]any
			json.Unmarshal(b, &fields)
			delete(fields, missing)
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(fields) }))
			defer srv.Close()
			c, _ := New(srv.URL, srv.Client())
			if _, e := c.Observe(t.Context(), "ephemeral", time.Now()); e == nil {
				t.Fatal("missing field accepted")
			}
		})
	}
}
func TestSettingsResponsesAndPendingExpiry(t *testing.T) {
	o := fixture()
	p := Payload{o.Settings.Revision, 12, 2}
	id, _ := NewID(*o.SettingsScope, time.Now())
	now := time.Now().UTC()
	op := Operation{ID: id, Payload: p, State: "pending", AcceptedAt: now.Add(-9 * 24 * time.Hour), ExpiresAt: now.Add(-2 * 24 * time.Hour)}
	status := 202
	var body any = op
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ephemeral" {
			t.Error("authority not per-call")
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	if got, e := c.Operation(t.Context(), id, p, "ephemeral", true); e != nil || got.State != "pending" {
		t.Fatal("pending provisional expiry", got, e)
	}
	op.State = "effective"
	op.EffectiveAt = &now
	op.Revision = *o.SettingsScope + "." + strings.Repeat("e", 32)
	op.ExpiresAt = now.Add(7 * 24 * time.Hour)
	status = 200
	body = op
	if got, e := c.Operation(t.Context(), id, p, "ephemeral", true); e != nil || got.State != "effective" {
		t.Fatal("200 effective replay", got, e)
	}
	op.State = "failed"
	op.Failure = "stale_revision"
	op.Revision = ""
	op.EffectiveAt = nil
	status = 409
	body = map[string]any{"error": map[string]any{"code": "conflict", "details": map[string]any{"reason": "stale_revision", "operation": op}}}
	if got, e := c.Operation(t.Context(), id, p, "ephemeral", true); e == nil || got.State != "failed" {
		t.Fatal("retained rejection lost", got, e)
	}
	for _, reason := range []string{"settings_busy", "request_reused", "request_scope_mismatch", "settings_unavailable"} {
		status = 409
		if reason == "settings_unavailable" {
			status = 503
		}
		body = map[string]any{"error": map[string]any{"code": map[bool]string{true: "dataset_unavailable", false: "conflict"}[status == 503], "details": map[string]any{"reason": reason}}}
		got, e := c.Operation(t.Context(), id, p, "ephemeral", true)
		var refusal *Refusal
		if !errors.As(e, &refusal) || refusal.Reason != reason || got.ID != "" {
			t.Fatal(reason, got, e)
		}
	}
}
func TestScopeIsNotAcquisitionIdentity(t *testing.T) {
	o := fixture()
	id, e := NewID(*o.SettingsScope, time.Now())
	if e != nil || !ValidID(id) || ValidID("123-"+strings.Repeat("a", 32)) || ValidRevision(*o.SettingsScope) {
		t.Fatal(id, e)
	}
}

func TestNullHoursCannotMasqueradeAsDisabled(t *testing.T) {
	o := fixture()
	o.Settings.EveryHours = 0
	o.Export.EveryHours = 0
	o.Enabled = false
	o.Next = nil
	o.NextReason = "disabled"
	b, _ := json.Marshal(o)
	var fields map[string]any
	json.Unmarshal(b, &fields)
	fields["settings"].(map[string]any)["every_hours"] = nil
	fields["settings_export"].(map[string]any)["every_hours"] = nil
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(fields) }))
	defer srv.Close()
	c, _ := New(srv.URL, srv.Client())
	if _, e := c.Observe(t.Context(), "ephemeral", time.Now()); e == nil {
		t.Fatal("null hours incorrectly acknowledged disabled settings")
	}
}
