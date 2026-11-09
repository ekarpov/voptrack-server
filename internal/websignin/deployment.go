package websignin

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

const DefaultClientID = "voptrack-website"
const DefaultDataGrants = "read write insert"
const PublicFile = "/var/lib/voptrack-public/website-sign-in.json"

var errDeployment = errors.New("invalid generated website sign-in configuration")

// Deployment enables only the owned website on fresh installations. Reruns
// retain previous policy (including absence) unless the operator changes it.
func Deployment(mode, console, id, origin, grants, administer string, previous []byte) ([]byte, error) {
	switch mode {
	case "", "auto", "on", "off":
	default:
		return nil, errDeployment
	}
	if mode == "off" || console == "off" {
		return nil, nil
	}
	if id != "" || grants != "" || administer != "" {
		return Fragment(id, origin, grants, administer)
	}
	if mode == "on" || previous == nil {
		return Fragment(DefaultClientID, origin, DefaultDataGrants, "yes")
	}
	policies, raw, err := serverPolicies(previous)
	if err != nil {
		return nil, err
	}
	if len(policies) == 0 {
		return nil, nil
	}
	matches := 0
	for _, p := range policies {
		if p.Origin == origin {
			matches++
		}
	}
	if matches != 1 {
		return nil, errDeployment
	}
	// Preserve additive operator fields and the exact existing authority ceiling.
	return append(append([]byte(`"first_party_consoles":`), raw...), ','), nil
}

func serverPolicies(server []byte) ([]Policy, json.RawMessage, error) {
	var config struct {
		Policies json.RawMessage `json:"first_party_consoles"`
	}
	if len(bytes.TrimSpace(server)) == 0 || bytes.TrimSpace(server)[0] != '{' || json.Unmarshal(server, &config) != nil {
		return nil, nil, errDeployment
	}
	if len(config.Policies) == 0 {
		return nil, nil, nil
	}
	var policies []Policy
	if json.Unmarshal(config.Policies, &policies) != nil {
		return nil, nil, errDeployment
	}
	ids := map[string]bool{}
	for _, p := range policies {
		admin := "no"
		if p.Administer {
			admin = "yes"
		}
		if _, err := Fragment(p.ClientID, p.Origin, strings.Join(p.DataGrants, " "), admin); err != nil || p.ClientID == "" || p.RedirectURI != p.Origin+"/auth/callback" || ids[p.ClientID] {
			return nil, nil, errDeployment
		}
		ids[p.ClientID] = true
	}
	return policies, config.Policies, nil
}

type publicSettings struct {
	Version  int    `json:"version"`
	Origin   string `json:"origin"`
	ClientID string `json:"client_id"`
}

// Publish exposes an identity only, never credentials or grants.
func Publish(path, origin string, server []byte) error {
	if _, err := Fragment(DefaultClientID, origin, "", "no"); err != nil {
		return errDeployment
	}
	policies, _, err := serverPolicies(server)
	if err != nil {
		return err
	}
	settings := publicSettings{Version: 1, Origin: origin}
	for _, p := range policies {
		if p.Origin == origin {
			if settings.ClientID != "" {
				return errDeployment
			}
			settings.ClientID = p.ClientID
		}
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path+".new", raw, 0644); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

// ClientID honors a disabled receipt too. Environment fallback is for legacy
// deployments without a generated receipt; malformed receipts fail closed.
func ClientID(path, origin, fallback string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	if len(raw) > 4096 {
		return "", errDeployment
	}
	var s publicSettings
	if json.Unmarshal(raw, &s) != nil || s.Version != 1 || s.Origin != origin {
		return "", errDeployment
	}
	if s.ClientID != "" {
		if _, err := Fragment(s.ClientID, s.Origin, "", "no"); err != nil {
			return "", errDeployment
		}
	}
	return s.ClientID, nil
}
