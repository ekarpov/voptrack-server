// Package websignin maps owned-website defaults and operator policy to Tessera's public
// first_party_consoles interface. Branding and public registration cannot opt in.
package websignin

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

type Policy struct {
	ClientID    string   `json:"client_id"`
	Name        string   `json:"name"`
	Origin      string   `json:"origin"`
	RedirectURI string   `json:"redirect_uri"`
	DataGrants  []string `json:"data_grants"`
	Administer  bool     `json:"administer"`
}

// Fragment is empty unless the operator explicitly selects a dedicated ID and
// authority ceiling. No schema, erase or dataset-creation grant is conferred.
func Fragment(id, origin, grants, administer string) ([]byte, error) {
	if id == "" {
		if grants != "" || administer != "" {
			return nil, errors.New("website policy requires a dedicated identity")
		}
		return nil, nil
	}
	if len(id) > 200 || strings.ContainsAny(id, " \t\r\n") || strings.TrimSpace(id) == "" {
		return nil, errors.New("invalid website identity")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, errors.New("invalid website origin")
	}
	if administer != "yes" && administer != "no" {
		return nil, errors.New("explicit website administration policy required")
	}
	policy := Policy{ClientID: id, Name: "Voptrack", Origin: origin, RedirectURI: origin + "/auth/callback", Administer: administer == "yes", DataGrants: []string{}}
	seen := map[string]bool{}
	for _, g := range strings.Fields(grants) {
		if (g != "read" && g != "write" && g != "insert") || seen[g] {
			return nil, errors.New("invalid website data policy")
		}
		seen[g] = true
		policy.DataGrants = append(policy.DataGrants, g)
	}
	raw, err := json.Marshal([]Policy{policy})
	if err != nil {
		return nil, err
	}
	return append(append([]byte(`"first_party_consoles":`), raw...), ','), nil
}
