package websignin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func asServer(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return append(append([]byte("{"), raw[:len(raw)-1]...), '}')
}
func firstPolicy(t *testing.T, raw []byte) Policy {
	t.Helper()
	p, _, err := serverPolicies(asServer(raw))
	if err != nil || len(p) != 1 {
		t.Fatal("expected one exact website policy")
	}
	return p[0]
}
func TestFreshOwnedWebsiteDefaultsAndExistingChoices(t *testing.T) {
	for _, origin := range []string{"https://home.invalid", "https://research.invalid", "https://personal.invalid:9443"} {
		raw, err := Deployment("auto", "on", "", origin, "", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		p := firstPolicy(t, raw)
		if p.ClientID != DefaultClientID || p.Origin != origin || p.RedirectURI != origin+"/auth/callback" || !p.Administer || strings.Join(p.DataGrants, " ") != "read write insert" {
			t.Fatal("fresh owned-website authority or origin changed")
		}
		preserved, err := Deployment("auto", "on", "", origin, "", "", asServer(raw))
		if err != nil || string(raw) != string(preserved) {
			t.Fatal("rerun changed the generated policy")
		}
	}
	origin := "https://home.invalid"
	for _, mode := range []string{"", "auto", "off"} {
		raw, err := Deployment(mode, "on", "", origin, "", "", []byte("{}"))
		if err != nil || len(raw) != 0 {
			t.Fatal("legacy deliberate absence became trusted")
		}
	}
	raw, err := Deployment("on", "on", "", origin, "", "", []byte("{}"))
	if err != nil || len(raw) == 0 {
		t.Fatal("explicit opt-in failed")
	}
	for _, console := range []string{"on", "off"} {
		raw, err = Deployment("off", console, "operator", origin, "read", "yes", nil)
		if err != nil || len(raw) != 0 {
			t.Fatal("explicit opt-out did not disable policy")
		}
	}
	raw, err = Deployment("auto", "off", "", origin, "", "", nil)
	if err != nil || len(raw) != 0 {
		t.Fatal("app-only deployment got a website policy")
	}

	limited, _ := Fragment("chosen-website", origin, "read", "no")
	previous := strings.Replace(string(asServer(limited)), `"administer":false`, `"administer":false,"future_optional":"preserved"`, 1)
	raw, err = Deployment("auto", "on", "", origin, "", "", []byte(previous))
	if err != nil || !strings.Contains(string(raw), "future_optional") {
		t.Fatal("additive prior fields lost")
	}
	p := firstPolicy(t, raw)
	if p.ClientID != "chosen-website" || p.Administer || strings.Join(p.DataGrants, " ") != "read" {
		t.Fatal("existing ceiling expanded")
	}
	for _, old := range [][]byte{[]byte("null"), []byte("{"), asServer(limited)} {
		_, err = Deployment("auto", "on", "", "https://different.invalid", "", "", old)
		if err == nil {
			t.Fatal("malformed or foreign-origin prior policy accepted")
		}
	}
	for _, args := range [][6]string{
		{"invalid", "on", "", origin, "", ""}, {"auto", "on", "", "http://unsafe.invalid", "", ""},
		{"on", "on", "custom", origin, "read erase", "yes"}, {"on", "on", "", origin, "read", "yes"},
	} {
		if _, err = Deployment(args[0], args[1], args[2], args[3], args[4], args[5], nil); err == nil {
			t.Fatal("invalid deployment policy accepted")
		}
	}
}
func TestGeneratedWebsiteIdentityIsBoundAndExplicitlyDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "website-sign-in.json")
	origin := "https://home.invalid:9443"
	id, err := ClientID(path, origin, "legacy-identity")
	if err != nil || id != "legacy-identity" {
		t.Fatal("legacy absence fallback changed")
	}
	raw, _ := Deployment("auto", "on", "", origin, "", "", nil)
	if err = Publish(path, origin, asServer(raw)); err != nil {
		t.Fatal(err)
	}
	id, err = ClientID(path, origin, "wrong-environment")
	if err != nil || id != DefaultClientID {
		t.Fatal("Console identity disagrees with generated config")
	}
	data, _ := os.ReadFile(path)
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil || len(fields) != 3 || fields["version"] != float64(1) {
		t.Fatal("unexpected public receipt fields")
	}
	if _, err = ClientID(path, "https://wrong.invalid", "fallback"); err == nil {
		t.Fatal("wrong origin accepted")
	}
	if err = Publish(path, origin, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	id, err = ClientID(path, origin, "still-configured-in-env")
	if err != nil || id != "" {
		t.Fatal("disabled receipt fell back to broader environment")
	}
	for _, data := range []string{`{"version":9,"origin":"https://home.invalid:9443","client_id":"x"}`, "{", strings.Repeat("x", 4097)} {
		if err = os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err = ClientID(path, origin, "fallback"); err == nil {
			t.Fatal("malformed receipt fell back to environment")
		}
	}
}
