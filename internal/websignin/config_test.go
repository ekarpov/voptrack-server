package websignin

import (
	"encoding/json"
	"testing"
)

func TestExplicitWebsitePolicy(t *testing.T) {
	raw, e := Fragment("", "", "", "")
	if e != nil || len(raw) != 0 {
		t.Fatal("empty explicit policy must stay empty")
	}
	raw, e = Fragment("operator-website", "https://home.invalid:9443", "read write insert", "yes")
	if e != nil {
		t.Fatal(e)
	}
	var d struct {
		Consoles []Policy `json:"first_party_consoles"`
	}
	if json.Unmarshal(append(append([]byte{'{'}, raw[:len(raw)-1]...), '}'), &d) != nil || len(d.Consoles) != 1 || !d.Consoles[0].Administer || d.Consoles[0].RedirectURI != "https://home.invalid:9443/auth/callback" {
		t.Fatal("wrong exact policy")
	}
	for _, tc := range [][4]string{{"", "https://home.invalid", "read", "yes"}, {"operator", "https://home.invalid/", "read", "yes"}, {"operator", "http://home.invalid", "read", "yes"}, {"operator", "https://home.invalid?", "read", "yes"}, {"operator", "https://home.invalid", "read erase", "yes"}, {"operator", "https://home.invalid", "read read", "yes"}, {"operator", "https://home.invalid", "read", ""}, {"bad id", "https://home.invalid", "read", "yes"}} {
		if _, e := Fragment(tc[0], tc[1], tc[2], tc[3]); e == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
