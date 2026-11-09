package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func homeContractFixture(t *testing.T, name string, out any) {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "app-home-v1", name))
	must(t, e)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	must(t, d.Decode(out))
}
func homeTestMeta(id string) Meta {
	fields := sharedSubjectFields
	if id == homeCopyLabelType {
		fields = homeLabelFields
	}
	m := Meta{ID: id}
	var ids []string
	for key := range fields {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	for _, key := range ids {
		m.Fields = append(m.Fields, Field{ID: key, Type: fields[key], MetaID: id})
	}
	return m
}
func homeTestRecord(m Meta, dataset, id string, values map[string]any) readRecord {
	return readRecord{ID: id, Dataset: dataset, Type: m.ID, Fields: m.Fields, Values: values}
}
func homeTestSubject(dataset, object, id, name string) readRecord {
	return homeTestRecord(homeTestMeta(sharedSubjectType), dataset, object, map[string]any{"contract_version": json.Number("1"), "subject_id": id, "name": name, "category": "pet", "kind_text": "Dog"})
}
func homeTestLabel(dataset, id string, record objectReference, subject, category string) readRecord {
	return homeTestRecord(homeTestMeta(homeCopyLabelType), dataset, id, map[string]any{
		"record": record, "track_type": record.Type, "at": time.Now().UTC().Format(time.RFC3339Nano),
		"contract_version": json.Number("1"), "subject_id": subject, "subject": "Old display name", "kind": "pet", "kind_text": "Dog", "category": category,
		// These figures must NEVER become another payment or replace the canonical value.
		"amount": json.Number("999999"), "currency": "USD", "value": json.Number("999"),
	})
}

func TestHomeContractsUsePinnedExamplesAndAdditiveEvolution(t *testing.T) {
	var p struct {
		SourceRevision string
		Files          map[string]string
	}
	homeContractFixture(t, "provenance.json", &p)
	if p.SourceRevision != "dd0fd9a0d3b6ed93fd9eb4f7830a5586affed65a" {
		t.Fatal("contract provenance changed")
	}
	for path, want := range p.Files {
		raw, e := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "app-home-v1", path))
		must(t, e)
		hash := sha256.Sum256(raw)
		if hex.EncodeToString(hash[:]) != want {
			t.Fatal("contract artifact hash mismatch")
		}
	}
	var set recordSet
	homeContractFixture(t, "examples/shared-subject-v1.read.json", &set)
	if len(set.Records) != 2 {
		t.Fatal("unexpected synthetic fixture shape")
	}
	rows, e := decodeRecords(set, set.Records[0].ID.Dataset, "Member")
	must(t, e)
	sub, ok := decodeHomeSubject(rows[0], "Member")
	if !ok || sub.Name != "Rex" || sub.Family != "pets" {
		t.Fatal("published shared-subject example was not readable")
	}
	key := sub.Key
	rows[0].Values["name"] = "Renamed"
	rows[0].Values["contract_version"] = json.Number("200")
	rows[0].Fields = append(rows[0].Fields, Field{ID: "future_optional", Type: "blob"})
	rows[0].Values["future_optional"] = "opaque"
	rows[0].Values["category"] = "future-category"
	sub, ok = decodeHomeSubject(rows[0], "Member")
	if !ok || sub.Key != key || sub.Name != "Renamed" || sub.Family != "other" {
		t.Fatal("additive version or rename changed identity")
	}
	rows[0].Values["contract_version"] = "200"
	if _, ok = decodeHomeSubject(rows[0], "Member"); ok {
		t.Fatal("wrong version wire type was accepted")
	}
	rows[0].Values["contract_version"] = nil
	if _, ok = decodeHomeSubject(rows[0], "Member"); ok {
		t.Fatal("missing required subject version was accepted")
	}
	if sharedHomeKey("a", "opaque") == sharedHomeKey("b", "opaque") || sharedHomeKey("a", "opaque") == sharedHomeKey("a", "new-id") {
		t.Fatal("store or erase/unshare identity boundary was lost")
	}
	for _, field := range []string{"subject_id", "name", "category"} {
		bad := homeTestSubject("a", "object", "opaque", "Rex")
		bad.Values[field] = nil
		if _, ok = decodeHomeSubject(bad, "Member"); ok {
			t.Fatal("missing required subject field was accepted")
		}
	}
	if !homeInfrastructure(sharedSubjectType) || !homeInfrastructure(homeCopyLabelType) || homeInfrastructure("org.example.custom") {
		t.Fatal("contract metadata became a Track or custom data was hidden")
	}
}

func TestHomeLabelLegacyVersionsAndConflicts(t *testing.T) {
	record := objectReference{"a", "com.voptrack.track.expense", "record"}
	label := homeTestLabel("a", "label", record, "opaque", "Groceries")
	label.Values["contract_version"] = nil
	if _, ok := decodeHomeLabel(label); !ok {
		t.Fatal("legacy null label version rejected")
	}
	label.Values["contract_version"] = json.Number("300")
	if _, ok := decodeHomeLabel(label); !ok {
		t.Fatal("future additive label version rejected")
	}
	label.Values["record"] = objectReference{"private", record.Type, record.ID}
	if _, ok := decodeHomeLabel(label); ok {
		t.Fatal("cross-store label accepted")
	}
	label.Values["record"] = record
	label.Values["track_type"] = "other"
	if _, ok := decodeHomeLabel(label); ok {
		t.Fatal("label type did not match canonical record")
	}
}

type homeReadFixture struct {
	tessera *Tessera
	metas   map[string][]Meta
	rows    map[string][]readRecord
	denied  map[string]bool
	begun   map[string]int
}

func homeFixtureServer(t *testing.T, metas map[string][]Meta, rows map[string][]readRecord) *homeReadFixture {
	t.Helper()
	f := &homeReadFixture{metas: metas, rows: rows, denied: map[string]bool{}, begun: map[string]int{}}
	type cursor struct {
		key    string
		offset int
	}
	cursors := map[string]*cursor{}
	var mu sync.Mutex
	sequence := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 4 || parts[0] != "v1" || parts[1] != "datasets" {
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
			return
		}
		dataset := parts[2]
		if r.Header.Get("Authorization") != "Bearer home-"+dataset {
			t.Error("reader authority changed")
			w.WriteHeader(403)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case parts[3] == "meta":
			writeJSON(w, 200, map[string]any{"types": f.metas[dataset]})
		case len(parts) == 5 && parts[3] == "objects" && strings.HasSuffix(parts[4], ":page"):
			typ := strings.TrimSuffix(parts[4], ":page")
			key := dataset + "|" + typ
			if f.denied[key] {
				w.WriteHeader(403)
				return
			}
			var request struct{ Query map[string]any }
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("invalid query body")
				w.WriteHeader(400)
				return
			}
			if typ == sharedSubjectType && request.Query["filter"] != nil {
				t.Error("subject discovery was filtered by activity time")
			}
			f.begun[key]++
			sequence++
			id := fmt.Sprint(sequence)
			cursors[id] = &cursor{key: key}
			writeJSON(w, 200, map[string]any{"cursor": id})
		case len(parts) == 5 && parts[3] == "pages" && r.Method == "DELETE":
			delete(cursors, parts[4])
			w.WriteHeader(204)
		case len(parts) == 5 && parts[3] == "pages" && strings.HasSuffix(parts[4], ":next"):
			id := strings.TrimSuffix(parts[4], ":next")
			c := cursors[id]
			if c == nil {
				w.WriteHeader(404)
				return
			}
			collection := f.rows[c.key]
			end := min(len(collection), c.offset+100)
			var records []wireRecord
			var fields []Field
			for _, m := range f.metas[dataset] {
				if dataset+"|"+m.ID == c.key {
					fields = m.Fields
				}
			}
			for _, rec := range collection[c.offset:end] {
				values := Row{}
				for _, field := range fields {
					values = append(values, (Row{{Type: field.Type, Value: rec.Values[field.ID]}})[0])
				}
				records = append(records, wireRecord{ID: recordIdentity{ID: rec.ID, Dataset: dataset}, Type: rec.Type, Values: values})
			}
			c.offset = end
			set := recordSet{Meta: Meta{Fields: fields}, Records: records}
			if end < len(collection) {
				set.Next = &id
			}
			writeJSON(w, 200, set)
		default:
			t.Error("reference escaped the explicit contract reads")
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	f.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	return f
}

func homeSourcesForTest(ids ...string) []Source {
	var sources []Source
	for _, id := range ids {
		id := id
		sources = append(sources, Source{Dataset: id, Name: "Member " + id, Home: true, Token: func(context.Context) (string, error) { return "home-" + id, nil }})
	}
	return sources
}
func TestHomeInventoryLabelsAndCanonicalTotals(t *testing.T) {
	expense := catalogueTypeForTest(t, "com.voptrack.track.expense")
	weight := catalogueTypeForTest(t, "com.voptrack.track.weight")
	now := time.Now().UTC()
	old := now.AddDate(0, -2, 0)
	refA := objectReference{"a", sharedSubjectType, "subject-a"}
	refB := objectReference{"b", sharedSubjectType, "subject-b"}
	aRecord := homeTestRecord(expense.Schema, "a", "expense-a", map[string]any{"subject": refA, "timestamp": now.Add(-time.Minute).Format(time.RFC3339Nano), "amount": json.Number("10000"), "currency": "EUR", "purpose": "Groceries"})
	bRecord := homeTestRecord(expense.Schema, "b", "expense-b", map[string]any{"subject": refB, "timestamp": now.Add(-time.Minute).Format(time.RFC3339Nano), "amount": json.Number("20000"), "currency": "EUR", "purpose": "Groceries"})
	oldWeight := homeTestRecord(weight.Schema, "a", "weight-old", map[string]any{"subject": refA, "timestamp": old.Format(time.RFC3339Nano), "weight": json.Number("24.8"), "unit": "kg"})
	labelA := homeTestLabel("a", "label-a", objectReference{"a", expense.ID, "expense-a"}, "same-opaque-id", "Groceries")
	duplicate := labelA
	duplicate.ID = "label-a-repeat"
	orphan := homeTestLabel("a", "orphan", objectReference{"a", expense.ID, "missing"}, "same-opaque-id", "Other")
	f := homeFixtureServer(t, map[string][]Meta{
		"a": {homeTestMeta(sharedSubjectType), homeTestMeta(homeCopyLabelType), expense.Schema, weight.Schema},
		"b": {homeTestMeta(sharedSubjectType), homeTestMeta(homeCopyLabelType), expense.Schema},
	}, map[string][]readRecord{
		"a|" + sharedSubjectType: {homeTestSubject("a", "subject-a", "same-opaque-id", "Rex")},
		"b|" + sharedSubjectType: {homeTestSubject("b", "subject-b", "same-opaque-id", "Rex")},
		"a|" + homeCopyLabelType: {labelA, duplicate, orphan},
		"b|" + homeCopyLabelType: {homeTestLabel("b", "label-b", objectReference{"b", expense.ID, "expense-b"}, "same-opaque-id", "Groceries")},
		"a|" + expense.ID:        {aRecord}, "b|" + expense.ID: {bRecord}, "a|" + weight.ID: {oldWeight},
	})
	sources := homeSourcesForTest("a", "b")
	cat, skipped := catalog(context.Background(), f.tessera, sources)
	if len(skipped) != 0 {
		t.Fatal("synthetic catalogue unavailable")
	}
	a := &app{tessera: f.tessera, tz: time.UTC}
	window, _ := requestedWindow(url.Values{"period": {"month"}}, now)
	h, e := a.buildHousehold(httptest.NewRequest("GET", "/home?period=month", nil), sources, cat, window, false)
	must(t, e)
	if len(h.Subjects) != 2 || h.Subjects[0].Key == h.Subjects[1].Key {
		t.Fatal("same-named store-scoped subjects merged")
	}
	if h.ExpenseCount != 2 || h.Current != "EUR 300.00" || len(h.Spending) != 1 || h.Spending[0].Value != "EUR 300.00" {
		t.Fatal("labels changed canonical money totals or categories")
	}
	if h.ContextIncomplete {
		t.Fatal("identical duplicate labels caused a false failure")
	}
	for _, row := range h.Rows {
		if row.Subject != "Rex" || row.SubjectKey == "" {
			t.Fatal("document name did not override the stale label name")
		}
	}
	quiet := exploreWindow{Key: "week", Label: "Selected week", From: now.AddDate(0, 0, 1), To: now.AddDate(0, 0, 8), PreviousFrom: now.AddDate(0, 0, -6), PreviousTo: now.AddDate(0, 0, 1), Bucket: "day"}
	h, e = a.buildHousehold(httptest.NewRequest("GET", "/home?period=week&q=no-match", nil), sources, cat, quiet, false)
	must(t, e)
	if len(h.Subjects) != 2 || len(h.Rows) != 0 {
		t.Fatal("activity filters removed the independent inventory")
	}
	clearFilters, err := url.Parse(h.ClearURL)
	must(t, err)
	clearSearch, err := url.Parse(h.Controls.SearchClearURL)
	must(t, err)
	if clearFilters.Query().Get("q") != "no-match" || clearSearch.Query().Has("q") || clearSearch.Query().Get("period") != "week" {
		t.Fatal("household resets do not keep search and date selection independent")
	}
	key := sharedHomeKey("a", "same-opaque-id")
	h, e = a.buildHousehold(httptest.NewRequest("GET", "/home?view=pets&subject="+key, nil), sources, cat, quiet, false)
	must(t, e)
	if h.SelectedSubject == nil || h.Title != "Rex" || len(h.Rows) != 0 || len(h.LatestFacts) != 1 {
		t.Fatal("quiet named page lost identity or independently dated latest value")
	}
	if !strings.Contains(h.LatestFacts[0].Value, "24.8") || !strings.Contains(h.LatestFacts[0].Detail, old.Format("2 Jan 2006")) {
		t.Fatal("latest fact lost its original recorded date")
	}
	// No label: the new root reference still names the minimal shared document.
	f.rows["a|"+homeCopyLabelType] = nil
	h, e = a.buildHousehold(httptest.NewRequest("GET", "/home", nil), sources, cat, window, false)
	must(t, e)
	if h.ExpenseCount != 2 || h.Current != "EUR 300.00" {
		t.Fatal("missing label hid canonical records")
	}
	for _, row := range h.Rows {
		if row.Record.Dataset == "a" && row.Subject != "Rex" {
			t.Fatal("missing label broke root identity resolution")
		}
	}
	// Revocation affects current inventory immediately; no durable name cache exists.
	f.denied["a|"+sharedSubjectType] = true
	h, e = a.buildHousehold(httptest.NewRequest("GET", "/home?view=pets&subject="+key, nil), sources, cat, window, false)
	must(t, e)
	if !h.SubjectUnavailable || !h.ContextIncomplete || h.SelectedSubject != nil {
		t.Fatal("revoked minimal identity remained browsable")
	}
}

func TestHomeAmbiguousIdentitiesAndLabelsAreNotGuessed(t *testing.T) {
	expense := catalogueTypeForTest(t, "com.voptrack.track.expense")
	first := homeTestSubject("a", "first", "opaque / id", "Rex")
	second := homeTestSubject("a", "second", "opaque / id", "Another name")
	label := homeTestLabel("a", "label", objectReference{"a", expense.ID, "record"}, "opaque / id", "Food")
	conflict := label
	conflict.ID = "another-label"
	conflict.Values = map[string]any{}
	for k, v := range label.Values {
		conflict.Values[k] = v
	}
	conflict.Values["category"] = "Different"
	f := homeFixtureServer(t, map[string][]Meta{"a": {homeTestMeta(sharedSubjectType), homeTestMeta(homeCopyLabelType)}}, map[string][]readRecord{
		"a|" + sharedSubjectType: {first, second}, "a|" + homeCopyLabelType: {label, conflict},
	})
	shared := (&app{tessera: f.tessera}).readHomeShared(context.Background(), homeSourcesForTest("a", "a"), Catalog{Metadata: f.metas}, time.Time{}, time.Time{})
	if !shared.Incomplete || len(shared.Subjects) != 0 || len(shared.Labels) != 0 {
		t.Fatal("ambiguous identity or label was chosen arbitrarily")
	}
	if f.begun["a|"+sharedSubjectType] != 1 || f.begun["a|"+homeCopyLabelType] != 1 {
		t.Fatal("duplicate source caused duplicate contract reads")
	}
	// A label cannot rebind a shared record across stores, even with the same id.
	own := homeTestSubject("a", "first", "opaque", "Rex")
	sub, ok := decodeHomeSubject(own, "Member")
	if !ok {
		t.Fatal("fixture invalid")
	}
	row := householdRow{Record: homeTestRecord(expense.Schema, "b", "record", map[string]any{"subject": sub.Ref})}
	context := &homeSharedContext{Subjects: map[string]homeSharedSubject{refKey(sub.Ref): sub}, Labels: map[string]homeRecordLabel{}}
	context.decorate(&row)
	if row.SubjectKey != "" || row.Record.HomeSubjectKey != "" {
		t.Fatal("cross-store root became a local identity")
	}
}
func TestHomeInventoryLimitRemainsExplicit(t *testing.T) {
	var records []readRecord
	for i := 0; i < recordReadLimit+1; i++ {
		records = append(records, homeTestSubject("a", fmt.Sprint(i), "id-"+fmt.Sprint(i), "Rex"))
	}
	f := homeFixtureServer(t, map[string][]Meta{"a": {homeTestMeta(sharedSubjectType)}}, map[string][]readRecord{"a|" + sharedSubjectType: records})
	shared := (&app{tessera: f.tessera}).readHomeShared(context.Background(), homeSourcesForTest("a"), Catalog{Metadata: f.metas}, time.Time{}, time.Time{})
	if !shared.Incomplete || len(shared.Subjects) != recordReadLimit {
		t.Fatal("bounded inventory was presented as complete or discarded all known identities")
	}
}

func TestHomeStableSubjectFilterCannotFallBackToRawReference(t *testing.T) {
	ref := objectReference{"a", sharedSubjectType, "object"}
	row := readRecord{HomeSubjectKey: sharedHomeKey("a", "opaque"), Values: map[string]any{"subject": ref}}
	if len(filterSubject([]readRecord{row}, row.HomeSubjectKey)) != 1 || len(filterSubject([]readRecord{row}, refKey(ref))) != 0 {
		t.Fatal("stable subject filter changed identity domains")
	}
	row.HomeSubjectKey = ""
	if len(filterSubject([]readRecord{row}, refKey(ref))) != 0 {
		t.Fatal("unresolved shared subject bypassed the minimal document")
	}
}

func TestHomeFunctionalHeadingsAndQuietRender(t *testing.T) {
	for _, page := range []struct{ section, title string }{{"overview", "Home"}, {"expenses", "Spending"}, {"timeline", "Activity"}, {"add", "Add to your household"}} {
		h := &householdView{Title: page.title, Section: page.section, Subtitle: "November 2026", Controls: exploreControls{Range: "November 2026"}, Chart: Chart{Empty: true}, CompactChart: Chart{Empty: true}}
		body := renderUIForTest(t, httptest.NewRequest("GET", "/home", nil), "overview", overviewData{Scope: "home", Household: h})
		if !strings.Contains(body, "<h1>"+page.title+"</h1>") || (page.section != "add" && strings.Count(body, "November 2026") != 1) {
			t.Fatal("functional heading or selected period missing")
		}
		for _, bad := range []string{"A look at life together", "The little details", "bigger picture", "Shared subjects", "Category / subject"} {
			if strings.Contains(body, bad) {
				t.Fatal("marketing or engineering language returned")
			}
		}
	}
}

func TestHomePublishedLabelInsertsAndKnownSchemaDrift(t *testing.T) {
	for _, name := range []string{"home-copy-label-v1.insert.json", "home-copy-label-v1.expense-insert.json"} {
		var insert struct {
			Operations []struct {
				Type   string
				Fields map[string]struct {
					Type  string
					Value any
				}
			}
		}
		homeContractFixture(t, "examples/"+name, &insert)
		if len(insert.Operations) != 1 {
			t.Fatal("unexpected published label insert shape")
		}
		op := insert.Operations[0]
		ref, ok := valueReference(op.Fields["record"].Value)
		if !ok {
			t.Fatal("published label has no qualified record binding")
		}
		rec := readRecord{Dataset: ref.Dataset, ID: "fixture-label", Type: op.Type, Fields: homeTestMeta(homeCopyLabelType).Fields, Values: map[string]any{}}
		for key, cell := range op.Fields {
			rec.Values[key] = cell.Value
		}
		label, ok := decodeHomeLabel(rec)
		if !ok || label.Record != ref || label.Track != ref.Type {
			t.Fatal("published label example was not readable")
		}
	}
	var fixture struct{ Type Meta }
	homeContractFixture(t, "examples/shared-subject-v1.meta.json", &fixture)
	for _, key := range []string{"contract_version", "subject_id", "name", "category", "kind_text"} {
		bad := fixture.Type
		bad.Fields = append([]Field(nil), bad.Fields...)
		for i := range bad.Fields {
			if bad.Fields[i].ID == key {
				bad.Fields[i].Type = "blob"
			}
		}
		if homeContractMeta(bad, sharedSubjectType) {
			t.Fatal("known contract field type drift accepted")
		}
	}
}

func TestHomeRecordNameAndTitleStayWithinCanonicalParent(t *testing.T) {
	calls := 0
	doc := homeTestSubject("a", "subject", "opaque", "Rex")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer home-a" || r.URL.Path != "/v1/datasets/a/objects/"+sharedSubjectType+"/subject" {
			t.Error("detail lookup widened authority")
			w.WriteHeader(403)
			return
		}
		var values Row
		for _, field := range doc.Fields {
			values = append(values, (Row{{Type: field.Type, Value: doc.Values[field.ID]}})[0])
		}
		writeJSON(w, 200, recordSet{Meta: Meta{Fields: doc.Fields}, Records: []wireRecord{{ID: recordIdentity{ID: doc.ID, Dataset: doc.Dataset}, Type: doc.Type, Values: values}}})
	}))
	defer srv.Close()
	a := &app{tessera: &Tessera{internal: srv.URL, http: srv.Client()}}
	src := homeSourcesForTest("a")[0]
	rec := readRecord{Dataset: "a", Values: map[string]any{"subject": objectReference{"private", sharedSubjectType, "subject"}}}
	name, _ := a.homeRecordName(context.Background(), src, rec)
	if name != "" || calls != 0 {
		t.Fatal("detail followed a private cross-store reference")
	}
	rec.Values["subject"] = objectReference{"a", "com.voptrack.subject", "subject"}
	name, _ = a.homeRecordName(context.Background(), src, rec)
	if name != "" || calls != 0 {
		t.Fatal("detail followed a full original subject")
	}
	rec.Values["subject"] = objectReference{"a", sharedSubjectType, "subject"}
	name, kind := a.homeRecordName(context.Background(), src, rec)
	if name != "Rex" || kind != "Dog" || calls != 1 {
		t.Fatal("minimal detail context was not resolved")
	}
	rec.Fields = []Field{{ID: "purpose", Type: "text"}, {ID: "name", Type: "blob"}}
	rec.Values["purpose"] = "Recorded purpose"
	if homeEntryTitle(rec, "Expense") != "Recorded purpose" {
		t.Fatal("detail did not use recorded text")
	}
	rec.Values["purpose"] = nil
	rec.Values["name"] = "encoded-private-blob"
	if homeEntryTitle(rec, "Expense") != "Expense" {
		t.Fatal("detail title exposed an uninterpreted blob")
	}
}

func TestHomeReviewExport(t *testing.T) {
	dir := os.Getenv("VOPTRACK_UI_REVIEW_DIR")
	if dir == "" {
		t.Skip("optional local synthetic browser review export")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("review output directory must be absolute")
	}
	must(t, os.MkdirAll(filepath.Join(dir, "static"), 0700))
	// Reuse the exact embedded production assets, never a mock stylesheet.
	for _, name := range []string{"console.css", "brand-mark-light.svg", "brand-mark-dark.svg", "favicon.svg", "favicon.ico", "favicon-32.png", "favicon-16.png", "apple-touch-icon-180.png"} {
		raw, e := assets.ReadFile("static/" + name)
		must(t, e)
		must(t, os.WriteFile(filepath.Join(dir, "static", name), raw, 0600))
	}
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.Variant = "household"
	s.CSRF = "synthetic-review"
	tpl, e := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "localizeName": localizeName, "resultRows": resultRows, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText}).ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/overview.html")
	must(t, e)
	a.pages["overview"] = tpl
	// Rendering fixtures exercise the actual production templates. Data/permission
	// behavior is qualified by the reader/route tests, not by these static exports.
	sub := householdSubject{Name: "Rex", Monogram: "R", Member: "Sara", Kind: "Dog", Family: "pets", Key: "synthetic-key", URL: "/named.html", Latest: "7 Nov 2026"}
	for _, page := range []struct{ name, section, title string }{{"home", "overview", "Home"}, {"named", "pets", "Rex"}, {"spending", "expenses", "Spending"}, {"activity", "timeline", "Activity"}, {"add", "add", "Add to your household"}, {"quiet", "pets", "Rex"}, {"small", "overview", "Home"}, {"search", "expenses", "Spending"}, {"search-empty", "expenses", "Spending"}} {
		h := &householdView{Title: page.title, Section: page.section, Subtitle: "November 2026 · 1–7 November", ComputedAt: "7 November 2026, 12:00 UTC", Chart: Chart{Empty: true}, CompactChart: Chart{Empty: true}, RecordsLabel: "2 entries in this period", Controls: exploreControls{Period: "month", Periods: []exploreOption{{ID: "today", Name: "Today", URL: "/home.html"}, {ID: "month", Name: "Month", Selected: true, URL: "/home.html"}, {ID: "all", Name: "All", URL: "/activity.html"}}}}
		h.Subjects = []householdSubject{sub, {Name: "Our apartment", Monogram: "O", Member: "Ben", Kind: "Home", Family: "house", URL: "/activity.html"}, {Name: "Volvo", Monogram: "V", Member: "Ben", Kind: "Car", Family: "car", URL: "/activity.html"}}
		h.Rows = []householdRow{{Title: "Vet visit", Value: "EUR 50.00", Date: "6 Nov 2026, 14:00", Subject: "Rex", Member: "Sara", Category: "Pet care", Family: "expenses", URL: "/named.html"}, {Title: "Breakfast recorded", TrackName: "Feeding", Value: "180 g", Date: "7 Nov 2026, 08:00", Subject: "Rex", Member: "Sara", Family: "pets", URL: "/named.html"}}
		h.TotalRecords = 2
		if page.name == "named" || page.name == "quiet" {
			h.FilterSubject = sub.Key
			h.SelectedSubject = &sub
			h.LatestFacts = []householdMetric{{Name: "Weight", Value: "24.8 kg", Detail: "7 November 2026 · Sara", URL: "/activity.html"}}
		}
		if page.name == "quiet" {
			h.Subtitle = "December 2026"
			h.Rows = nil
			h.RecordsLabel = "No entries in this period"
		}
		if page.name == "home" || page.name == "spending" {
			h.HasExpenses = true
			h.ShowSpending = true
			h.Current = "EUR 648.70"
			h.HeroCaption = "1–7 November 2026"
			h.ExpenseCount = 9
			h.ExpenseScope = "Recorded expenses"
			h.TrendTitle = "Daily spending"
			chartData := Result{Spec: Spec{Measure: MeasureSum}}
			for i, amount := range []int64{18000, 8640, 5880, 9600, 6900, 11850, 4000} {
				chartData.Points = append(chartData.Points, Point{Label: fmt.Sprint(i+1) + " Nov", Tally: Tally{Rows: 1, Values: 1, Sum: big.NewRat(amount, 100)}})
			}
			h.Chart = buildChartSized(chartData, false, 720, 180)
			h.CompactChart = buildChartSized(chartData, false, 320, 180)
			h.CompactChart.Compact = true
			h.ChartRows = resultRows(chartData)
			h.Spending = []spendingGroup{{Name: "Groceries", Value: "EUR 251.40", Width: 39, URL: "/spending.html"}, {Name: "Home", Value: "EUR 180.00", Width: 28, URL: "/spending.html"}, {Name: "Transport", Value: "EUR 95.00", Width: 15, URL: "/spending.html"}, {Name: "Pet care", Value: "EUR 82.30", Width: 13, URL: "/spending.html"}, {Name: "Other", Value: "EUR 40.00", Width: 6, URL: "/spending.html"}}
		}
		if page.name == "small" {
			h.Subjects = nil
			h.Rows = nil
			h.RecordsLabel = "No shared activity"
		}
		h.Controls.Range = h.Subtitle
		h.Controls.View = page.section
		h.Controls.Subject = h.FilterSubject
		h.Controls.Subjects = []exploreOption{{Name: "Everyone and everything", Selected: h.FilterSubject == ""}, {ID: sub.Key, Name: "Rex", Selected: h.FilterSubject != ""}}
		h.Controls.Members = []exploreOption{{Name: "All members", Selected: true}, {ID: "sara", Name: "Sara"}, {ID: "ben", Name: "Ben"}}
		if page.name == "search" || page.name == "search-empty" {
			h.Controls.Query = "vet"
			h.HasExpenses = true
			h.Current = "EUR 50.00"
			h.ExpenseCount = 1
			h.TrendTitle = "Daily spending"
			h.ExpenseScope = "Recorded expenses"
			result := Result{Spec: Spec{Measure: MeasureSum}, Points: []Point{{Label: "6 Nov", Tally: Tally{Rows: 1, Values: 1, Sum: big.NewRat(50, 1)}}}}
			h.Chart = buildChartSized(result, false, 720, 180)
			h.CompactChart = buildChartSized(result, false, 320, 180)
			h.CompactChart.Compact = true
			h.Controls.SearchClearURL = "/spending.html"
			h.Controls.Categories = []exploreOption{{Name: "All categories", Selected: true}, {ID: "pet", Name: "Pet care"}}
			h.Filtered = true
			h.Rows = h.Rows[:1]
			h.RecordsLabel = "1 matching entry in this period"
			if page.name == "search-empty" {
				h.Controls.Query = "no-match"
				h.Current = "EUR 0.00"
				h.ExpenseCount = 0
				h.Chart = Chart{Empty: true}
				h.CompactChart = Chart{Empty: true}
				h.Rows = nil
				h.RecordsLabel = "No matching entries"
			}
		}
		r := httptest.NewRequest("GET", "/home?view="+page.section, nil)
		out := httptest.NewRecorder()
		a.render(out, r, s, "overview", 200, overviewData{Scope: "home", Household: h})
		if out.Code != 200 || !strings.Contains(out.Body.String(), "<h1>"+page.title+"</h1>") || !strings.Contains(out.Body.String(), "</html>") {
			t.Fatal("production review render incomplete")
		}
		must(t, os.WriteFile(filepath.Join(dir, page.name+".html"), out.Body.Bytes(), 0600))
	}
}

func TestHomeLabelsKeepSameObjectIDsInDifferentTypesSeparate(t *testing.T) {
	refs := []objectReference{{"a", "com.voptrack.track.expense", "same"}, {"a", "com.voptrack.track.weight", "same"}}
	f := homeFixtureServer(t, map[string][]Meta{"a": {homeTestMeta(homeCopyLabelType)}}, map[string][]readRecord{
		"a|" + homeCopyLabelType: {homeTestLabel("a", "one", refs[0], "opaque", "Food"), homeTestLabel("a", "two", refs[1], "opaque", "")},
	})
	c := (&app{tessera: f.tessera}).readHomeShared(context.Background(), homeSourcesForTest("a"), Catalog{Metadata: f.metas}, time.Time{}, time.Time{})
	if c.Incomplete || len(c.Labels) != 2 {
		t.Fatal("same-type copy or concrete record identity boundary lost")
	}
}
