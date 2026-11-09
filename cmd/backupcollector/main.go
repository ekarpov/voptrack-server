// Command backupcollector owns only a retrieval credential. Provision/revoke
// are explicit administrator operations; an administrator token is never saved.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type credential struct {
	ID         string    `json:"id"`
	Token      string    `json:"token"`
	Deployment string    `json:"deployment"`
	Origin     string    `json:"origin"`
	Expires    time.Time `json:"expires_at"`
}

var errSafe = errors.New("collector operation failed; inspect authorization, trusted deployment and private storage")

func secret(path string) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errSafe
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 16384 {
		return nil, errSafe
	}
	b := make([]byte, st.Size())
	_, e = f.ReadAt(b, 0)
	if e != nil {
		return nil, errSafe
	}
	return b, nil
}
func run() error {
	mode := flag.String("mode", "collect", "collect, queue, provision, revoke or retain-lineage")
	origin := flag.String("origin", "https://tesserad:8443", "trusted HTTPS origin")
	ca := flag.String("ca", "/var/lib/voptrack-public/server.crt", "CA file")
	file := flag.String("credential", "/var/lib/voptrack-collector/credential.json", "private collector credential")
	adminFile := flag.String("administrator-file", "", "ephemeral approved administrator token file; never saved")
	id := flag.String("id", "", "caller-selected credential ID, required for provision/revoke")
	deployment := flag.String("deployment", "", "independently trusted deployment identity; required for provisioning")
	seconds := flag.Int("lifetime", 2592000, "credential lifetime in seconds")
	dir := flag.String("bundles", "/var/lib/voptrack-bundles", "private published bundles")
	keep := flag.Int("keep", 7, "local retained results per kind, 1..32")
	outbox := flag.String("outbox", "", "host-provisioned private durable replication outbox; optional")
	outboxBudget := flag.Int64("outbox-budget", 5<<30, "maximum pending outbox bytes")
	once := flag.Bool("once", false, "one collection scan")
	flag.Parse()
	if *mode == "queue" {
		if *outbox == "" {
			return errSafe
		}
		q := bc.Outbox{Dir: *outbox, Budget: *outboxBudget}
		store := bc.Store{Dir: *dir}
		lock, e := store.Writer()
		if e != nil {
			return errSafe
		}
		defer lock.Close()
		all, e := store.List()
		if e != nil {
			return errSafe
		}
		for _, bundle := range all {
			f, e := store.Pin(bundle.Operation.ID)
			if e != nil {
				return errSafe
			}
			f.Close()
			if q.Capture(filepath.Join(store.Dir, bundle.Operation.ID), bundle.Manifest) != nil || q.LocalPublished(bundle.Operation.ID) != nil {
				return errSafe
			}
		}
		return nil
	}
	pem, e := os.ReadFile(*ca)
	if e != nil {
		return errSafe
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return errSafe
	}
	client, e := ac.New(*origin, &http.Client{Timeout: 115 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}})
	if e != nil {
		return errSafe
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if *mode == "retain-lineage" {
		raw, e := secret(*file)
		var v credential
		if e != nil || json.Unmarshal(raw, &v) != nil || v.Deployment == "" || *deployment != v.Deployment || v.Origin != *origin {
			return errSafe
		}
		return retainLineage(filepath.Dir(*file), v.Deployment, v.Origin)
	}
	if *mode == "provision" || *mode == "revoke" {
		raw, e := secret(*adminFile)
		if e != nil {
			return errSafe
		}
		admin := strings.TrimSpace(string(raw))
		request, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		if *mode == "revoke" {
			if client.Revoke(request, *id, admin) != nil {
				return errSafe
			}
			return nil
		}
		if *deployment == "" {
			return errSafe
		}
		// O_EXCL reserves output before network issuance. On any error the caller
		// must revoke this same ID, including uncertain issuance or disk failure.
		if os.MkdirAll(filepath.Dir(*file), 0700) != nil {
			return errSafe
		}
		f, e := os.OpenFile(*file, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return errSafe
		}
		defer f.Close()
		issued, e := client.Provision(request, *id, admin, *seconds)
		if e != nil {
			return errSafe
		}
		v := credential{issued.ID, issued.Token, *deployment, *origin, issued.Expires}
		if json.NewEncoder(f).Encode(v) != nil || f.Sync() != nil {
			return errSafe
		}
		folder, e := os.Open(filepath.Dir(*file))
		if e != nil {
			return errSafe
		}
		defer folder.Close()
		if folder.Sync() != nil {
			return errSafe
		}
		return retainLineage(filepath.Dir(*file), *deployment, *origin)
	}
	if *mode != "collect" || *keep < 1 || *keep > 32 {
		return errSafe
	}
	for {
		raw, e := secret(*file)
		var v credential
		if e == nil && (json.Unmarshal(raw, &v) != nil || v.Token == "" || v.Deployment == "" || v.Origin != *origin || !time.Now().Before(v.Expires)) {
			e = errSafe
		}
		if e == nil {
			_ = (bc.Store{Dir: *dir}).WriteStatus("preparing")
			c := bc.Collector{Client: client, Store: bc.Store{Dir: *dir}, Token: v.Token, Deployment: v.Deployment, Origin: *origin, Keep: *keep}
			if *outbox != "" {
				c.Outbox = &bc.Outbox{Dir: *outbox, Budget: *outboxBudget}
			}
			e = c.Scan(ctx)
		}
		if e == nil {
			_ = (bc.Store{Dir: *dir}).WriteStatus("idle")
		} else {
			_ = (bc.Store{Dir: *dir}).WriteStatus("unavailable")
		}
		if *once {
			if e != nil {
				return errSafe
			}
			return nil
		}
		if e != nil {
			code := "unavailable"
			var refusal *ac.Refusal
			if errors.As(e, &refusal) {
				code = refusal.Code
			} else if errors.Is(e, bc.ErrProvenance) {
				code = "provenance"
			} else if errors.Is(e, ac.ErrInvalid) {
				code = "invalid_descriptor"
			} else if errors.Is(e, ac.ErrIntegrity) {
				code = "integrity"
			} else if errors.Is(e, bc.ErrStorage) {
				code = "local_storage"
			}
			fmt.Fprintln(os.Stderr, "collector: preparation unavailable ("+code+"); no success or custody claimed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(15 * time.Second):
		}
	}
}
func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, errSafe)
		os.Exit(1)
	}
}

func retainLineage(dir, deployment, origin string) error {
	return recoverymaterial.Locked(dir, func() error {
		lineagePath := filepath.Join(dir, "lineage.json")
		lineage, _ := json.Marshal(struct {
			Deployment string `json:"deployment"`
			Origin     string `json:"origin"`
		}{deployment, origin})
		if old, e := os.ReadFile(lineagePath); e == nil {
			if !bytes.Equal(old, lineage) {
				return errSafe
			}
			return nil
		} else if !os.IsNotExist(e) {
			return errSafe
		}
		return recoverymaterial.Write(lineagePath, lineage)
	})
}
