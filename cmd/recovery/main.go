// voptrack-recovery is explicitly host-started and serves TLS itself.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	gr "github.com/ekarpov/voptrack-server/internal/guidedrecovery"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "recovery refused; retain inputs and review host configuration")
		os.Exit(1)
	}
}
func run() error {
	config := flag.String("config", "", "private host configuration file")
	mode := flag.String("mode", "serve", "serve or authorize (host-only while stopped)")
	token := flag.String("token-file", "", "new private output file for host authorization; never printed")
	listen := flag.String("listen", "", "explicit protected listener, for example 127.0.0.1:9543")
	cert := flag.String("tls-cert", "", "host-provisioned TLS certificate")
	key := flag.String("tls-key", "", "host-provisioned TLS private key")
	flag.Parse()
	if flag.NArg() != 0 {
		return os.ErrInvalid
	}
	f, e := os.Open(*config)
	if e != nil {
		return e
	}
	defer f.Close()
	var c gr.Config
	d := json.NewDecoder(io.LimitReader(f, 16385))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return os.ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return os.ErrInvalid
	}
	app, e := gr.Open(c)
	if e != nil {
		return e
	}
	defer app.Close()
	if *mode == "authorize" {
		return app.Authorize(*token)
	}
	if *mode != "serve" || *listen == "" || *cert == "" || *key == "" {
		return os.ErrInvalid
	}
	// Bind only the explicit host address. No fallback wildcard listener, proxy
	// TLS trust or first-visitor authorization is accepted.
	host, _, e := net.SplitHostPort(*listen)
	if e != nil || net.ParseIP(host) == nil {
		return os.ErrInvalid
	}
	server := &http.Server{Addr: *listen, Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = server.Close()
				return
			case <-ticker.C:
				app.Expire()
			}
		}
	}()
	e = server.ListenAndServeTLS(*cert, *key)
	stop()
	<-done
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
