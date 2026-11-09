// Test-only TLS fault interposition for disposable deployments. No credentials
// are stored, logged, minted or owned. Never included in the deployment image.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func main() {
	cert, e := os.ReadFile("/config/server.crt")
	if e != nil {
		os.Exit(1)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		os.Exit(1)
	}
	target, _ := url.Parse("https://tesserad:8443")
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		r.Header["X-Forwarded-For"] = nil
		r.Header.Del("Forwarded")
		r.Header.Del("X-Forwarded-Host")
		r.Header.Del("X-Forwarded-Proto")
	}
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	lost := errors.New("test response loss")
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ModifyResponse = func(r *http.Response) error {
		if !strings.HasPrefix(r.Request.URL.Path, "/admin/api/v1/backup-settings-operations/") || r.StatusCode >= 300 {
			return nil
		}
		mode, _ := os.ReadFile("/fault/mode")
		action := strings.TrimSpace(string(mode))
		if (r.Request.Method == "PUT" && (action == "drop-put" || action == "503-put")) || (r.Request.Method == "GET" && action == "drop-get") {
			if os.WriteFile("/fault/mode", nil, 0600) != nil {
				return lost
			}
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if action != "503-put" {
				return lost
			}
			body := `{"error":{"code":"dataset_unavailable","details":{"reason":"settings_unavailable"}}}`
			r.StatusCode = 503
			r.Status = "503 Service Unavailable"
			r.Body = io.NopCloser(strings.NewReader(body))
			r.ContentLength = int64(len(body))
			r.Header.Del("Content-Length")
			r.Header.Set("Cache-Control", "no-store")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		if errors.Is(e, lost) {
			if h, ok := w.(http.Hijacker); ok {
				c, _, err := h.Hijack()
				if err == nil {
					c.Close()
				}
			}
			return
		}
		w.WriteHeader(503)
	}
	server := &http.Server{Addr: ":9443", Handler: proxy, ErrorLog: log.New(io.Discard, "", 0)}
	if server.ListenAndServeTLS("/fault/crt", "/fault/key") != nil {
		os.Exit(1)
	}
}
