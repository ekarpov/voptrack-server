package main

import (
	"errors"
	"fmt"
	"strings"
)

// Research tracks, the way the app's composer describes them: a name, and
// values of five kinds, a number optionally carrying a unit and a meaning.
// A researcher designs these; nobody is asked for a schema. The schema is
// derived here, exactly as the app derives it for its own custom tracks --
// the contract is voptrack-ios docs/research-track-contract.md, and
// testdata/research-vectors (the app's own output) pins every byte of it.
//
// Every study track is a child of the app's Track root, com.voptrack.track,
// which gives it subject, source and timestamp: when a thing happened is the
// root's timestamp, not a field of the study's own. The study's issuer
// installs the root in each participant's store, because a participant holds
// no schema rights and their app cannot.

// FieldKind is a composer kind.
type FieldKind string

const (
	KindNumber      FieldKind = "number"
	KindWholeNumber FieldKind = "wholeNumber"
	KindText        FieldKind = "text"
	KindYesNo       FieldKind = "yesNo"
	KindDateTime    FieldKind = "dateTime"
)

var fieldTypes = map[FieldKind]string{
	KindNumber: "float", KindWholeNumber: "integer", KindText: "text", KindYesNo: "boolean", KindDateTime: "date_time",
}

// Kinds in the order a researcher is offered them.
var Kinds = []FieldKind{KindNumber, KindWholeNumber, KindText, KindYesNo, KindDateTime}

func (k FieldKind) numeric() bool { return k == KindNumber || k == KindWholeNumber }

// TrackField is one value a track records.
type TrackField struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    FieldKind `json:"kind"`
	Unit    string    `json:"unit,omitempty"`    // numbers only; free text, never normalised
	Meaning string    `json:"meaning,omitempty"` // numbers only: "additive" or "sample"
}

// TrackDesign is one track as the researcher designed it. Catalogue names one
// of the app's own tracks instead, installed exactly as the app defines it.
type TrackDesign struct {
	TypeID    string       `json:"type_id"`
	Name      string       `json:"name"`
	Fields    []TrackField `json:"fields,omitempty"`
	Catalogue string       `json:"catalogue,omitempty"`
}

// The app's Subject and Source, exactly as the app installs them
// (subject.json, source.json). Root types with no parent. A participant holds
// no schema right, so the study installs them: the app then makes one
// anonymous "Participant" subject and one "Voptrack" source in the store, and
// every record names both -- never the person's own subject, whose reference
// would be the same in every study they join.
func subjectType() TypeDef {
	const id = "com.voptrack.subject"
	return TypeDef{ID: id, Name: "model.subject", Fields: []TypeField{
		{ID: "name", MetaID: id, Name: "model.subject.field.name", Type: "blob"},
		{ID: "avatar", MetaID: id, Name: "model.subject.field.avatar", Type: "reference"},
		{ID: "details", MetaID: id, Name: "model.subject.field.details", Type: "blob"},
		{ID: "avatarStyle", MetaID: id, Name: "model.subject.field.avatar", Type: "blob"},
	}}
}

func sourceType() TypeDef {
	const id = "com.voptrack.source"
	return TypeDef{ID: id, Name: "model.source", Fields: []TypeField{
		{ID: "name", MetaID: id, Name: "model.source.field.name", Type: "blob"},
	}}
}

// supportTypes are what a study installs before its tracks, in order: what
// the root's subject and source references name, then the root itself.
func supportTypes() []TypeDef { return []TypeDef{subjectType(), sourceType(), trackRoot()} }

// The app's Track root, exactly as the app installs it (store.json).
func trackRoot() TypeDef {
	const id = "com.voptrack.track"
	f := func(field, typ string, roles ...string) TypeField {
		return TypeField{ID: field, MetaID: id, Name: "model.track.field." + field, Type: typ, Roles: roles}
	}
	return TypeDef{ID: id, Name: "model.track", Roles: []string{"track"}, Fields: []TypeField{
		f("subject", "reference", "subject"),
		f("source", "reference", "source"),
		f("timestamp", "date_time", "timestamp"),
		f("endTimestamp", "date_time", "end_timestamp"),
		f("plan", "reference"),
		f("planPeriod", "text"),
		f("planOccurrence", "date_time"),
	}}
}

// catalogue is the app's own tracks a study may use, each exactly as the app
// installs it. Only tracks the app has published a vector for are offered: a
// guessed schema would be a different type to the app.
var catalogue = map[string]func() TypeDef{
	"com.voptrack.track.weight": func() TypeDef {
		const id = "com.voptrack.track.weight"
		root := trackRoot().ID
		return TypeDef{ID: id, ParentID: &root, Name: "strings.track.weight", Fields: []TypeField{
			{ID: "weight", MetaID: id, Name: "strings.track.weight.field.weight", Type: "float"},
			{ID: "unit", MetaID: id, Name: "strings.track.weight.field.unit", Type: "text"},
		}}
	},
}

// CatalogueTracks lists what a researcher may pick, with a readable name.
var CatalogueTracks = []struct{ ID, Name string }{{"com.voptrack.track.weight", "Weight"}}

// studyID is a study's part of its type identifiers. It is derived once, when
// the study is created, and never recomputed; the app treats it as opaque.
func studyID(slug string) string {
	id := identifier(slug)
	if strings.HasPrefix(id, "t_") && len(slug) > 0 && slug[0] >= '0' && slug[0] <= '9' {
		id = "s" + strings.TrimPrefix(id, "t_")
	}
	return id
}

// schema is the type the app would install for this design.
func (d TrackDesign) schema() (TypeDef, error) {
	if d.Catalogue != "" {
		make, ok := catalogue[d.Catalogue]
		if !ok {
			return TypeDef{}, fmt.Errorf("%q is not a track this Console can install", d.Catalogue)
		}
		return make(), nil
	}
	root := trackRoot().ID
	t := TypeDef{ID: d.TypeID, ParentID: &root, Name: d.Name}
	for _, f := range d.Fields {
		typ, ok := fieldTypes[f.Kind]
		if !ok {
			return TypeDef{}, fmt.Errorf("%q is not a kind of value", f.Kind)
		}
		field := TypeField{ID: f.ID, MetaID: d.TypeID, Name: f.Name, Type: typ}
		if f.Meaning != "" {
			field.Annotations = map[string]string{"meaning": f.Meaning}
		}
		if f.Unit == "" {
			t.Fields = append(t.Fields, field)
			continue
		}
		if field.Annotations == nil {
			field.Annotations = map[string]string{}
		}
		unitField := f.ID + "_unit"
		field.Annotations["unit"], field.Annotations["unit_field"], field.Annotations["unit_source"] = f.Unit, unitField, "record"
		t.Fields = append(t.Fields, field,
			TypeField{ID: unitField, MetaID: d.TypeID, Name: f.Name + " unit", Type: "text"})
	}
	return t, nil
}

// studyTypes is everything a study's issuer installs, in order: Subject,
// Source and the Track root, then each track. Validation happens here, so a study cannot open with a
// design the app would not read.
func studyTypes(tracks []TrackDesign) ([]TypeDef, error) {
	if len(tracks) == 0 {
		return nil, errors.New("a study needs at least one track")
	}
	out := supportTypes()
	seen := map[string]bool{}
	for _, d := range tracks {
		if err := d.validate(); err != nil {
			return nil, err
		}
		t, err := d.schema()
		if err != nil {
			return nil, err
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("two tracks are called %q", d.Name)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out, nil
}

func (d TrackDesign) validate() error {
	if d.Catalogue != "" {
		return nil
	}
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("every track needs a name")
	}
	if len(d.Fields) == 0 {
		return fmt.Errorf("track %q records no values", d.Name)
	}
	ids := map[string]bool{}
	for _, f := range d.Fields {
		if strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("track %q has a value with no name", d.Name)
		}
		if _, ok := fieldTypes[f.Kind]; !ok {
			return fmt.Errorf("%q in %q: choose what kind of value it is", f.Name, d.Name)
		}
		if !f.Kind.numeric() && (f.Unit != "" || f.Meaning != "") {
			return fmt.Errorf("%q in %q: only numbers have a unit or say how they add up", f.Name, d.Name)
		}
		if f.Meaning != "" && f.Meaning != "additive" && f.Meaning != "sample" {
			return fmt.Errorf("%q in %q: a number either adds up to a total or is a reading", f.Name, d.Name)
		}
		for _, id := range []string{f.ID, f.ID + "_unit"} {
			if ids[id] {
				return fmt.Errorf("track %q has two values called %q", d.Name, f.Name)
			}
		}
		ids[f.ID] = true
		if f.Unit != "" {
			ids[f.ID+"_unit"] = true
		}
	}
	return nil
}

// studyTracks is a study's tracks alone: what people record into, without the
// types they rest on.
func studyTracks(tracks []TrackDesign) ([]TypeDef, error) {
	types, err := studyTypes(tracks)
	if err != nil {
		return nil, err
	}
	return types[len(supportTypes()):], nil
}
