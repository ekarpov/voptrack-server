// Package catalogue reads the immutable app export. It never installs types.
package catalogue

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

const Version = 2
const ManifestSHA256 = "fff42e31ac1a258782e85f40475909cb5ad3276d64cc571ab0c84690b0a7ccbb"

//go:embed 2/manifest.json 2/types/*.json 2/vectors/*.json
var files embed.FS

type Text struct {
	Bundled struct {
		Key string `json:"key"`
	} `json:"bundled"`
}
type Field struct {
	ID, Type string
	Internal bool
	Units    *struct{ UnitField, CanonicalUnit, DefaultUnit string }
}
type Metric struct{ Operation map[string]json.RawMessage }
type Presentation struct {
	DisplayName        Text
	Reads, ReadsReason string
	ReadsAsLevel       bool
	Headline           struct {
		Metric      *Metric
		HomeCardSum *string
	}
	LibraryDeclaration struct {
		Category         struct{ ID string }
		InternalFieldIDs []string
	}
	Spending struct{ IsSpending bool }
}
type Document struct {
	CatalogueVersion int
	ID, Kind         string
	Fields           []Field
	Installation     struct{ Type json.RawMessage }
	Presentation     *Presentation
}
type Manifest struct {
	CatalogueVersion int
	SourceRevision   string
	Documents        []struct{ Path, SHA256, TypeID, Kind string }
	Vectors          []struct{ Path, SHA256, TypeID string }
	InternalTypes    []struct{ ID, Reason string }
}
type Catalogue struct {
	Documents      map[string]Document
	Internal       map[string]bool
	SourceRevision string
}

func Load() (*Catalogue, error) {
	raw, err := files.ReadFile("2/manifest.json")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != ManifestSHA256 {
		return nil, fmt.Errorf("catalogue manifest hash mismatch")
	}
	var m Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.CatalogueVersion != Version {
		return nil, fmt.Errorf("catalogue version mismatch")
	}
	c := &Catalogue{Documents: map[string]Document{}, Internal: map[string]bool{}, SourceRevision: m.SourceRevision}
	read := func(p, h string) ([]byte, error) {
		if path.Clean(p) != p || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			return nil, fmt.Errorf("invalid catalogue path")
		}
		raw, e := files.ReadFile("2/" + p)
		if e != nil {
			return nil, e
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != h {
			return nil, fmt.Errorf("catalogue file hash mismatch: %s", p)
		}
		return raw, nil
	}
	for _, item := range m.Documents {
		raw, e := read(item.Path, item.SHA256)
		if e != nil {
			return nil, e
		}
		var d Document
		if e = json.Unmarshal(raw, &d); e != nil {
			return nil, e
		}
		if d.ID != item.TypeID || d.Kind != item.Kind || d.CatalogueVersion != Version {
			return nil, fmt.Errorf("catalogue identity mismatch")
		}
		if _, exists := c.Documents[d.ID]; exists {
			return nil, fmt.Errorf("duplicate catalogue type")
		}
		c.Documents[d.ID] = d
	}
	for _, item := range m.Vectors {
		if _, e := read(item.Path, item.SHA256); e != nil {
			return nil, e
		}
	}
	for _, item := range m.InternalTypes {
		c.Internal[item.ID] = true
	}
	return c, nil
}
