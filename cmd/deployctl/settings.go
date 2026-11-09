package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// A one-shot host observation with current authority; no token is written or
// retained. This supports app-only kit preparation without an admin broker.
func observeSettings(ctx context.Context, origin, caPath, tokenPath, dir, generatedPath string) error {
	ca, e := os.ReadFile(caPath)
	if e != nil {
		return e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return os.ErrInvalid
	}
	f, e := os.OpenFile(tokenPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 16384 {
		return os.ErrInvalid
	}
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(b) > 16384 {
		return os.ErrInvalid
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return os.ErrInvalid
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	c, e := sc.New(origin, &http.Client{Transport: transport})
	if e != nil {
		return e
	}
	return recoverymaterial.Locked(dir, func() error {
		return bs.WithLive(dir, func(v *bs.Live, save func() error) error {
			o, e := c.Observe(ctx, token, time.Now())
			if e != nil {
				return e
			}
			if !o.Available || o.Export == nil || o.PendingRequestID != nil {
				return os.ErrInvalid
			}
			g, e := bs.ReadGenerated(generatedPath)
			if e != nil {
				return e
			}
			v.Observation = &o
			v.Destination = g.Destination
			return save()
		})
	})
}
