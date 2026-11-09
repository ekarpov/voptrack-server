package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const sharedSubjectType = "com.voptrack.shared_subject"
const homeCopyLabelType = "com.voptrack.home_copy_label"

// These are read contracts, not install payloads. Unknown optional fields and
// contract versions are accepted independently of the immutable Track catalogue.
var sharedSubjectFields = map[string]string{
	"contract_version": "integer", "subject_id": "text", "name": "text", "category": "text", "kind_text": "text",
}
var homeLabelFields = map[string]string{
	"contract_version": "integer", "record": "reference", "track_type": "text", "at": "date_time",
	"amount": "integer", "currency": "text", "category": "text", "subject": "text", "kind": "text",
	"subject_id": "text", "kind_text": "text", "value": "float", "unit": "text", "note": "text",
}

type homeSharedSubject struct {
	Ref                                           objectReference
	ID, Key, Name, Category, Kind, Member, Family string
}
type homeRecordLabel struct {
	Record                                                 objectReference
	Track, SubjectID, Name, Category, Kind, KindText, Note string
}
type homeSharedContext struct {
	Subjects   map[string]homeSharedSubject // qualified object reference -> minimal document
	Labels     map[string]homeRecordLabel   // qualified canonical record -> one unambiguous label
	Incomplete bool
}

func homeContractMeta(m Meta, id string) bool {
	if m.ID != id || m.ParentID != nil {
		return false
	}
	fields, required := sharedSubjectFields, []string{"contract_version", "subject_id", "name", "category"}
	if id == homeCopyLabelType {
		fields, required = homeLabelFields, []string{"record", "track_type", "at"}
	}
	seen := map[string]bool{}
	for _, f := range m.Fields {
		if seen[f.ID] {
			return false
		}
		seen[f.ID] = true
		if want, known := fields[f.ID]; known && f.Type != want {
			return false
		}
	}
	for _, id := range required {
		if !seen[id] {
			return false
		}
	}
	return true
}
func homeContractVersion(v any, legacy bool) bool {
	if v == nil {
		return legacy
	}
	switch v.(type) {
	case json.Number, int, int64, float64:
	default:
		return false
	}
	n, err := rat(v)
	return err == nil && n != nil && n.IsInt()
}
func homeText(rec readRecord, key string, required bool) (string, bool) {
	v := rec.Values[key]
	if v == nil {
		return "", !required
	}
	s, ok := v.(string)
	return s, ok
}
func sharedHomeKey(dataset, id string) string {
	return memberKey("shared-subject\x00" + dataset + "\x00" + id)
}
func sharedHomeFamily(category, kind string) string {
	if category == "pet" {
		return "pets"
	}
	if category == "thing" {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "car", "vehicle":
			return "car"
		case "home", "house", "apartment":
			return "house"
		}
	}
	return "other"
}
func homeMemberName(src Source) string {
	if src.Name == "" || src.Name == src.Dataset {
		return "Household member"
	}
	return src.Name
}
func decodeHomeSubject(rec readRecord, member string) (homeSharedSubject, bool) {
	if rec.Type != sharedSubjectType || !homeContractMeta(Meta{ID: rec.Type, Fields: rec.Fields}, sharedSubjectType) || !homeContractVersion(rec.Values["contract_version"], false) {
		return homeSharedSubject{}, false
	}
	id, ok := homeText(rec, "subject_id", true)
	if !ok || id == "" {
		return homeSharedSubject{}, false
	}
	name, ok := homeText(rec, "name", true)
	if !ok {
		return homeSharedSubject{}, false
	}
	category, ok := homeText(rec, "category", true)
	if !ok {
		return homeSharedSubject{}, false
	}
	kind, ok := homeText(rec, "kind_text", false)
	if !ok {
		return homeSharedSubject{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Name unavailable"
	}
	// Free text is a presentation hint, not a structured kind or a health judgment.
	return homeSharedSubject{Ref: objectReference{rec.Dataset, rec.Type, rec.ID}, ID: id, Key: sharedHomeKey(rec.Dataset, id), Name: name, Category: category, Kind: kind, Member: member, Family: sharedHomeFamily(category, kind)}, true
}
func decodeHomeLabel(rec readRecord) (homeRecordLabel, bool) {
	if rec.Type != homeCopyLabelType || !homeContractMeta(Meta{ID: rec.Type, Fields: rec.Fields}, homeCopyLabelType) || !homeContractVersion(rec.Values["contract_version"], true) {
		return homeRecordLabel{}, false
	}
	ref, ok := valueReference(rec.Values["record"])
	if !ok || ref.Dataset != rec.Dataset || homeInfrastructure(ref.Type) {
		return homeRecordLabel{}, false
	}
	track, ok := homeText(rec, "track_type", true)
	if !ok || track != ref.Type {
		return homeRecordLabel{}, false
	}
	out := homeRecordLabel{Record: ref, Track: track}
	for key, dest := range map[string]*string{"subject_id": &out.SubjectID, "subject": &out.Name, "category": &out.Category, "kind": &out.Kind, "kind_text": &out.KindText, "note": &out.Note} {
		value, valid := homeText(rec, key, false)
		if !valid {
			return homeRecordLabel{}, false
		}
		*dest = value
	}
	return out, true
}

// Inventory is independent of the activity window and never durable across
// requests. Missing grants, failed reads and a cap cannot become "no pet".
func (a *app) readHomeShared(ctx context.Context, sources []Source, cat Catalog, from, to time.Time) *homeSharedContext {
	out := &homeSharedContext{Subjects: map[string]homeSharedSubject{}, Labels: map[string]homeRecordLabel{}}
	visited := map[string]bool{}
	identities := map[string]string{}
	badIdentity, badLabel := map[string]bool{}, map[string]bool{}
	for _, src := range sources {
		if visited[src.Dataset] {
			continue
		}
		visited[src.Dataset] = true
		for _, m := range cat.Metadata[src.Dataset] {
			if m.ID != sharedSubjectType && m.ID != homeCopyLabelType {
				continue
			}
			if !homeContractMeta(m, m.ID) {
				out.Incomplete = true
				continue
			}
			tok, err := src.Token(ctx)
			if err != nil {
				out.Incomplete = true
				continue
			}
			query := map[string]any{"type_scope": map[string]any{"kind": "exact"}}
			if m.ID == homeCopyLabelType {
				query["result_sort"] = []any{map[string]any{"field_id": "at", "ascending": false}}
				if window := (Spec{Date: "at", From: from, To: to}).window(); window != nil {
					query["filter"] = window
				}
			}
			var rows []readRecord
			var cut bool
			err = retryRefused(ctx, src, &tok, func(token string) error {
				var e error
				rows, cut, e = a.tessera.recordRows(ctx, token, src.Dataset, m.ID, query, recordReadLimit)
				return e
			})
			if err != nil {
				out.Incomplete = true
				continue
			}
			out.Incomplete = out.Incomplete || cut
			for _, rec := range rows {
				if rec.Type != m.ID || rec.Dataset != src.Dataset {
					out.Incomplete = true
					continue
				}
				if m.ID == sharedSubjectType {
					sub, ok := decodeHomeSubject(rec, homeMemberName(src))
					if !ok {
						out.Incomplete = true
						continue
					}
					ref := refKey(sub.Ref)
					if old, exists := identities[sub.Key]; exists && old != ref {
						delete(out.Subjects, old)
						badIdentity[sub.Key] = true
						out.Incomplete = true
					}
					identities[sub.Key] = ref
					if !badIdentity[sub.Key] {
						out.Subjects[ref] = sub
					}
				} else {
					label, ok := decodeHomeLabel(rec)
					if !ok {
						out.Incomplete = true
						continue
					}
					key := recordMapKey(readRecord{Dataset: label.Record.Dataset, Type: label.Record.Type, ID: label.Record.ID})
					if old, exists := out.Labels[key]; exists && old != label {
						delete(out.Labels, key)
						badLabel[key] = true
						out.Incomplete = true
					}
					if !badLabel[key] {
						out.Labels[key] = label
					}
				}
			}
		}
	}
	return out
}

// Only decorate canonical records the activity reader actually returned. An
// orphan label never creates history, a payment or a discovered subject.
func (c *homeSharedContext) decorate(row *householdRow) {
	rec := row.Record
	ref, hasRef := valueReference(rec.Values["subject"])
	sub, hasSub := c.Subjects[refKey(ref)]
	if hasRef && ref.Type == sharedSubjectType {
		row.Subject = "Details unavailable"
		row.SubjectKey = ""
		row.SubjectFamily = ""
		if !hasSub || ref.Dataset != rec.Dataset {
			c.Incomplete = true
		}
		if hasSub && ref.Dataset == rec.Dataset {
			row.Subject, row.SubjectKey, row.SubjectFamily = sub.Name, sub.Key, sub.Family
		}
	}
	label, hasLabel := c.Labels[recordMapKey(rec)]
	if hasLabel {
		// The root reference is authoritative. A disagreeing label is not a way to
		// reassign a record to another pet or evade a missing subject document.
		if hasRef && ref.Type == sharedSubjectType && (!hasSub || ref.Dataset != rec.Dataset || label.SubjectID != sub.ID) {
			c.Incomplete = true
		} else {
			if label.Category != "" {
				row.Category = label.Category
				row.CategoryKey = "label:" + memberKey(label.Category)
			}
			if row.SubjectKey == "" && label.Name != "" {
				row.Subject = strings.TrimSpace(label.Name)
			}
		}
	}
	if row.SubjectFamily != "" && row.Family != "expenses" {
		row.Family = row.SubjectFamily
	}
	row.Record.HomeSubjectKey = row.SubjectKey
}

// Legacy full subjects retain historical read compatibility only under an
// explicit Home reader. Shared minimal subjects never use the blob/details path.
func (r *householdResolver) legacySubject(origin string, ref objectReference) *readRecord {
	if ref.Type == sharedSubjectType || strings.HasPrefix(ref.Type, "com.voptrack.") && ref.Type != "com.voptrack.subject" {
		return nil
	}
	return r.object(origin, ref)
}

func (c *homeSharedContext) annotate(rows []readRecord, typ TypeInfo) {
	for i := range rows {
		row := householdRow{Record: rows[i], Type: typ}
		c.decorate(&row)
		rows[i].HomeSubjectKey = row.Record.HomeSubjectKey
	}
}

// Latest facts use only declared latest reductions and the selected store-scoped
// identity. They are separately dated, never presented as period totals.
func (a *app) latestHomeFacts(ctx context.Context, sources []Source, cat Catalog, types []TypeInfo, shared *homeSharedContext, key string, ctrl exploreControls) ([]householdMetric, bool) {
	var target *homeSharedSubject
	for _, sub := range shared.Subjects {
		if sub.Key == key {
			copy := sub
			target = &copy
			break
		}
	}
	if target == nil || ctrl.Query != "" || ctrl.Member != "" && ctrl.Member != memberKey(target.Ref.Dataset) {
		return nil, false
	}
	var source []Source
	for _, src := range sources {
		if src.Dataset == target.Ref.Dataset {
			source = []Source{src}
			break
		}
	}
	if len(source) == 0 {
		return nil, true
	}
	var facts []householdMetric
	unavailable := false
	now := time.Now().In(a.tz)
	for _, typ := range types {
		m := metricFor(typ)
		if !qualifiedHomeMetric(typ) || !m.Latest || m.Unsupported {
			continue
		}
		rows, states := a.readHouseholdTrack(ctx, source, cat, typ, time.Time{}, now)
		for _, state := range states {
			if state == "Unavailable" || state == "Reading limit reached" || state == "Different schema" {
				unavailable = true
			}
		}
		shared.annotate(rows, typ)
		rows = filterSubject(rows, key)
		var valid []readRecord
		from := now
		for _, row := range rows {
			if row.When.IsZero() || row.When.After(now) {
				continue
			}
			valid = append(valid, row)
			if row.When.Before(from) {
				from = row.When
			}
		}
		if len(valid) == 0 {
			continue
		}
		for _, series := range viewSeries(valid, typ, a.tz, from, false, "month") {
			latest := time.Time{}
			for _, rec := range series.Records {
				if rec.When.After(latest) {
					latest = rec.When
				}
			}
			v := filterQuery(ctrl)
			v.Set("scope", "home")
			v.Set("type", typ.ID)
			v.Set("subject", key)
			v.Set("period", "all")
			v.Set("series", series.Key)
			for _, field := range []string{"month", "from", "to", "bucket", "offset"} {
				v.Del(field)
			}
			facts = append(facts, householdMetric{TypeID: typ.ID, Name: trackTitle(typ), Value: formattedValue(seriesValue(series), series.Metric, series.Unit), Detail: latest.In(a.tz).Format("2 Jan 2006, 15:04 MST") + " · " + series.Label, URL: "/tracks?" + v.Encode()})
		}
	}
	return facts, unavailable
}

// Detail context follows only a same-store minimal subject reference after the
// parent record has been authorized. No full/private original subject is read.
func (a *app) homeRecordName(ctx context.Context, src Source, rec readRecord) (string, string) {
	ref, ok := valueReference(rec.Values["subject"])
	if !ok || ref.Dataset != rec.Dataset || src.Dataset != rec.Dataset || ref.Type != sharedSubjectType {
		return "", ""
	}
	tok, err := src.Token(ctx)
	if err != nil {
		return "", ""
	}
	var document readRecord
	err = retryRefused(ctx, src, &tok, func(token string) error {
		var e error
		document, e = a.tessera.recordRead(ctx, token, ref.Dataset, ref.Type, ref.ID)
		return e
	})
	if err != nil {
		return "", ""
	}
	sub, ok := decodeHomeSubject(document, homeMemberName(src))
	if !ok {
		return "", ""
	}
	return sub.Name, sub.Kind
}
func homeEntryTitle(rec readRecord, fallback string) string {
	for _, key := range []string{"purpose", "description", "task", "name", "text", "reason", "food"} {
		for _, field := range rec.Fields {
			if field.ID == key && field.Type == "text" {
				if text, ok := rec.Values[key].(string); ok && strings.TrimSpace(text) != "" {
					return text
				}
			}
		}
	}
	return fallback
}
