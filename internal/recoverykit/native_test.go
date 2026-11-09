package recoverykit

import (
	"encoding/json"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeExportFreshnessAndTrustedDeployment(t *testing.T) {
	s := fixture(t)
	now := time.Now().UTC()
	s.Now = func() time.Time { return now }
	scope := "0123456789abcdef0123456789abcdef." + strings.Repeat("b", 32)
	revision := scope + "." + strings.Repeat("c", 32)
	o := sc.Observation{InstanceID: strings.Repeat("d", 32), ObservedAt: now, HistoryScope: "process_instance", Configured: true, Available: true, Enabled: true, Reason: "ready", Settings: &sc.Settings{EveryHours: 24, Keep: 7, Revision: revision}, SettingsScope: &scope, Next: &now, NextReason: "scheduled", Retention: &sc.Retention{Retained: 7, Target: 7, State: "converged", Reason: "at_target"}, Export: &sc.Export{Version: 1, Deployment: "0123456789abcdef0123456789abcdef", EveryHours: 24, Keep: 7, Revision: revision, ExportedAt: now}}
	save := func() {
		t.Helper()
		must(t, bs.WithLive(s.path("voptrack-console"), func(v *bs.Live, save func() error) error { v.Observation = &o; return save() }))
	}
	save()
	m, e := s.Prepare()
	must(t, e)
	if m.SettingsRevision != revision {
		t.Fatal("missing native provenance")
	}
	raw, e := os.ReadFile(filepath.Join(s.Private, m.Revision+".tar"))
	must(t, e)
	files, e := Validate(raw)
	must(t, e)
	var export sc.Export
	must(t, json.Unmarshal(files["voptrack-bootstrap/backup-settings-export.json"], &export))
	if export.Revision != revision {
		t.Fatal("export not included")
	}
	o.Settings.Revision = scope + "." + strings.Repeat("e", 32)
	o.Export.Revision = o.Settings.Revision
	save()
	if s.Status().State != "stale" {
		t.Fatal("effective change left old kit current")
	}
	next, e := s.Prepare()
	must(t, e)
	if next.Revision == m.Revision {
		t.Fatal("new settings did not produce new kit")
	}
	// Observation freshness is a separate requirement from archive integrity.
	now = now.Add(time.Minute)
	if s.Status().State != "unknown" {
		t.Fatal("stale observation advertised current")
	}
	if _, e = s.Prepare(); e == nil {
		t.Fatal("prepared from stale observation")
	}
	still, e := os.ReadFile(filepath.Join(s.Private, m.Revision+".tar"))
	must(t, e)
	if string(still) != string(raw) {
		t.Fatal("failed preparation destroyed usable old kit")
	}
	now = o.ObservedAt
	o.Export.Deployment = strings.Repeat("f", 32)
	o.Export.Revision = o.Export.Deployment + "." + strings.Repeat("b", 32) + "." + strings.Repeat("e", 32)
	o.Settings.Revision = o.Export.Revision
	newScope := sc.Scope(o.Export.Revision)
	o.SettingsScope = &newScope
	save()
	if _, e = s.Prepare(); e == nil {
		t.Fatal("adopted foreign export lineage")
	}
}

func TestNoNativeObservationCannotAdvertiseCurrentKit(t *testing.T) {
	s := fixture(t)
	m, e := s.Prepare()
	must(t, e)
	must(t, os.Remove(filepath.Join(s.path("voptrack-console"), "backup-live.json")))
	if s.Status().State == "ready" {
		t.Fatal("missing effective-settings evidence called current")
	}
	if _, e = s.Prepare(); e == nil {
		t.Fatal("kit prepared without native export")
	}
	if _, e = os.Stat(filepath.Join(s.Private, m.Revision+".tar")); e != nil {
		t.Fatal("prior kit lost", e)
	}
}

func TestAcknowledgementRequiresFreshExportForEveryKitPath(t *testing.T) {
	for _, state := range []string{"before_ack", "superseded_revision", "wrong_values", "missing_outcome", "later_failed_command", "equal_time_other_revision"} {
		t.Run(state, func(t *testing.T) {
			s := fixture(t)
			session, operator, newSession := strings.Repeat("1", 48), strings.Repeat("2", 48), strings.Repeat("3", 48)
			m, e := s.Prepare()
			must(t, e)
			r, e := s.Request(session, operator)
			must(t, e)
			must(t, s.Approve(r.ID, r.Revision))
			original, e := os.ReadFile(filepath.Join(s.Private, m.Revision+".tar"))
			must(t, e)
			dir := s.path("voptrack-console")
			must(t, bs.WithLive(dir, func(l *bs.Live, save func() error) error {
				o := l.Observation
				before := o.ObservedAt
				applied := before.Add(time.Millisecond)
				s.Now = func() time.Time { return applied.Add(time.Millisecond) }
				p := sc.Payload{ExpectedRevision: o.Settings.Revision, EveryHours: 12, Keep: 2}
				if e := l.Begin(l.Fingerprint(), p, before); e != nil {
					return e
				}
				c := l.Current()
				revision := sc.Scope(p.ExpectedRevision) + "." + strings.Repeat("e", 32)
				c.State = "effective"
				c.Outcome = &sc.Operation{ID: c.ID, Payload: p, State: "effective", AcceptedAt: before, ExpiresAt: before.Add(time.Hour), EffectiveAt: &applied, Revision: revision}
				switch state {
				case "superseded_revision":
					o.ObservedAt = applied.Add(time.Millisecond)
					o.Export.ExportedAt = o.ObservedAt
				case "wrong_values":
					o.ObservedAt, o.Export.ExportedAt = applied, applied
					o.Settings.Revision, o.Export.Revision = revision, revision
				case "missing_outcome":
					c.Outcome = nil
				case "later_failed_command":
					if e := l.Begin(l.Fingerprint(), sc.Payload{ExpectedRevision: revision, EveryHours: 6, Keep: 3}, applied); e != nil {
						return e
					}
					c = l.Current()
					c.State = "failed"
					c.Outcome = &sc.Operation{ID: c.ID, Payload: c.Payload, State: "failed", Failure: "stale_revision", AcceptedAt: applied, ExpiresAt: applied.Add(time.Hour)}
				case "equal_time_other_revision":
					o.ObservedAt, o.Export.ExportedAt = applied, applied
					o.Settings.Revision = sc.Scope(revision) + "." + strings.Repeat("f", 32)
					o.Export.Revision = o.Settings.Revision
				}
				return save()
			}))
			if _, e := bs.StartupValues(dir, bs.Values{Destination: "local", EveryHours: 24, Keep: 7}); e == nil {
				t.Fatal("startup accepted pre-acknowledgement policy")
			}
			if s.Status().State == "ready" {
				t.Fatal("stale acknowledged policy advertised current")
			}
			if _, e := s.Prepare(); e == nil {
				t.Fatal("prepared from pre-acknowledgement export")
			}
			if _, e := s.Request(newSession, operator); e == nil {
				t.Fatal("approved request basis from stale export")
			}
			if _, e := s.Deliver(r.ID, session, operator); e == nil {
				t.Fatal("delivered from pre-acknowledgement export")
			}
			retained, e := os.ReadFile(filepath.Join(s.Private, m.Revision+".tar"))
			must(t, e)
			if string(retained) != string(original) {
				t.Fatal("rejection changed prior usable kit")
			}
			// A fresh authoritative R2 observation repairs readiness. Drop the
			// deliberately malformed outcome only in its own fixture.
			must(t, bs.WithLive(dir, func(l *bs.Live, save func() error) error {
				c := &l.Commands[0]
				applied := s.Now().Add(-time.Millisecond)
				if c.Outcome == nil {
					c.Outcome = &sc.Operation{ID: c.ID, Payload: c.Payload, State: "effective", AcceptedAt: applied, ExpiresAt: applied.Add(time.Hour), EffectiveAt: &applied, Revision: sc.Scope(c.Payload.ExpectedRevision) + "." + strings.Repeat("e", 32)}
				}
				o := l.Observation
				o.ObservedAt, o.Export.ExportedAt = s.Now(), s.Now()
				o.Settings.Revision, o.Export.Revision = c.Outcome.Revision, c.Outcome.Revision
				o.Settings.EveryHours, o.Export.EveryHours = c.Payload.EveryHours, c.Payload.EveryHours
				o.Settings.Keep, o.Export.Keep, o.Retention.Target = c.Payload.Keep, c.Payload.Keep, c.Payload.Keep
				return save()
			}))
			fresh, e := s.Prepare()
			must(t, e)
			if fresh.SettingsRevision == m.SettingsRevision || s.Status().State != "ready" {
				t.Fatal("fresh acknowledgement did not restore readiness")
			}
			newRequest, e := s.Request(newSession, operator)
			must(t, e)
			must(t, s.Approve(newRequest.ID, newRequest.Revision))
			if _, e := s.Deliver(newRequest.ID, newSession, operator); e != nil {
				t.Fatal("fresh kit not deliverable", e)
			}
		})
	}
}
