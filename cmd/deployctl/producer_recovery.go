package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// recoverProducer is an offline action, never a server launcher. The caller holds
// the deployment's exclusive maintenance guard; Tessera owns destination locking,
// strict export validation and its resumable recovery transaction. The expected
// identity is an independent operator input, never inferred from the export.
func recoverProducer(ctx context.Context, configPath, executable, exportPath, expected string) error {
	if expected == "" {
		return os.ErrInvalid
	}
	private, err := os.MkdirTemp("", "voptrack-producer-recovery-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(private)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	config := filepath.Join(private, "server.json")
	if err = os.WriteFile(config, raw, 0400); err != nil {
		return err
	}
	f, err := os.OpenFile(exportPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return os.ErrInvalid
	}
	raw, err = io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return err
	}
	if len(raw) > 4096 {
		return os.ErrInvalid
	}
	export := filepath.Join(private, "settings-export.json")
	if err = os.WriteFile(export, raw, 0400); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, "-config", config, "-recover-backup-producer", "-expected-backup-producer", expected, "-backup-settings-bootstrap", export)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
