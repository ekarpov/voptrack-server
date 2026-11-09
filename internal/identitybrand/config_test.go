package identitybrand

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = `{"name":"Voptrack Demo","light":{"accent":"#0C6A61","surface":"#F2F2F7","text":"#111418"},"dark":{"accent":"#3FB8A9","surface":"#000000","text":"#F2F2F7"}}`

func TestOperatorInputCannotEnableClientPrivileges(t *testing.T) {
	native, err := Native([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(native, &got) != nil || got["logo_file"] != Logo || len(got) != 4 {
		t.Fatal("unexpected native configuration")
	}
	for _, raw := range []string{`{}`, `{"name":" "}`, `{"name":"x","web_client_ids":["client"]}`, `{"name":"x","logo_file":"/tmp/other.png"}`, `{"name":"x"} {}`, `{"name":"x\n"}`, `{"name":"` + strings.Repeat("x", 201) + `"}`} {
		if _, err := Native([]byte(raw)); err == nil {
			t.Fatal("invalid or privileged input accepted")
		}
	}
	if !Matches(append(append([]byte(`{"identity_brand":`), native...), '}'), []byte(valid)) {
		t.Fatal("native config mismatch")
	}
	if Matches([]byte(`{}`), []byte(valid)) || Matches(append(append([]byte(`{"identity_brand":`), native...), '}'), nil) {
		t.Fatal("branding silently lost")
	}
	if !Matches([]byte(`{}`), nil) {
		t.Fatal("legacy config refused")
	}
}
func TestPrivateRegularInputAndJSONFragment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "brand.json")
	if raw, e := Read(p); e != nil || raw != nil {
		t.Fatal("optional file no longer optional")
	}
	if e := os.WriteFile(p, []byte(valid), 0600); e != nil {
		t.Fatal(e)
	}
	fragment, e := Fragment(p)
	if e != nil {
		t.Fatal(e)
	}
	var out any
	if json.Unmarshal(append(append([]byte("{"), fragment...), []byte(`"engine":"sqlite"}`)...), &out) != nil {
		t.Fatal("invalid generated JSON")
	}
	if e = os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(p); e == nil {
		t.Fatal("public input accepted")
	}
	os.Chmod(p, 0600)
	link := p + "-link"
	if e = os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(link); e == nil {
		t.Fatal("symlink accepted")
	}
	if e = os.WriteFile(p, make([]byte, MaxInput+1), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(p); e == nil {
		t.Fatal("oversize file accepted")
	}
}
