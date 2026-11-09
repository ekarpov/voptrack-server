// deployctl is a host-invoked deployment helper, never an HTTP service.
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/homesetup"
	"github.com/ekarpov/voptrack-server/internal/identitybrand"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	"github.com/ekarpov/voptrack-server/internal/websignin"
)

var errMaintenance = errors.New("maintenance unavailable: stop both console and tesserad; another maintenance operation may also be active")

const public = "/var/lib/voptrack-public"
const state = "/var/lib/voptrack-console"
const conf = "/var/lib/voptrack-config/server.json"
const data = "/var/lib/tessera"
const key = "/var/lib/voptrack-config/backup.key"

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		if errors.Is(err, errMaintenance) {
			fmt.Fprintln(os.Stderr, errMaintenance)
		}
		fmt.Fprintln(os.Stderr, "voptrack deployment: operation failed; keep services stopped, discard partial output and inspect the documented prerequisites")
		os.Exit(1)
	}
}

// Every supported deployment writer and secret/state producer holds the same
// inode locked. Export takes EX nonblocking: stopping only one service is not
// enough. CLI backup additionally takes Tessera's own exclusive dataset lock.
func maintenance(shared bool) (*os.File, error) {
	return maintenanceAt(filepath.Join(public, "maintenance.lock"), shared)
}
func maintenanceAt(path string, shared bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW
	if !shared {
		flags = os.O_RDWR | os.O_CREATE | syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return nil, err
	}
	op := syscall.LOCK_EX
	if shared {
		op = syscall.LOCK_SH
	}
	if err = syscall.Flock(int(f.Fd()), op|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func child(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case s := <-sig:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	return cmd.Wait()
}
func run(mode string) error {
	if mode == "restore-committed-kit" && os.Getenv("VOPTRACK_MAINTENANCE_HELD") == "yes" {
		raw, e := io.ReadAll(io.LimitReader(os.Stdin, recoverykit.MaxBytes+1))
		if e != nil {
			return e
		}
		return recoverykit.Restore("/", raw)
	}
	if mode == "settings" {
		h, e := strconv.Atoi(os.Getenv("VOPTRACK_BACKUP_EVERY_HOURS"))
		if e != nil {
			return e
		}
		k, e := strconv.Atoi(os.Getenv("VOPTRACK_BACKUP_KEEP"))
		if e != nil {
			return e
		}
		v := backupsettings.Values{Destination: "local", EveryHours: h, Keep: k}
		r, e := backupsettings.Read(state)
		if e != nil {
			return e
		}
		if expected := os.Getenv("VOPTRACK_BACKUP_REQUEST_REVISION"); expected != "" && expected != r.Revision {
			return backupsettings.ErrStale
		}
		if r.Revision != "none" {
			v = r.Values
		}
		v, e = backupsettings.StartupValues(state, v)
		if e != nil {
			return e
		}
		if !v.Valid() {
			return errors.New("invalid settings")
		}
		if info, e := os.Lstat(v.Directory()); e == nil && !info.IsDir() {
			return errors.New("invalid destination")
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
		fmt.Printf("%d %d %s\n", v.EveryHours, v.Keep, v.Directory())
		return nil
	}
	if mode == "publish" {
		var c struct {
			Backup struct {
				Directory  string `json:"directory"`
				EveryHours int    `json:"every_hours"`
				Keep       int    `json:"keep"`
			} `json:"backup"`
		}
		raw, e := os.ReadFile(conf)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &c); e != nil {
			return e
		}
		v := backupsettings.Values{Destination: "local", EveryHours: c.Backup.EveryHours, Keep: c.Backup.Keep}
		if c.Backup.Directory == "/var/lib/voptrack-backups/reserve" {
			v.Destination = "reserve"
		}
		if !v.Valid() || v.Directory() != c.Backup.Directory {
			return errors.New("unsupported configuration")
		}
		r, e := backupsettings.Read(state)
		if e != nil {
			return e
		}
		return backupsettings.WriteJSON(filepath.Join(public, "backup-settings.json"), backupsettings.Generated{Values: v, RequestRevision: r.Revision, At: time.Now().UTC()}, 0644)
	}
	if mode == "website-sign-in" {
		previous, e := os.ReadFile(conf)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		raw, e := websignin.Deployment(os.Getenv("VOPTRACK_WEBSITE_SIGN_IN"), os.Getenv("VOPTRACK_CONSOLE"), os.Getenv("VOPTRACK_WEBSITE_CLIENT_ID"), os.Getenv("VOPTRACK_WEBSITE_ORIGIN"), os.Getenv("VOPTRACK_WEBSITE_DATA_GRANTS"), os.Getenv("VOPTRACK_WEBSITE_ADMINISTER"), previous)
		if e != nil {
			return e
		}
		_, e = os.Stdout.Write(raw)
		return e
	}
	if mode == "website-sign-in-public" {
		raw, e := os.ReadFile(conf)
		if e != nil {
			return e
		}
		return websignin.Publish(websignin.PublicFile, os.Getenv("VOPTRACK_WEBSITE_ORIGIN"), raw)
	}
	if mode == "identity-brand" {
		_, e := os.Stat(conf)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		raw, e := identitybrand.Deployment(identitybrand.Input, os.Getenv("VOPTRACK_VARIANT"), os.Getenv("VOPTRACK_CONSOLE"), os.Getenv("VOPTRACK_IDENTITY_BRAND"), e == nil)
		if e != nil {
			return e
		}
		_, e = os.Stdout.Write(raw)
		return e
	}
	if mode == "serve" || mode == "console" {
		if homesetup.LaunchAllowed(homesetup.PendingFile) != nil {
			return homesetup.ErrRefused
		}
		if mode == "console" && homesetup.Apply(homesetup.PublicFile) != nil {
			return homesetup.ErrRefused
		}
	}
	shared := mode == "serve" || mode == "bootstrap-settings" || mode == "observe-settings" || mode == "console"
	lock, err := maintenance(shared)
	if err != nil {
		return errMaintenance
	}
	defer lock.Close()
	switch mode {
	case "observe-settings":
		return observeSettings(context.Background(), "https://tesserad:8443", public+"/server.crt", "/run/approved-token", state, public+"/backup-settings.json")
	case "recover-producer":
		return recoverProducer(context.Background(), conf, "/usr/local/bin/tesserad", "/run/backup-settings-export.json", os.Getenv("VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT"))
	case "bootstrap-settings":
		return observedServerBootstrap(context.Background(), conf, "/var/lib/voptrack-runtime/server.json", "/usr/local/bin/tesserad", "/run/backup-settings-export.json", os.Getenv("VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT"))
	case "serve":
		return observedServer(context.Background(), conf, "/var/lib/voptrack-runtime/server.json", "/usr/local/bin/tesserad")
	case "console":
		return child(context.Background(), "/usr/local/bin/voptrack-console")
	case "maintenance":
		os.Setenv("VOPTRACK_MAINTENANCE_HELD", "yes")
		return child(context.Background(), "/usr/local/bin/voptrack-init", os.Args[2:]...)
	case "restore-committed-kit":
		raw, e := io.ReadAll(io.LimitReader(os.Stdin, recoverykit.MaxBytes+1))
		if e != nil {
			return e
		}
		return recoverykit.Restore("/", raw)
	case "take-backup":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()
		return exportBackup(ctx, os.Stdout)
	case "recovery-kit":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()
		return exportKit(ctx, os.Stdout)
	default:
		return errors.New("unknown operation")
	}
}
func exportBackup(ctx context.Context, out io.Writer) error {

	var configuration struct {
		Engine  string `json:"engine"`
		DataDir string `json:"data_dir"`
	}
	raw, err := os.ReadFile(conf)
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &configuration) != nil || configuration.Engine != "sqlite" || configuration.DataDir != data {
		return errors.New("unsupported recovery configuration")
	}
	work, err := os.MkdirTemp("", "voptrack-recovery-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	name := "tessera-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".enc"
	path := filepath.Join(work, name)
	// Refuse a missing ledger before opening Tessera: opening an old database
	// must never manufacture a replacement for required recovery evidence.
	if err = copyRegular(filepath.Join(data, "dataset.db.retired"), filepath.Join(work, "fence-before")); err != nil {
		return errors.New("current fence unavailable")
	}

	// Never select an online filename. Successful completion of this supported
	// offline command is the completion proof for this exact private archive.
	cmd := exec.CommandContext(ctx, "/usr/local/bin/tesserad", "-config", conf, "-backup", path, "-backup-key-file", key)
	if err = cmd.Run(); err != nil {
		return errors.New("offline backup failed")
	}
	if err = copyRegular(filepath.Join(data, "dataset.db.retired"), filepath.Join(work, "dataset.db.retired")); err != nil {
		return err
	}

	before, err := fileDigest(filepath.Join(work, "fence-before"))
	if err != nil {
		return err
	}
	after, err := fileDigest(filepath.Join(work, "dataset.db.retired"))
	if err != nil || !bytes.Equal(before, after) {
		return errors.New("retirement fence changed")
	}
	// Stage the complete tar before stdout. The exclusive maintenance lock pins
	// the fence after the offline snapshot until delivery finishes or aborts.
	return stagedTar(contextWriter{ctx, out}, work, []string{name, "dataset.db.retired"})
}
func copyRegular(src, dst string) error {
	f, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("missing recovery material")
	}
	d, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.CopyN(d, f, info.Size())
	if err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return errors.New("changed recovery material")
	}
	current, err := os.Lstat(src)
	if err != nil || !os.SameFile(current, info) {
		return errors.New("replaced recovery material")
	}
	return d.Close()
}
func stagedTar(out io.Writer, base string, names []string) error {
	stage, err := os.CreateTemp("", "voptrack-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	tw := tar.NewWriter(stage)
	for _, name := range names {
		err = filepath.Walk(filepath.Join(base, name), func(path string, info os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return errors.New("unsupported recovery material")
			}
			relative, e := filepath.Rel(base, path)
			if e != nil {
				return e
			}
			h, e := tar.FileInfoHeader(info, "")
			if e != nil {
				return e
			}
			h.Name = relative
			if e = tw.WriteHeader(h); e != nil {
				return e
			}
			if info.IsDir() {
				return nil
			}
			f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				return e
			}
			defer f.Close()
			opened, e := f.Stat()
			if e != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
				return errors.New("changed recovery material")
			}
			_, e = io.CopyN(tw, f, info.Size())
			if e != nil {
				return e
			}
			after, e := f.Stat()
			if e != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
				return errors.New("changed recovery material")
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if _, err = stage.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(out, stage)
	return err
}
func exportKit(ctx context.Context, out io.Writer) error {
	if _, err := os.Stat("/var/lib/voptrack-bootstrap/owner-password"); err != nil {
		return err
	}
	// The maintenance lock also excludes Console, so service/bootstrap material
	// cannot change underneath this snapshot. No changing retirement fence here.
	return stagedTar(contextWriter{ctx, out}, "/", []string{"var/lib/voptrack-config/backup.key", "var/lib/voptrack-console", "var/lib/voptrack-bootstrap", "var/lib/voptrack-ca"})
}

type contextWriter struct {
	context.Context
	io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.Err(); err != nil {
		return 0, err
	}
	return w.Writer.Write(p)
}

func fileDigest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
