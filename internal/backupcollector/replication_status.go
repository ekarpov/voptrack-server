package backupcollector

import (
	"time"
)

type ReplicationStatus struct {
	Version                                int
	Label, State                           string
	Observed                               time.Time
	LastVerified, LastBackup, LastEvidence time.Time
	Pending                                int
	PendingBytes, Budget                   int64
	OldestPending                          time.Time
	IndependentDestination                 bool
	SettingsAvailable                      bool
}

func ReadReplicationStatus(path string, now time.Time) (s ReplicationStatus, e error) {
	if path == "" {
		s.State = "disabled"
		return
	}
	if ReadPrivateJSON(path, &s) != nil || s.Version != 1 || !SafeLabel(s.Label) || s.Pending < 0 || s.PendingBytes < 0 || s.Budget < 0 {
		s = ReplicationStatus{State: "unknown"}
		return s, ErrStorage
	}
	switch s.State {
	case "unprotected", "copying", "verified", "unavailable", "degraded", "stale":
	default:
		s.State = "unknown"
		return s, ErrStorage
	}
	if s.Observed.IsZero() || s.Observed.After(now.Add(5*time.Minute)) || now.Sub(s.Observed) > 2*time.Minute {
		s.State = "unknown"
	}
	if s.LastVerified.After(now.Add(5*time.Minute)) || s.LastBackup.After(now.Add(5*time.Minute)) || s.LastEvidence.After(now.Add(5*time.Minute)) || s.OldestPending.After(now.Add(5*time.Minute)) {
		s.State = "unknown"
	}
	if s.State == "verified" && (s.LastVerified.IsZero() || now.Sub(s.LastVerified) > 26*time.Hour) {
		s.State = "stale"
	}
	return
}
