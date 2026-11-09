package recoverykit

import (
	"archive/tar"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/homesetup"
	"github.com/ekarpov/voptrack-server/internal/identitybrand"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Validate parses the bounded format entirely before writing fresh targets.
// Digests detect accidental mixing/corruption, not a malicious trusted host.
func Validate(raw []byte) (map[string][]byte, error) {
	if len(raw) > MaxBytes {
		return nil, ErrUnavailable
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	files := map[string][]byte{}
	total := 0
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil || h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > MaxBytes {
			return nil, ErrUnavailable
		}
		n := strings.TrimPrefix(h.Name, "var/lib/")
		if h.Name != "var/lib/"+n || !allowed(n) || files[n] != nil {
			return nil, ErrUnavailable
		}
		b, e := io.ReadAll(io.LimitReader(tr, MaxBytes+1))
		total += len(b)
		if e != nil || int64(len(b)) != h.Size || total > MaxBytes {
			return nil, ErrUnavailable
		}
		files[n] = b
	}
	var m manifest
	desc := files["voptrack-bootstrap/kit-manifest.json"]
	if json.Unmarshal(desc, &m) != nil || m.Format != "voptrack-recovery-kit-1" || !ValidID(m.Metadata.Revision) {
		return nil, ErrUnavailable
	}
	if len(m.Members) != len(files)-1 {
		return nil, ErrUnavailable
	}
	for n, h := range m.Members {
		if b, ok := files[n]; !ok || digest(b) != h {
			return nil, ErrUnavailable
		}
	}
	for _, n := range []string{"voptrack-public/voptrack.json", "voptrack-bootstrap/backup-lineage.json", "voptrack-config/backup.key", "voptrack-config/server.json", "voptrack-bootstrap/owner-password", "voptrack-bootstrap/variant", "voptrack-bootstrap/console", "voptrack-bootstrap/recovery-lineage", "voptrack-ca/root.crt", "voptrack-ca/root.key", "voptrack-config/server.crt", "voptrack-config/server.key"} {
		if len(files[n]) == 0 {
			return nil, ErrUnavailable
		}
	}
	if !validConfiguration(files["voptrack-config/server.json"]) || !identitybrand.Matches(files["voptrack-config/server.json"], files["voptrack-config/identity-brand.json"]) {
		return nil, ErrUnavailable
	}
	if !validLineage(files["voptrack-bootstrap/backup-lineage.json"]) {
		return nil, ErrUnavailable
	}
	if raw, ok := files["voptrack-bootstrap/backup-settings-export.json"]; ok {
		var export sc.Export
		var lineage struct {
			Deployment string `json:"deployment"`
		}
		if json.Unmarshal(raw, &export) != nil || !export.Valid() || export.Revision != m.Metadata.SettingsRevision || json.Unmarshal(files["voptrack-bootstrap/backup-lineage.json"], &lineage) != nil || export.Deployment != lineage.Deployment {
			return nil, ErrUnavailable
		}
	} else if m.Metadata.SettingsRevision != "" {
		return nil, ErrUnavailable
	}
	k := files["voptrack-config/backup.key"]
	lineage := files["voptrack-bootstrap/recovery-lineage"]
	variant := files["voptrack-bootstrap/variant"]
	configuration := digest(append(append(bytes.Clone(files["voptrack-config/server.json"]), 0), files["voptrack-public/voptrack.json"]...))
	if raw, ok := files["voptrack-bootstrap/home-setup.json"]; ok {
		configuration = digest(append(append(append([]byte(configuration), 0), raw...), files["voptrack-bootstrap/home-password-retired"]...))
	}
	if len(k) != 32 || !ValidID(strings.TrimSpace(string(lineage))) || digest(append(append(append(append(append(bytes.Clone(lineage), k...), variant...), files["voptrack-bootstrap/backup-lineage.json"]...), files["voptrack-ca/root.crt"]...), files["voptrack-config/server.crt"]...)) != m.Metadata.Compatibility || configuration != m.Metadata.Configuration {
		return nil, ErrUnavailable
	}
	if _, e := tls.X509KeyPair(files["voptrack-ca/root.crt"], files["voptrack-ca/root.key"]); e != nil {
		return nil, ErrUnavailable
	}
	if _, e := tls.X509KeyPair(files["voptrack-config/server.crt"], files["voptrack-config/server.key"]); e != nil {
		return nil, ErrUnavailable
	}
	// Required product-specific service files must exist even with a rewritten
	// manifest. A kit is not proof of pairing with any particular data backup.
	on := strings.TrimSpace(string(files["voptrack-bootstrap/console"]))
	v := strings.TrimSpace(string(variant))
	if (on != "on" && on != "off") || (v != "household" && v != "research" && v != "standard") || (v == "research" && on == "off") {
		return nil, ErrUnavailable
	}
	var product struct {
		Product  string `json:"product"`
		Timezone string `json:"timezone"`
		Web      bool   `json:"web_interface"`
	}
	expected := map[string]string{"standard": "personal", "household": "home", "research": "research"}[v]
	if json.Unmarshal(files["voptrack-public/voptrack.json"], &product) != nil || product.Product != expected || product.Timezone == "" || product.Web != (on == "on") {
		return nil, ErrUnavailable
	}
	if _, e := time.LoadLocation(product.Timezone); e != nil {
		return nil, ErrUnavailable
	}
	required := []string{}
	if v == "household" {
		if on == "off" {
			required = append(required, "voptrack-bootstrap/issuer-home-members.secret")
		} else {
			required = append(required, "voptrack-console/home.secret", "voptrack-console/issuer-home-members.secret")
		}
	}
	if on == "on" && (v == "household" || v == "research") {
		required = append(required, "voptrack-console/console-state.secret", "voptrack-console/console-state.dataset")
	}
	if v == "research" {
		required = append(required, "voptrack-console/analysis.secret")
	}
	for _, n := range required {
		if len(files[n]) == 0 {
			return nil, ErrUnavailable
		}
	}

	if raw, ok := files["voptrack-bootstrap/home-setup.json"]; ok {
		choices, err := homesetup.DecodeChoices(raw)
		if err != nil || string(files["voptrack-bootstrap/home-password-retired"]) != "initial password retired\n" || strings.TrimSpace(string(files["voptrack-bootstrap/variant"])) != "household" {
			return nil, ErrUnavailable
		}
		var product struct {
			TimeZone string `json:"timezone"`
			Web      bool   `json:"web_interface"`
		}
		var config struct {
			Name string `json:"display_name"`
		}
		if json.Unmarshal(files["voptrack-public/voptrack.json"], &product) != nil || json.Unmarshal(files["voptrack-config/server.json"], &config) != nil || product.TimeZone != choices.TimeZone || product.Web != (choices.Mode != "off") || config.Name != choices.Name {
			return nil, ErrUnavailable
		}
		expected := "on"
		if choices.Mode == "off" {
			expected = "off"
		}
		if strings.TrimSpace(string(files["voptrack-bootstrap/console"])) != expected {
			return nil, ErrUnavailable
		}
	} else if _, ok := files["voptrack-bootstrap/home-password-retired"]; ok {
		return nil, ErrUnavailable
	}
	return files, nil
}
func allowed(n string) bool {
	if filepath.Clean(n) != n || strings.Contains(n, "\\") || strings.HasPrefix(n, "/") {
		return false
	}
	if strings.HasPrefix(n, "voptrack-console/.recovery-history/") {
		parts := strings.SplitN(strings.TrimPrefix(n, "voptrack-console/.recovery-history/"), "/", 2)
		if len(parts) != 2 || !ValidID(parts[0]) || strings.HasPrefix(parts[1], ".recovery-history/") {
			return false
		}
		return allowed("voptrack-console/" + parts[1])
	}
	for _, v := range []string{"voptrack-config/identity-brand.json", "voptrack-bootstrap/home-setup.json", "voptrack-bootstrap/home-password-retired", "voptrack-bootstrap/backup-settings-export.json", "voptrack-public/voptrack.json", "voptrack-bootstrap/backup-lineage.json", "voptrack-config/backup.key", "voptrack-config/server.json", "voptrack-config/server.crt", "voptrack-config/server.key", "voptrack-ca/root.crt", "voptrack-ca/root.key", "voptrack-bootstrap/owner-password", "voptrack-bootstrap/variant", "voptrack-bootstrap/console", "voptrack-bootstrap/recovery-lineage", "voptrack-bootstrap/issuer-home-members.secret", "voptrack-bootstrap/kit-manifest.json", "voptrack-console/home.secret", "voptrack-console/analysis.secret", "voptrack-console/issuer-home-members.secret", "voptrack-console/console-state.secret", "voptrack-console/console-state.dataset"} {
		if n == v {
			return true
		}
	}
	if strings.HasPrefix(n, "voptrack-console/oauth-client-") && !strings.Contains(strings.TrimPrefix(n, "voptrack-console/"), "/") {
		return true
	}
	if strings.HasPrefix(n, "voptrack-console/studies/") {
		base := strings.TrimPrefix(n, "voptrack-console/studies/")
		return !strings.Contains(base, "/") && strings.HasSuffix(base, ".issuer-secret")
	}
	return false
}
func Restore(root string, raw []byte) error {
	files, e := Validate(raw)
	if e != nil {
		return e
	}
	for _, dir := range []string{"voptrack-config", "voptrack-console", "voptrack-bootstrap", "voptrack-ca"} {
		path := filepath.Join(root, "var/lib", dir)
		if st, e := os.Lstat(path); e == nil && !st.IsDir() {
			return ErrUnavailable
		}
		entries, e := os.ReadDir(path)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if len(entries) != 0 {
			return ErrUnavailable
		}
	}
	public := filepath.Join(root, "var/lib/voptrack-public")
	if st, e := os.Lstat(public); e == nil && !st.IsDir() {
		return ErrUnavailable
	}
	entries, e := os.ReadDir(public)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	for _, entry := range entries {
		if entry.Name() != "maintenance.lock" {
			return ErrUnavailable
		}
	}
	for n, b := range files {
		path := filepath.Join(root, "var/lib", n)
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		if e = recoverymaterial.Write(path, b); e != nil {
			return e
		}
	}
	return nil
}

func validLineage(raw []byte) bool {
	var v struct{ Deployment, Origin string }
	if json.Unmarshal(raw, &v) != nil || len(v.Deployment) < 16 || len(v.Deployment) > 128 {
		return false
	}
	for _, c := range v.Deployment {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	u, e := url.Parse(v.Origin)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func validConfiguration(raw []byte) bool {
	var c struct {
		Engine    string `json:"engine"`
		Data      string `json:"data_dir"`
		Key       string `json:"tls_key"`
		Cert      string `json:"tls_certificate"`
		Encrypted bool   `json:"encrypted_storage_confirmed"`
		Backup    struct {
			Directory string `json:"directory"`
			Key       string `json:"key_file"`
			Hours     int    `json:"every_hours"`
			Keep      int    `json:"keep"`
		} `json:"backup"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Engine != "sqlite" || c.Data != "/var/lib/tessera" || c.Key != "/var/lib/voptrack-config/server.key" || c.Cert != "/var/lib/voptrack-config/server.crt" || !c.Encrypted || c.Backup.Key != "/var/lib/voptrack-config/backup.key" {
		return false
	}
	v := backupsettings.Values{Destination: "local", EveryHours: c.Backup.Hours, Keep: c.Backup.Keep}
	if c.Backup.Directory == "/var/lib/voptrack-backups/reserve" {
		v.Destination = "reserve"
	}
	return v.Valid() && v.Directory() == c.Backup.Directory
}
