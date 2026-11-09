package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	appcatalogue "github.com/ekarpov/voptrack-server/catalogue"
)

var appTracks = func() *appcatalogue.Catalogue {
	c, e := appcatalogue.Load()
	if e != nil {
		panic(e)
	}
	return c
}()

// A type's effective declaration contains inherited fields/roles. Compare the
// whole read shape, including opaque annotations, rather than a matching ID.
// Older app stores may omit only the optional root bookkeeping addedAt and
// writeToken fields. That is read compatibility, never installation permission.
func catalogueReadCompatible(t TypeInfo) bool {
	if t.SchemaConflict {
		return false
	}
	doc, ok := appTracks.Documents[t.ID]
	if !ok || doc.Kind != "track" || t.Schema.ID != t.ID {
		return false
	}
	expected, ok := catalogueEffective(t.ID)
	if !ok {
		return false
	}
	actual := t.Schema
	optional := map[string]bool{"addedAt": true, "writeToken": true}
	present := map[string]bool{}
	for _, f := range actual.Fields {
		present[f.ID] = true
	}
	kept := expected.Fields[:0]
	for _, f := range expected.Fields {
		if f.MetaID == "com.voptrack.track" && optional[f.ID] && !present[f.ID] {
			continue
		}
		kept = append(kept, f)
	}
	expected.Fields = kept
	return sameReadMeta(actual, expected)
}
func catalogueEffective(id string) (Meta, bool) {
	d, ok := appTracks.Documents[id]
	if !ok {
		return Meta{}, false
	}
	var m Meta
	if json.Unmarshal(d.Installation.Type, &m) != nil {
		return Meta{}, false
	}
	if m.ParentID != nil {
		parent, ok := catalogueEffective(*m.ParentID)
		if !ok {
			return Meta{}, false
		}
		m.Fields = append(parent.Fields, m.Fields...)
		m.Roles = append(parent.Roles, m.Roles...)
	}
	return m, true
}
func sameReadMeta(a, b Meta) bool {
	normalize := func(m Meta) Meta {
		m.Fields = append([]Field(nil), m.Fields...)
		m.Roles = append([]string(nil), m.Roles...)
		sort.Strings(m.Roles)
		sort.Slice(m.Fields, func(i, j int) bool { return m.Fields[i].ID < m.Fields[j].ID })
		for i := range m.Fields {
			m.Fields[i].Roles = append([]string(nil), m.Fields[i].Roles...)
			sort.Strings(m.Fields[i].Roles)
		}
		return m
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}

// Catalogue kind, rather than an ID prefix or inherited Track role, determines
// which app documents may appear as standalone household Tracks. Unknown custom
// types retain their existing behavior. Parts remain available as dependencies.
func catalogueNonTrack(c *appcatalogue.Catalogue, id string) bool {
	if c.Internal[id] {
		return true
	}
	doc, known := c.Documents[id]
	return known && doc.Kind != "track"
}

func catalogueInternalField(typ, id string) bool {
	if id == "writeToken" {
		_, ok := appTracks.Documents[typ]
		return ok
	}
	d, ok := appTracks.Documents[typ]
	if !ok {
		return false
	}
	for _, f := range d.Fields {
		if f.ID == id && f.Internal {
			return true
		}
	}
	if d.Presentation != nil {
		for _, f := range d.Presentation.LibraryDeclaration.InternalFieldIDs {
			if f == id {
				return true
			}
		}
	}
	return false
}
func catalogueMetric(t TypeInfo) (valueMetric, bool) {
	doc, known := appTracks.Documents[t.ID]
	if !known || doc.Kind != "track" {
		return valueMetric{}, false
	}
	m := valueMetric{Catalogue: true, Unsupported: true}
	if !catalogueReadCompatible(t) || doc.Presentation == nil {
		return m, true
	}
	p := doc.Presentation
	m.Level = p.ReadsAsLevel
	if p.Reads == "count" {
		m.Measure = MeasureCount
		m.Label = "Records"
		m.Unsupported = false
		return m, true
	}
	if p.Reads != "latest" && p.Reads != "sum" && p.Reads != "average" {
		return m, true
	}
	if p.Reads == "latest" && p.Headline.HomeCardSum != nil && *p.Headline.HomeCardSum == "meterUse" {
		for _, f := range doc.Fields {
			if f.Units == nil || (f.Type != "float" && f.Type != "integer" && f.Type != "fixed") {
				continue
			}
			for _, n := range t.Numeric {
				if n.ID == f.ID {
					m.Options = append(m.Options, valueField{Field: f.ID, UnitField: f.Units.UnitField, Label: localizeName(fieldName(n), translator("en"))})
				}
			}
		}
		if len(m.Options) == 0 {
			return m, true
		}
		m.Field, m.UnitField, m.Label = m.Options[0].Field, m.Options[0].UnitField, m.Options[0].Label
		m.Measure, m.Latest, m.Unsupported = MeasureLatest, true, false
		m.KindField, m.KindLabel = "meterID", "meterID"
		return m, true
	}
	if p.Headline.Metric == nil {
		return m, true
	}
	// Only operations with a declared numeric field are qualified here. Runtime,
	// duration/union, counter-delta and composite page readers keep raw history.
	var op struct {
		FieldID     string
		Aggregation string
		Kind        string `json:"_0"`
	}
	raw, money := p.Headline.Metric.Operation["money"]
	if !money {
		raw = p.Headline.Metric.Operation["numeric"]
	}
	if len(raw) == 0 || json.Unmarshal(raw, &op) != nil {
		return m, true
	}
	operation := op.Kind
	if money {
		operation = op.Aggregation
	}
	if operation != p.Reads {
		return m, true
	}
	for _, f := range t.Numeric {
		if f.ID == op.FieldID {
			m.Field = f.ID
			m.Label = localizeName(fieldName(f), translator("en"))
		}
	}
	if m.Field == "" {
		return m, true
	}
	m.Money = money
	m.Latest = p.Reads == "latest"
	switch p.Reads {
	case "latest":
		m.Measure = MeasureLatest
	case "sum":
		m.Measure = MeasureSum
	case "average":
		m.Measure = MeasureAverage
	}
	if money {
		m.UnitField = "currency"
	} else {
		for _, f := range doc.Fields {
			if f.ID == m.Field && f.Units != nil {
				m.UnitField = f.Units.UnitField
				if m.UnitField == "" {
					m.Unit = f.Units.CanonicalUnit
				}
			}
		}
	}
	if m.Latest {
		m.KindField = "subject"
		m.KindLabel = "$member"
	}
	m.Unsupported = false
	return m, true
}
func metricLine(m valueMetric) bool {
	if m.Catalogue {
		return m.Latest || m.Level
	}
	return true
}

func init() {
	categories := map[string]string{"finance": "money", "household": "house", "activity": "activity", "health": "health", "pets": "pets", "learning": "learning", "general": "general"}
	for id, d := range appTracks.Documents {
		if d.Kind != "track" || d.Presentation == nil {
			continue
		}
		p := d.Presentation
		old := homeTrackIdentities[id]
		old.Key = p.DisplayName.Bundled.Key
		if old.Name == "" {
			old.Name = localizeName(old.Key, translator("en"))
			if old.Name == old.Key {
				old.Name = strings.ReplaceAll(strings.TrimPrefix(id, "com.voptrack.track."), "_", " ")
			}
		}
		old.Category = categories[strings.TrimPrefix(p.LibraryDeclaration.Category.ID, "com.voptrack.category.")]
		if old.Category == "" {
			old.Category = "general"
		}
		homeTrackIdentities[id] = old
	}
}

func recordValueMetric(m valueMetric, rec readRecord) (valueMetric, bool) {
	if len(m.Options) == 0 {
		return m, true
	}
	var chosen *valueField
	for i := range m.Options {
		if rec.Values[m.Options[i].Field] != nil {
			if chosen != nil {
				return m, false
			}
			chosen = &m.Options[i]
		}
	}
	if chosen == nil {
		return m, false
	}
	m.Field, m.UnitField, m.Unit, m.Label = chosen.Field, chosen.UnitField, chosen.Unit, chosen.Label
	return m, true
}
