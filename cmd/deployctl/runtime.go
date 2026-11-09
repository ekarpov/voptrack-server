package main

import (
	"context"
	"encoding/json"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"github.com/ekarpov/voptrack-server/internal/runtimeobs"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// The child's -config names a private immutable copy of the very bytes hashed.
// The shared maintenance guard still excludes init/configuration changes.
func observedServer(ctx context.Context, configPath, receiptPath, executable string) error {
	return observedServerBootstrap(ctx, configPath, receiptPath, executable, "", "")
}
func observedServerBootstrap(ctx context.Context, configPath, receiptPath, executable, exportPath, trustedDeployment string) error {
	owner, e := maintenanceAt(filepath.Join(filepath.Dir(receiptPath), ".runtime.lock"), false)
	if e != nil {
		return e
	}
	defer owner.Close()
	raw, e := os.ReadFile(configPath)
	if e != nil {
		return e
	}
	var c struct {
		Backup struct {
			Directory  string `json:"directory"`
			EveryHours int    `json:"every_hours"`
			Keep       int    `json:"keep"`
		} `json:"backup"`
	}
	if e = json.Unmarshal(raw, &c); e != nil {
		return e
	}
	v := backupsettings.Values{Destination: "local", EveryHours: c.Backup.EveryHours, Keep: c.Backup.Keep}
	if c.Backup.Directory == "/var/lib/voptrack-backups/reserve" {
		v.Destination = "reserve"
	}
	// Unsupported settings cannot be published as a valid effective receipt.
	if !v.Valid() || v.Directory() != c.Backup.Directory {
		return os.ErrInvalid
	}
	private, e := os.MkdirTemp("", "voptrack-launch-*")
	if e != nil {
		return e
	}
	defer os.RemoveAll(private)
	pinned := filepath.Join(private, "server.json")
	if e = os.WriteFile(pinned, raw, 0400); e != nil {
		return e
	}
	r := runtimeobs.Receipt{Instance: recoverymaterial.ID(), ConfigSHA256: runtimeobs.Digest(raw), Values: v, Started: time.Now().UTC(), State: "starting"}
	publish := func() error { r.Observed = time.Now().UTC(); return backupsettings.WriteJSON(receiptPath, r, 0644) }
	if e = publish(); e != nil {
		return e
	}
	args := []string{"-config", pinned}
	if exportPath != "" {
		f, err := os.OpenFile(exportPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			f.Close()
			return os.ErrInvalid
		}
		b, err := io.ReadAll(io.LimitReader(f, 4097))
		f.Close()
		if err != nil || len(b) > 4096 {
			return os.ErrInvalid
		}
		var export sc.Export
		if json.Unmarshal(b, &export) != nil || !export.Valid() || trustedDeployment == "" || export.Deployment != trustedDeployment {
			return os.ErrInvalid
		}
		path := filepath.Join(private, "settings-export.json")
		if err = os.WriteFile(path, b, 0400); err != nil {
			return err
		}
		args = append(args, "-backup-settings-bootstrap", path)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if e = cmd.Start(); e != nil {
		r.State = "failed"
		_ = publish()
		return e
	}
	r.PID = cmd.Process.Pid
	r.State = "running"
	// Wait owns child reaping. Receipt heartbeat never infers scheduler health.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	if e = publish(); e != nil {
		_ = cmd.Process.Kill()
		<-done
		return e
	}
	for {
		select {
		case e = <-done:
			r.State = "exited"
			if e != nil {
				r.State = "failed"
			}
			_ = publish()
			return e
		case s := <-signals:
			_ = cmd.Process.Signal(s)
		case <-tick.C:
			if e = publish(); e != nil {
				_ = cmd.Process.Kill()
				<-done
				return e
			}
		}
	}
}
