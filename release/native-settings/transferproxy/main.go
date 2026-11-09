// Test-only collector transfer pause. Never included in the deployment image.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type relay struct {
	target, control string
	transport       http.RoundTripper
	claimed         atomic.Bool
	timeout         time.Duration
}

func (p *relay) mark(name string) bool {
	return os.WriteFile(filepath.Join(p.control, name), []byte("1\n"), 0600) == nil
}
func (p *relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	request := r.Clone(r.Context())
	request.RequestURI = ""
	request.URL.Scheme = "https"
	request.URL.Host = p.target
	request.Host = p.target
	// Unit selfcheck uses an HTTP transport only; production invocation is HTTPS.
	if strings.HasPrefix(p.target, "http://") {
		request.URL.Scheme = "http"
		request.URL.Host = strings.TrimPrefix(p.target, "http://")
		request.Host = request.URL.Host
	}
	request.Header = request.Header.Clone()
	for _, h := range []string{"Connection", "Proxy-Connection", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		request.Header.Del(h)
	}
	response, err := p.transport.RoundTrip(request)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for k, v := range response.Header {
		if k != "Connection" && k != "Transfer-Encoding" {
			w.Header()[k] = v
		}
	}
	pause := r.Method == "GET" && strings.Contains(r.URL.Path, "/admin/api/v1/backup-operations/") && strings.Contains(r.URL.Path, "/artifacts/") && (response.StatusCode == 200 || response.StatusCode == 206) && p.claimed.CompareAndSwap(false, true)
	if !pause {
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
		return
	}
	first := make([]byte, 1)
	if _, err = io.ReadFull(response.Body, first); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		p.mark("empty")
		return
	}
	w.WriteHeader(response.StatusCode)
	if _, err = w.Write(first); err != nil {
		p.mark("aborted")
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	} else {
		p.mark("unflushed")
		return
	}
	if !p.mark("started") {
		return
	}
	deadline := time.NewTimer(p.timeout)
	defer deadline.Stop()
	tick := time.NewTicker(15 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			p.mark("aborted")
			return
		case <-deadline.C:
			p.mark("expired")
			return
		case <-tick.C:
			if _, err = os.Stat(filepath.Join(p.control, "release")); err == nil {
				p.mark("released")
				io.Copy(w, response.Body)
				return
			}
		}
	}
}
func selfcheck() error {
	dir, err := os.MkdirTemp("", "transferproxy-selfcheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	content := bytes.Repeat([]byte("synthetic transfer bytes"), 200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
	defer upstream.Close()
	proxy := httptest.NewServer(&relay{target: upstream.URL, control: dir, transport: http.DefaultTransport, timeout: 3 * time.Second})
	defer proxy.Close()
	response, err := http.Get(proxy.URL + "/admin/api/v1/backup-operations/synthetic/artifacts/archive")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	first := make([]byte, 1)
	if _, err = io.ReadFull(response.Body, first); err != nil {
		return err
	}
	for n := 0; n < 100; n++ {
		if _, err = os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if _, err = os.Stat(filepath.Join(dir, "started")); err != nil {
		return errors.New("pause marker absent")
	}
	result := make(chan []byte, 1)
	go func() { rest, _ := io.ReadAll(response.Body); result <- append(first, rest...) }()
	select {
	case <-result:
		return errors.New("artifact was not paused")
	case <-time.After(50 * time.Millisecond):
	}
	if err = os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		return err
	}
	select {
	case got := <-result:
		if !bytes.Equal(got, content) {
			return errors.New("artifact bytes changed")
		}
	case <-time.After(time.Second):
		return errors.New("release stalled")
	}
	// A missing release must stop within the test bound, never hold collection indefinitely.
	for _, name := range []string{"started", "released", "release"} {
		os.Remove(filepath.Join(dir, name))
	}
	bounded := httptest.NewServer(&relay{target: upstream.URL, control: dir, transport: http.DefaultTransport, timeout: 30 * time.Millisecond})
	defer bounded.Close()
	timed, err := http.Get(bounded.URL + "/admin/api/v1/backup-operations/synthetic/artifacts/archive")
	if err != nil {
		return err
	}
	short, _ := io.ReadAll(timed.Body)
	timed.Body.Close()
	if len(short) != 1 {
		return errors.New("bounded pause did not truncate unreleased transfer")
	}
	if _, err = os.Stat(filepath.Join(dir, "expired")); err != nil {
		return errors.New("bounded pause expiry absent")
	}
	return nil
}
func main() {
	check := flag.Bool("selfcheck", false, "run synthetic bounded pause selfcheck")
	lock := flag.String("writer-lock", "", "check task collector writer lock is held")
	flag.Parse()
	if *check {
		if selfcheck() != nil {
			os.Exit(1)
		}
		return
	}
	if *lock != "" {
		f, e := os.Open(*lock)
		if e != nil {
			os.Exit(2)
		}
		defer f.Close()
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
			return
		}
		if e == nil {
			syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
		os.Exit(2)
	}
	cert, err := os.ReadFile("/config/server.crt")
	if err != nil {
		os.Exit(1)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		os.Exit(1)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 30 * time.Second}
	handler := &relay{target: "tesserad:8443", control: "/fault-control", transport: transport, timeout: 90 * time.Second}
	server := &http.Server{Addr: ":8443", Handler: handler, ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 120 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	pair, err := tls.LoadX509KeyPair("/config/server.crt", "/config/server.key")
	if err != nil {
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", ":8443")
	if err != nil {
		os.Exit(1)
	}
	if !handler.mark("ready") {
		listener.Close()
		os.Exit(1)
	}
	if server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})) != nil {
		os.Exit(1)
	}
}
