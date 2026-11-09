package main

import (
	"regexp"
	"strings"
)

// TypeDef is a type as Tessera installs it: what a study's issuer template
// carries. Research tracks are designed in trackdef.go and derived into these.

type TypeField struct {
	ID          string            `json:"id"`
	MetaID      string            `json:"meta_id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Scale       *int              `json:"scale,omitempty"`
	Roles       []string          `json:"roles,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type TypeDef struct {
	ID       string      `json:"id"`
	ParentID *string     `json:"parent_id,omitempty"`
	Name     string      `json:"name"`
	Roles    []string    `json:"roles,omitempty"`
	Fields   []TypeField `json:"fields"`
}

var identifierPart = regexp.MustCompile(`[^a-z0-9]+`)

// identifier turns a person's name for something into a protocol identifier:
// lowercase letters, digits and underscores, starting with a letter.
func identifier(name string) string {
	id := strings.Trim(identifierPart.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if id == "" || (id[0] >= '0' && id[0] <= '9') {
		id = "t_" + id
	}
	return id
}
