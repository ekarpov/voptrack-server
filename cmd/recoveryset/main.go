// recoveryset is an offline, host-invoked preflight; it never restores or starts services.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ekarpov/voptrack-server/internal/recoveryset"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	directory := flag.String("inputs", "/recovery-inputs", "operator-controlled retained selection directory")
	trust := flag.String("trust-file", "/recovery-trust/backup-lineage.json", "independently retained producer lineage")
	key := flag.String("key-file", "/recovery-key/backup.key", "separately retained 32-byte backup key")
	flag.Parse()
	if flag.NArg() != 0 {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	receipt, err := recoveryset.Preflight(ctx, recoveryset.Options{Directory: *directory, TrustFile: *trust, KeyFile: *key, Verifier: "/usr/local/lib/tessera/verify_acquisition.py"})
	if err != nil {
		fmt.Fprintln(os.Stderr, recoveryset.ErrInvalid)
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(receipt) != nil {
		os.Exit(1)
	}
}
