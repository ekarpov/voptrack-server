package backupcollector

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Status struct {
	State    string    `json:"state"`
	Observed time.Time `json:"observed_at"`
}

func (s Store) WriteStatus(state string) error {
	if state != "preparing" && state != "idle" && state != "unavailable" {
		return ErrStorage
	}
	if e := s.Init(); e != nil {
		return e
	}
	raw, _ := json.Marshal(Status{state, time.Now().UTC()})
	f, e := os.CreateTemp(s.Dir, ".status-")
	if e != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return ErrStorage
	}
	if os.Rename(f.Name(), filepath.Join(s.Dir, "status.json")) != nil {
		return ErrStorage
	}
	return syncDir(s.Dir)
}
func (s Store) Status() (Status, error) {
	var v Status
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return v, ErrStorage
	}
	defer root.Close()
	f, e := root.OpenFile("status.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return v, ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 4096 {
		return v, ErrStorage
	}
	raw, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || json.Unmarshal(raw, &v) != nil || time.Since(v.Observed) > 2*time.Minute || time.Until(v.Observed) > time.Minute {
		return v, ErrStorage
	}
	if v.State != "preparing" && v.State != "idle" && v.State != "unavailable" {
		return v, ErrStorage
	}
	return v, nil
}
