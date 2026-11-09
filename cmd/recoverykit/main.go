// recoverykit is a constrained local Unix-socket service, plus host-only approval.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

const socket = "/var/lib/voptrack-kit-control/kit.sock"

func main() {
	s := recoverykit.Store{Root: "/", Private: "/var/lib/voptrack-kits"}
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	var e error
	switch mode {
	case "approve", "revoke":
		if (mode == "approve" && len(os.Args) != 4) || (mode == "revoke" && len(os.Args) != 3) {
			os.Exit(2)
		}
		if mode == "approve" {
			e = s.Approve(os.Args[2], os.Args[3])
		} else {
			e = s.Revoke(os.Args[2])
		}
	case "prepare":
		_, e = s.Prepare()
	case "serve":
		// Compose guarantees one instance. Stale socket after a crash is not state.
		_ = os.Remove(socket)
		var l net.Listener
		l, e = net.Listen("unix", socket)
		if e == nil {
			e = os.Chmod(socket, 0600)
		}
		if e == nil {
			e = (&http.Server{Handler: handler(s), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 4096}).Serve(l)
		}
	default:
		e = recoverykit.ErrUnavailable
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "recovery kit operation unavailable")
		os.Exit(1)
	}
}
func handler(s recoverykit.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s.Status())
	})
	mux.HandleFunc("POST /prepare", func(w http.ResponseWriter, r *http.Request) {
		m, e := s.Prepare()
		if e != nil {
			http.Error(w, "unavailable", 409)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(m)
	})
	for _, action := range []string{"request", "deliver"} {
		mux.HandleFunc("POST /"+action, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			var in struct{ ID, Session, Operator string }
			d := json.NewDecoder(io.LimitReader(r.Body, 2048))
			d.DisallowUnknownFields()
			if d.Decode(&in) != nil {
				http.Error(w, "unavailable", 400)
				return
			}
			if action == "request" {
				q, e := s.Request(in.Session, in.Operator)
				if e != nil {
					http.Error(w, "unavailable", 409)
					return
				}
				_ = json.NewEncoder(w).Encode(struct {
					ID, Revision string
					Expires      time.Time
				}{q.ID, q.Revision, q.Expires})
				return
			}
			raw, e := s.Deliver(in.ID, in.Session, in.Operator)
			if e != nil {
				http.Error(w, "unavailable", 403)
				return
			}
			w.Header().Set("Content-Type", "application/x-tar")
			w.Header().Set("Content-Disposition", `attachment; filename="voptrack-recovery-kit.tar"`)
			w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
			_, _ = w.Write(raw)
		})
	}
	slots := make(chan struct{}, 2)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			mux.ServeHTTP(w, r)
		default:
			http.Error(w, "unavailable", 503)
		}
	})
}
