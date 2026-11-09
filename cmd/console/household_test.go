package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHouseholdReferenceIdentityAndDisplayText(t *testing.T) {
	a := objectReference{Dataset: "a", Type: "subject", ID: "same"}
	b := a
	b.Dataset = "b"
	if refKey(a) == refKey(b) {
		t.Fatal("same-named subjects in different datasets were merged")
	}
	for _, tc := range []struct{ raw, want string }{
		{`{"literal":{"_0":"Milo"}}`, "Milo"},
		{`{"external":{"translations":{"en":"Family car","fi":"Auto"},"fallback":"Car"}}`, "Family car"},
	} {
		got := displayBlob(base64.StdEncoding.EncodeToString([]byte(tc.raw)))
		if got != tc.want {
			t.Fatalf("display text: got %q want %q", got, tc.want)
		}
	}
	rec := &readRecord{Fields: []Field{{ID: "name", Type: "blob"}}, Values: map[string]any{"name": base64.StdEncoding.EncodeToString([]byte(`{"unknown":"payload"}`))}}
	if textField(rec, "name") != "" {
		t.Fatal("unrecognized blob leaked into visible text")
	}
}

func TestHouseholdReferencesUseOnlyExplicitHomeReaders(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer target-reader" {
			t.Error("reference used a different authority")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	resolver := householdResolver{ctx: context.Background(), t: &Tessera{internal: srv.URL, http: srv.Client()}, cache: map[string]*readRecord{}, sources: map[string]Source{
		"target": {Dataset: "target", Token: func(context.Context) (string, error) { return "target-reader", nil }},
	}}
	ref := objectReference{Dataset: "target", Type: "category", ID: "one"}
	if resolver.object("origin", ref) != nil || resolver.object("origin", ref) != nil {
		t.Fatal("unreadable reference was fabricated")
	}
	if calls != 1 {
		t.Fatal("unreadable reference was retried outside request cache")
	}
	ref.Dataset = "private"
	if resolver.object("origin", ref) != nil || calls != 1 {
		t.Fatal("unshared reference triggered a read")
	}
}

func TestHouseholdReadOnceAndSchemaConflict(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	fields := []Field{{ID: "timestamp", Type: "date_time"}, {ID: "value", Type: "integer", Annotations: map[string]string{"meaning": "additive"}}}
	typ := TypeInfo{ID: "track", Fields: fields, Dates: fields[:1]}
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":page"):
			reads++
			writeJSON(w, 200, map[string]any{"cursor": "one"})
		case strings.HasSuffix(r.URL.Path, ":next"):
			writeJSON(w, 200, recordSet{Meta: Meta{Fields: fields}, Records: []wireRecord{{ID: recordIdentity{ID: "one", Dataset: "a"}, Type: "track", Values: Row{{Type: "date_time", Value: now.Format(time.RFC3339)}, {Type: "integer", Value: 3}}}}})
		case r.Method == "DELETE":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected secondary metadata/record read %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	a := &app{tessera: &Tessera{internal: srv.URL, http: srv.Client()}}
	src := Source{Dataset: "a", Name: "Sara", Token: func(context.Context) (string, error) { return "reader", nil }}
	conflict := src
	conflict.Dataset = "b"
	conflict.Name = "Ben"
	changed := append([]Field(nil), fields...)
	changed[1] = Field{ID: "value", Type: "integer", Annotations: map[string]string{"meaning": "sample"}}
	cat := Catalog{Metadata: map[string][]Meta{"a": {{ID: "track", Fields: fields}}, "b": {{ID: "track", Fields: changed}}}}
	rows, states := a.readHouseholdTrack(context.Background(), []Source{src, src, conflict}, cat, typ, now.AddDate(0, -1, 0), now.Add(time.Hour))
	if reads != 1 || len(rows) != 1 || states["a"] != "Read" || states["b"] != "Different schema" {
		t.Fatalf("read contract: reads=%d rows=%d states=%v", reads, len(rows), states)
	}
}

func TestHouseholdMemberGapsAreNotZero(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	typ := TypeInfo{ID: "events"}
	row := readRecord{Dataset: "a", When: now, Values: map[string]any{}}
	window := exploreWindow{From: now.Add(-time.Hour), Bucket: "day"}
	series := viewSeries([]readRecord{row}, typ, time.UTC, window.From, false, "day")[0]
	parts := householdParts([]Source{{Dataset: "a", Name: "Sara"}, {Dataset: "b", Name: "Ben"}, {Dataset: "c", Name: "Lina"}}, []readRecord{row}, typ, series, map[string]string{"a": "Read", "b": "Read", "c": "Unavailable"}, exploreControls{}, time.UTC, window)
	if parts[0].Value != "1" || parts[0].State != "" || parts[1].Value != "—" || parts[1].State != "No values in this period" || parts[2].State != "Unavailable" {
		t.Fatalf("member gaps: %+v", parts)
	}
}

func TestHouseholdExpenseWithPetSubjectIsNotDoubleCounted(t *testing.T) {
	row := householdRow{Family: "expenses", SubjectFamily: "pets", SubjectKey: "milo"}
	for _, section := range []string{"overview", "expenses", "pets"} {
		if !matchHouseRow(row, url.Values{}, section) {
			t.Errorf("expense absent from %s", section)
		}
	}
	if matchHouseRow(row, url.Values{}, "house") || matchHouseRow(row, url.Values{"subject": {"different"}}, "pets") {
		t.Fatal("unrelated subject included")
	}
	if !homeInfrastructure("com.voptrack.home_copy") {
		t.Fatal("private copy markers shown as tracks")
	}
}

func TestHomeNavigationKeepsPeriodAndMember(t *testing.T) {
	q := url.Values{"period": {"year"}, "from": {"2026-01-01"}, "member": {"member-key"}, "q": {"food"}, "subject": {"pet-id"}, "spend": {"category-id"}, "bucket": {"2026-01"}, "offset": {"20"}, "currency": {"EUR"}, "type": {"private-type"}}
	links := homeNavigation(q)
	for _, section := range []string{"overview", "expenses", "pets", "house", "car", "other"} {
		u, err := url.Parse(links[section])
		if err != nil {
			t.Fatal(err)
		}
		got := u.Query()
		if u.Path != "/home" || got.Get("period") != "year" || got.Get("member") != "member-key" || got.Get("q") != "food" || got.Get("from") != "2026-01-01" {
			t.Fatalf("lost navigation context: %s", u)
		}
		for _, key := range []string{"subject", "spend", "bucket", "offset", "currency", "type"} {
			if got.Has(key) {
				t.Fatalf("carried view-specific state %s into %s", key, section)
			}
		}
	}
	if q.Get("subject") != "pet-id" {
		t.Fatal("navigation mutated current filters")
	}
}
