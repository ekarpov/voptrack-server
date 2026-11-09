// Package runtimeobs describes the exact child launched by the deployment wrapper.
// A live child observation is not a successful backup or scheduler observation.
package runtimeobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"os"
	"time"
)

type Receipt struct {
	Instance     string                `json:"instance"`
	PID          int                   `json:"pid"`
	ConfigSHA256 string                `json:"config_sha256"`
	Values       backupsettings.Values `json:"settings"`
	Started      time.Time             `json:"started_at"`
	Observed     time.Time             `json:"observed_at"`
	State        string                `json:"state"`
}

func Digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func Read(path string, now time.Time) (Receipt, error) {
	var r Receipt
	raw, e := os.ReadFile(path)
	if e != nil {
		return r, e
	}
	if json.Unmarshal(raw, &r) != nil || r.Instance == "" || len(r.ConfigSHA256) != 64 || r.Observed.IsZero() || !r.Values.Valid() {
		return Receipt{}, errors.New("invalid runtime observation")
	}
	if r.Observed.After(now.Add(time.Second)) || now.Sub(r.Observed) > 15*time.Second {
		r.State = "stale"
	}
	return r, nil
}
