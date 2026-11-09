// homesetup is a separate one-use host service, never an ordinary Console route.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ekarpov/voptrack-server/internal/homesetup"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(k, d string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return d
}
func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "Home setup unavailable; preserve partial targets and inspect host authorization")
		os.Exit(1)
	}
}
func run() error {
	state := env("VOPTRACK_SETUP_STATE", "/var/lib/voptrack-setup")
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "apply":
		if len(os.Args) != 3 || (os.Args[2] != "init" && os.Args[2] != "restore") {
			return homesetup.ErrRefused
		}
		storageAttestation := os.Getenv("VOPTRACK_STORAGE_ENCRYPTED")
		if homesetup.Apply(homesetup.SettingsFile) != nil {
			return homesetup.ErrRefused
		}
		// A restored kit describes the old host. It cannot attest encryption
		// of this destination; preserve the current operator's explicit value.
		if os.Args[2] == "restore" {
			os.Setenv("VOPTRACK_STORAGE_ENCRYPTED", storageAttestation)
		}
		os.Setenv("VOPTRACK_SETUP_APPLIED", "yes")
		return syscall.Exec("/usr/local/bin/voptrack-init", []string{"voptrack-init", os.Args[2]}, os.Environ())
	case "status":
		if len(os.Args) != 2 {
			return homesetup.ErrRefused
		}
		phase, e := homesetup.Status(state)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"phase": phase})
	case "access":
		if len(os.Args) != 2 {
			return homesetup.ErrRefused
		}
		v, e := homesetup.Access(state)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(v)
	case "authorize":
		if len(os.Args) != 2 {
			return homesetup.ErrRefused
		}
		return homesetup.Authorize(state)
	case "serve":
		if len(os.Args) > 2 || env("VOPTRACK_VARIANT", "") != "household" {
			return homesetup.ErrRefused
		}
	default:
		return homesetup.ErrRefused
	}
	public := env("VOPTRACK_PUBLIC_URL", "https://"+env("VOPTRACK_DOMAIN", "localhost"))
	s, e := homesetup.New(homesetup.Config{State: state, Root: "/", Origin: env("VOPTRACK_SETUP_ORIGIN", "https://localhost:9673"), PublicURL: public})
	if e != nil {
		return e
	}
	defer s.Close()
	if s.Complete() {
		return s.ReleaseCompleted()
	}
	config, e := s.TLS()
	if e != nil {
		return e
	}
	server := &http.Server{ErrorLog: log.New(io.Discard, "", 0), Addr: env("VOPTRACK_SETUP_LISTEN", ":9673"), Handler: s, TLSConfig: config, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServeTLS("", "") }()
	select {
	case e = <-done:
		if e == http.ErrServerClosed {
			return nil
		}
		return e
	case <-ctx.Done():
		e = ctx.Err()
	case <-s.Done():
		e = nil
	}
	shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = server.Shutdown(shutdown)
	return e
}
