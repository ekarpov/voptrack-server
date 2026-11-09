// Package homesetup implements host-authorized, first-use Home provisioning.
// It is separate from Console and has no remote administration interface.
package homesetup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrRefused = errors.New("setup unavailable; preserve targets and inspect host authorization")

const SettingsFile = "/var/lib/voptrack-bootstrap/home-setup.json"
const PublicFile = "/var/lib/voptrack-public/home-setup.json"
const PendingFile = "/var/lib/voptrack-public/home-setup-pending"

var roots = []string{"tessera", "voptrack-config", "voptrack-bootstrap", "voptrack-console", "voptrack-public", "voptrack-ca", "voptrack-backups"}

type Choices struct {
	Version              int
	Name, TimeZone, Mode string
	Encrypted            bool
}

func (c Choices) Valid() bool {
	if !utf8.ValidString(c.Name) || c.Version != 1 || !c.Encrypted || strings.TrimSpace(c.Name) != c.Name || len(c.Name) < 1 || len(c.Name) > 160 || strings.ContainsAny(c.Name, "\"\\") {
		return false
	}
	for _, r := range c.Name {
		if unicode.IsControl(r) {
			return false
		}
	}
	if c.Mode != "public" && c.Mode != "private" && c.Mode != "off" {
		return false
	}
	if len(c.TimeZone) > 80 || c.TimeZone == "Local" || c.TimeZone == "" || strings.Contains(c.TimeZone, "..") || strings.HasPrefix(c.TimeZone, "/") {
		return false
	}
	for _, r := range c.TimeZone {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_+/-", r) {
			return false
		}
	}
	_, e := time.LoadLocation(c.TimeZone)
	return e == nil
}
func (c Choices) Environment() []string {
	console, private := "on", "no"
	if c.Mode == "off" {
		console = "off"
	}
	if c.Mode == "private" {
		private = "yes"
	}
	return []string{"VOPTRACK_VARIANT=household", "VOPTRACK_STORAGE_ENCRYPTED=yes", "VOPTRACK_NAME=" + c.Name, "VOPTRACK_TIMEZONE=" + c.TimeZone, "VOPTRACK_CONSOLE=" + console, "VOPTRACK_CONSOLE_PRIVATE=" + private}
}
func ReadChoices(path string) (c Choices, e error) {
	e = bc.ReadPrivateJSON(path, &c)
	if e != nil || !c.Valid() {
		return c, ErrRefused
	}
	return
}
func Apply(path string) error {
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return nil
	}
	c, e := ReadChoices(path)
	if e != nil {
		return e
	}
	for _, v := range c.Environment() {
		k, value, _ := strings.Cut(v, "=")
		if os.Setenv(k, value) != nil {
			return ErrRefused
		}
	}
	return nil
}

// LaunchAllowed also rejects symlink/damaged markers. Missing is the only
// state that permits normal service startup.
func LaunchAllowed(path string) error {
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return nil
	}
	return ErrRefused
}
func origin(s string, loopback bool) bool {
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if loopback {
		ip := net.ParseIP(u.Hostname())
		return u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	}
	return true
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func writeJSON(path string, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return recoverymaterial.Write(path, raw)
}
func at(root, relative string) string { return filepath.Join(root, strings.TrimPrefix(relative, "/")) }
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

func DecodeChoices(raw []byte) (c Choices, e error) {
	if len(raw) > 4096 {
		return c, ErrRefused
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&c) != nil || d.Decode(&extra) != io.EOF || !c.Valid() {
		return c, ErrRefused
	}
	return c, nil
}
