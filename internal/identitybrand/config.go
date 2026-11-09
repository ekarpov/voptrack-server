// Package identitybrand serializes Voptrack's opt-in operator configuration
// through Tessera's documented identity_brand JSON interface.
package identitybrand

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Input = "/var/lib/voptrack-config/identity-brand.json"
const Logo = "/usr/local/share/voptrack/identity-logo.png"
const MaxInput = 16 << 10

type Palette struct {
	Accent  string `json:"accent,omitempty"`
	Surface string `json:"surface,omitempty"`
	Text    string `json:"text,omitempty"`
}
type Settings struct {
	Name  string  `json:"name"`
	Light Palette `json:"light"`
	Dark  Palette `json:"dark"`
}

var ErrInvalid = errors.New("invalid operator identity branding")

// Native adds only the immutable, packaged Voptrack mark. Font overrides,
// web-client allowlists and first-party exceptions are deliberately not exposed.
func Native(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > MaxInput || !utf8.Valid(raw) {
		return nil, ErrInvalid
	}
	var s Settings
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 200 {
		return nil, ErrInvalid
	}
	for _, r := range s.Name {
		if unicode.IsControl(r) {
			return nil, ErrInvalid
		}
	}
	// Tessera remains authoritative for PNG validation and palette contrast.
	return json.Marshal(struct {
		Name  string  `json:"name"`
		Logo  string  `json:"logo_file"`
		Light Palette `json:"light"`
		Dark  Palette `json:"dark"`
	}{s.Name, Logo, s.Light, s.Dark})
}

func Read(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxInput {
		return nil, ErrInvalid
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return nil, ErrInvalid
	}
	raw, e := io.ReadAll(io.LimitReader(f, MaxInput+1))
	if e != nil {
		return nil, e
	}
	if len(raw) > MaxInput {
		return nil, ErrInvalid
	}
	return raw, nil
}

func Fragment(path string) ([]byte, error) {
	raw, e := Read(filepath.Clean(path))
	if e != nil {
		return nil, e
	}
	brand, e := Native(raw)
	if e != nil || len(brand) == 0 {
		return nil, e
	}
	wrapped, e := json.Marshal(map[string]json.RawMessage{"identity_brand": brand})
	if e != nil {
		return nil, e
	}
	return append(wrapped[1:len(wrapped)-1], ','), nil
}

// Matches binds the archived operator input to the generated server config.
// Unmanaged branding is refused rather than silently lost at regeneration.
func Matches(server, input []byte) bool {
	var config struct {
		Brand json.RawMessage `json:"identity_brand"`
	}
	if json.Unmarshal(server, &config) != nil {
		return false
	}
	expected, e := Native(input)
	if e != nil {
		return false
	}
	var a, b any
	if len(expected) > 0 && json.Unmarshal(expected, &a) != nil {
		return false
	}
	if len(config.Brand) > 0 && json.Unmarshal(config.Brand, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
