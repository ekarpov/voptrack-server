package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func agg(rows, values int64, sum, min, max any) Aggregate {
	return Aggregate{RowCount: json.Number(big.NewInt(rows).String()),
		ValueCount: json.Number(big.NewInt(values).String()), Sum: sum, Minimum: min, Maximum: max}
}

// The whole point of combining per-store answers is that the result equals
// what one store holding every row would have answered.
func TestCombiningStoresIsExact(t *testing.T) {
	var total Tally
	// Past float64's exact integer range: 2^53 + 1 twice.
	must(t, total.addAggregate(agg(1, 1, json.Number("9007199254740993"), json.Number("9007199254740993"), json.Number("9007199254740993"))))
	must(t, total.addAggregate(agg(1, 1, json.Number("9007199254740993"), json.Number("9007199254740993"), json.Number("9007199254740993"))))
	if got := total.Value(MeasureSum, "x").RatString(); got != "18014398509481986" {
		t.Fatalf("integer sum went through a float: %s", got)
	}
	// Fixed decimals arrive as strings and must stay exact: 0.1 + 0.2.
	var money Tally
	must(t, money.addAggregate(agg(1, 1, "0.1", "0.1", "0.1")))
	must(t, money.addAggregate(agg(1, 1, "0.2", "0.2", "0.2")))
	if got := display(money.Value(MeasureSum, "x"), 2); got != "0.3" {
		t.Fatalf("fixed sum lost exactness: %s", got)
	}
}

func TestAverageIsNotAnAverageOfAverages(t *testing.T) {
	var t2 Tally
	// Store A: three values summing to 30 (average 10). Store B: one value, 100.
	must(t, t2.addAggregate(agg(3, 3, json.Number("30"), json.Number("5"), json.Number("15"))))
	must(t, t2.addAggregate(agg(1, 1, json.Number("100"), json.Number("100"), json.Number("100"))))
	if got := t2.Value(MeasureAverage, "x").RatString(); got != "65/2" { // 130 / 4
		t.Fatalf("average = %s, want 130/4; averaging the averages would give 55", got)
	}
	if got := t2.Value(MeasureMinimum, "x").RatString(); got != "5" {
		t.Fatalf("minimum = %s", got)
	}
	if got := t2.Value(MeasureMaximum, "x").RatString(); got != "100" {
		t.Fatalf("maximum = %s", got)
	}
}

func TestNullIsNoValueNotZero(t *testing.T) {
	var t3 Tally
	must(t, t3.addAggregate(agg(4, 0, nil, nil, nil)))
	if t3.Value(MeasureSum, "x") != nil || t3.Value(MeasureAverage, "x") != nil || t3.Value(MeasureMinimum, "x") != nil {
		t.Fatal("rows with no values must answer null, never zero")
	}
	if got := t3.Value(MeasureCount, "").RatString(); got != "4" {
		t.Fatalf("counting records = %s, want 4", got)
	}
	if got := t3.Value(MeasureCount, "x").RatString(); got != "0" {
		t.Fatalf("counting values = %s, want 0", got)
	}
}

// A day ends where the household's clock says it does. 23:30 UTC on 30
// September is already 1 October in Helsinki.
func TestBucketsFollowTheTimezone(t *testing.T) {
	at := time.Date(2026, 9, 30, 23, 30, 0, 0, time.UTC)
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	if err != nil {
		t.Skip("no tz database")
	}
	if k, _ := bucket(at, "month", time.UTC); k != "2026-09" {
		t.Fatalf("UTC month = %s", k)
	}
	if k, _ := bucket(at, "month", helsinki); k != "2026-10" {
		t.Fatalf("Helsinki month = %s", k)
	}
	if k, _ := bucket(at, "day", helsinki); k != "2026-10-01" {
		t.Fatalf("Helsinki day = %s", k)
	}
}

func TestWeeksStartOnMonday(t *testing.T) {
	sunday := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ks, ls := bucket(sunday, "week", time.UTC)
	km, lm := bucket(monday, "week", time.UTC)
	if ks != "2026-09-21" || ls != "2026-W39" {
		t.Fatalf("Sunday 27 Sep in week %s (%s), want 2026-09-21 (W39)", ks, ls)
	}
	if km != "2026-09-28" || lm != "2026-W40" {
		t.Fatalf("Monday 28 Sep in week %s (%s), want 2026-09-28 (W40)", km, lm)
	}
	keys := []string{km, ks}
	sort.Strings(keys)
	if keys[0] != ks {
		t.Fatal("week keys must sort chronologically")
	}
}

// Two references agreeing on object_id in different types are different
// groups; a Console keying on object_id alone would merge them.
func TestReferenceGroupsUseTheWholeIdentity(t *testing.T) {
	a, _ := groupLabel(json.RawMessage(`{"meta_id":"m.person","dataset_id":"d1","object_id":"7"}`))
	b, _ := groupLabel(json.RawMessage(`{"meta_id":"m.pet","dataset_id":"d1","object_id":"7"}`))
	c, _ := groupLabel(json.RawMessage(`{"meta_id":"m.person","dataset_id":"d2","object_id":"7"}`))
	if a == b || a == c || b == c {
		t.Fatalf("distinct references collapsed: %s %s %s", a, b, c)
	}
	if k, _ := groupLabel(json.RawMessage(`null`)); k != "null" {
		t.Fatalf("null group key = %s", k)
	}
	if k, l := groupLabel(json.RawMessage(`"groceries"`)); k != "text:groceries" || l != "groceries" {
		t.Fatalf("text group = %s %s", k, l)
	}
}

func TestEveryLanguageHasEveryKey(t *testing.T) {
	for key := range messages["en"] {
		for _, lang := range languages {
			if _, ok := messages[lang][key]; !ok {
				t.Errorf("%s is missing %q", lang, key)
			}
		}
	}
	for _, lang := range languages {
		for key := range messages[lang] {
			if _, ok := messages["en"][key]; !ok {
				t.Errorf("%s has %q, which English does not", lang, key)
			}
		}
	}
}

func TestCSVCellsCannotBecomeFormulas(t *testing.T) {
	for _, in := range []string{"=HYPERLINK(\"x\")", "+1", "-1", "@SUM(A1)", "\tx"} {
		if out := cell(in); !strings.HasPrefix(out, "'") {
			t.Errorf("cell(%q) = %q, would run as a formula", in, out)
		}
	}
	if cell("groceries") != "groceries" {
		t.Error("ordinary text must be left alone")
	}
}

func TestAGapIsNotZero(t *testing.T) {
	res := Result{Spec: Spec{Measure: MeasureSum, Field: "x"}}
	var jan, mar Tally
	jan.addValue(big.NewRat(10, 1))
	mar.addValue(big.NewRat(30, 1))
	res.Points = []Point{{Label: "2026-01", Tally: jan}, {Label: "2026-02"}, {Label: "2026-03", Tally: mar}}
	c := buildChart(res, true)
	if len(c.Dots) != 2 {
		t.Fatalf("dots = %d, want 2: February has no data and must not be drawn at zero", len(c.Dots))
	}
	if strings.Count(c.Path, "M") != 2 {
		t.Fatalf("path %q: the line must break across the empty month", c.Path)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSavedDashboards(t *testing.T) {
	s := &savedStore{docs: fileDocs{dir: t.TempDir()}}
	for _, bad := range []string{"../etc/passwd", "acct-../../x", "home/..", "", "acct-"} {
		if _, err := s.path(bad); err == nil {
			t.Errorf("key %q accepted; it could reach outside the directory", bad)
		}
	}
	panel, _, err := cleanPanel("scope=mine&type=org.x&measure=sum&field=amount&evil=1")
	if err != nil || strings.Contains(panel, "evil") {
		t.Fatalf("cleanPanel kept an unknown parameter: %q %v", panel, err)
	}
	id, err := s.addPanel("acct-aaaaaaaaaaaaaaaaaaaaaaaa", "Money", panel)
	must(t, err)
	again, err := s.addPanel("acct-aaaaaaaaaaaaaaaaaaaaaaaa", "money", panel) // same name, any case; same panel
	must(t, err)
	if again != id {
		t.Fatal("saving the same panel to the same dashboard made a second one")
	}
	if d, _ := s.find("acct-aaaaaaaaaaaaaaaaaaaaaaaa", id); len(d.Panels) != 1 {
		t.Fatalf("saving twice gave %d panels", len(d.Panels))
	}
	if _, ok := s.find("acct-bbbbbbbbbbbbbbbbbbbbbbbb", id); ok {
		t.Fatal("one member's dashboard is visible under another's key")
	}
	if _, err := s.addPanel("acct-aaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("x", 81), panel); err == nil {
		t.Fatal("an 81-character name was accepted")
	}
	must(t, s.removePanel("acct-aaaaaaaaaaaaaaaaaaaaaaaa", id, 0))
	if _, ok := s.find("acct-aaaaaaaaaaaaaaaaaaaaaaaa", id); ok {
		t.Fatal("a dashboard with no panels left must be gone")
	}
	for i := 0; i < maxPanels; i++ {
		p, _, _ := cleanPanel("scope=mine&type=org.x&from=2026-01-" + fmt.Sprintf("%02d", i+1))
		if _, err := s.addPanel("acct-aaaaaaaaaaaaaaaaaaaaaaaa", "Full", p); err != nil {
			t.Fatal(err)
		}
	}
	p, _, _ := cleanPanel("scope=mine&type=org.y")
	if _, err := s.addPanel("acct-aaaaaaaaaaaaaaaaaaaaaaaa", "Full", p); !errors.Is(err, errFull) {
		t.Fatalf("panel %d accepted, want errFull: %v", maxPanels+1, err)
	}
}

func TestConnectLinkIsAnOriginAndPortable(t *testing.T) {
	link, err := connectLink("https://home.example.org:7443/some/path?x=1#f", "The Rivers", "", "")
	must(t, err)
	want := "voptrack://connect?name=The%20Rivers&server=https%3A%2F%2Fhome.example.org%3A7443"
	if link != want {
		t.Fatalf("link = %s\nwant   %s", link, want)
	}
	if strings.Contains(link, "+") {
		t.Fatal("a space encoded as + arrives in the app as a literal plus")
	}
	for _, bad := range []string{"http://home.example.org", "ftp://x", "https://"} {
		if _, err := connectLink(bad, "", "", ""); err == nil {
			t.Errorf("connectLink(%q) accepted a non-https origin", bad)
		}
	}
	if l, _ := connectLink("https://h.example", "", "", ""); strings.Contains(l, "name=") {
		t.Error("an empty name must be left out, not sent empty")
	}
}

func TestConsentVersionsNeverChange(t *testing.T) {
	dir := t.TempDir()
	s := &studyStore{docs: fileDocs{dir: dir}, dir: dir}
	for _, bad := range []string{"../x", "A", "a", "has space", strings.Repeat("a", 64)} {
		if _, err := s.save(bad, StudyEdit{Title: "x"}, true); err == nil {
			t.Errorf("slug %q accepted", bad)
		}
	}
	_, err := s.save("sleep", StudyEdit{Title: "Sleep"}, true)
	must(t, err)
	if _, err := s.save("sleep", StudyEdit{Title: "Again"}, true); err == nil {
		t.Fatal("creating a study at a taken address succeeded")
	}
	// Opening without what a person must read first is refused.
	if _, err := s.save("sleep", StudyEdit{Title: "Sleep", Status: "open", ConsentText: "I agree."}, false); err == nil {
		t.Fatal("a study opened without summary, what is collected, who sees it and how to leave")
	}
	full := StudyEdit{Title: "Sleep", Summary: "s", Collected: "c", WhoSees: "w", HowToLeave: "h", ConsentText: "Version one.",
		Status: "open"}
	if _, err := s.save("sleep", full, false); err == nil || !strings.Contains(err.Error(), "track") {
		t.Fatalf("a study opened without tracks: %v", err)
	}
	_, err = s.saveTracks("sleep", []TrackDesign{{TypeID: "study.sleep.sleep", Name: "Sleep",
		Fields: []TrackField{{ID: "hours", Name: "Hours", Kind: KindNumber, Unit: "h"}}}})
	must(t, err)
	if _, err := s.save("sleep", full, false); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("a study opened without an issuer: %v", err)
	}
	// The order the Console follows: save everything as a draft, create the
	// issuer from the saved tracks, then open.
	draft := full
	draft.Status = ""
	_, err = s.save("sleep", draft, false)
	must(t, err)
	must(t, s.setIssuer("sleep", "study-sleep", "secret", 50))
	st, err := s.save("sleep", full, false)
	must(t, err)
	if st.Status != "open" || len(st.Consent) != 1 {
		t.Fatalf("status %s, %d consent versions", st.Status, len(st.Consent))
	}
	first := st.Consent[0]
	// Saving the same text does not publish a version.
	st, _ = s.save("sleep", full, false)
	if len(st.Consent) != 1 {
		t.Fatalf("saving unchanged consent text published version %d", len(st.Consent))
	}
	// Changing it publishes version 2 and leaves version 1 exactly as it was.
	full.ConsentText = "Version two."
	st, _ = s.save("sleep", full, false)
	if len(st.Consent) != 2 || st.Consent[1].Version != 2 || st.Consent[1].Text != "Version two." {
		t.Fatalf("consent after an edit: %+v", st.Consent)
	}
	if st.Consent[0] != first {
		t.Fatalf("version 1 changed: %+v, was %+v", st.Consent[0], first)
	}
	if cur, _ := st.Current(); cur.Version != 2 {
		t.Fatalf("current consent is version %d, want 2", cur.Version)
	}
}

func TestStudyTracksAndCodes(t *testing.T) {
	dir := t.TempDir()
	s := &studyStore{docs: fileDocs{dir: dir}, dir: dir}
	_, err := s.save("sm", StudyEdit{Title: "SM"}, true)
	must(t, err)
	must(t, s.setIssuer("sm", "study-sm", "shh", 10))
	if _, err := s.saveTracks("sm", []TrackDesign{{Name: "Sleep"}}); err == nil {
		t.Fatal("tracks changed after the study's issuer existed")
	}
	if _, err := s.save("sm", StudyEdit{Title: "SM", Status: "draft"}, false); err == nil {
		t.Fatal("an opened study went back to draft")
	}
	if sec, _ := s.issuerSecret("sm"); sec != "shh" {
		t.Fatalf("issuer secret = %q", sec)
	}
	code, err := s.newCode("sm")
	must(t, err)
	if !s.hasCode("sm", code) || !s.spendCode("sm", code) || s.spendCode("sm", code) || s.hasCode("sm", code) {
		t.Fatal("an invitation code must work exactly once")
	}
	if len(s.list()) != 1 {
		t.Fatalf("list() = %d studies; a study's side files must not count as studies", len(s.list()))
	}
}

func TestErasedFromApp(t *testing.T) {
	none := func(string) ([]Meta, error) { return nil, nil }
	some := func(string) ([]Meta, error) { return []Meta{{}}, nil }
	broken := func(string) ([]Meta, error) { return nil, errors.New("unreachable") }
	cases := []struct {
		name         string
		reader, mine []string
		types        func(string) ([]Meta, error)
		want         bool
	}{
		{"the reader holds none of theirs", []string{"other"}, []string{"new"}, some, true},
		{"they hold nothing at all", []string{"other"}, nil, some, true},
		{"read, with tracks", []string{"d"}, []string{"d"}, some, false},
		{"read, emptied", []string{"d"}, []string{"d"}, none, true},
		{"read, unreadable", []string{"d"}, []string{"d"}, broken, false},
		{"an old one read with tracks, a new one not", []string{"d"}, []string{"d", "new"}, some, false},
	}
	for _, c := range cases {
		if got := erasedFromApp(c.reader, c.mine, c.types); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestResearchVectors: the Console derives, for each design, exactly the
// schema the app's composer does. The vectors are the app's own output.
func TestResearchVectors(t *testing.T) {
	canon := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var back any
		_ = json.Unmarshal(raw, &back)
		out, _ := json.Marshal(back)
		return string(out)
	}
	files, _ := filepath.Glob("testdata/research-vectors/*.json")
	if len(files) < 8 {
		t.Fatalf("found %d vectors, want the app's 8", len(files))
	}
	var sleep TrackDesign
	for _, f := range files {
		if base := filepath.Base(f); base == "store.json" || base == "subject.json" || base == "source.json" {
			continue
		}
		raw, _ := os.ReadFile(f)
		var v struct {
			Described struct {
				TypeID string       `json:"typeID"`
				Name   string       `json:"name"`
				Fields []TrackField `json:"fields"`
			} `json:"described"`
			Schema json.RawMessage `json:"schema"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		d := TrackDesign{TypeID: v.Described.TypeID, Name: v.Described.Name, Fields: v.Described.Fields}
		if err := d.validate(); err != nil {
			t.Errorf("%s: the app's own design is refused: %v", f, err)
		}
		got, err := d.schema()
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var want any
		_ = json.Unmarshal(v.Schema, &want)
		if canon(got) != canon(want) {
			t.Errorf("%s:\n got %s\nwant %s", f, canon(got), canon(want))
		}
		if filepath.Base(f) == "study-sleep.json" {
			sleep = d
		}
	}
	raw, _ := os.ReadFile("testdata/research-vectors/store.json")
	var want any
	_ = json.Unmarshal(raw, &want)
	got, err := studyTypes([]TrackDesign{sleep, {Catalogue: "com.voptrack.track.weight"}})
	if err != nil {
		t.Fatal(err)
	}
	// Subject and Source come first; store.json begins at the root.
	if canon(got[2:]) != canon(want) {
		t.Errorf("the whole store differs:\n got %s\nwant %s", canon(got[2:]), canon(want))
	}
	for i, name := range []string{"subject.json", "source.json"} {
		raw, err := os.ReadFile("testdata/research-vectors/" + name)
		must(t, err)
		var want any
		_ = json.Unmarshal(raw, &want)
		if canon(got[i]) != canon(want) {
			t.Errorf("%s differs:\n got %s\nwant %s", name, canon(got[i]), canon(want))
		}
	}
}

func TestStudyID(t *testing.T) {
	for slug, want := range map[string]string{"sleep-mood": "sleep_mood", "2027-sleep": "s2027_sleep", "diet": "diet"} {
		if got := studyID(slug); got != want {
			t.Errorf("studyID(%q) = %q, want %q", slug, got, want)
		}
	}
}

func TestDesignForm(t *testing.T) {
	form := url.Values{
		"t0.present": {"1"}, "t0.name": {"Sleep"},
		"t0.f0.present": {"1"}, "t0.f0.name": {"Hours slept"}, "t0.f0.kind": {"number"}, "t0.f0.unit": {"h"}, "t0.f0.meaning": {"sample"},
		"t0.f1.present": {"1"}, "t0.f1.name": {"Gone"}, "t0.f1.kind": {"text"}, "t0.f1.remove": {"1"},
		"t0.f2.present": {"1"}, "t0.f2.name": {""},
		"t1.present": {"1"}, "t1.name": {""}, "t1.f0.present": {"1"}, "t1.f0.name": {""},
		"catalogue": {"com.voptrack.track.weight", "com.example.not.offered"},
	}
	got := parseDesign(form, "s2027_sleep")
	if len(got) != 2 || got[0].TypeID != "study.s2027_sleep.sleep" || len(got[0].Fields) != 1 ||
		got[0].Fields[0] != (TrackField{ID: "hours_slept", Name: "Hours slept", Kind: KindNumber, Unit: "h", Meaning: "sample"}) ||
		got[1].Catalogue != "com.voptrack.track.weight" {
		t.Fatalf("parsed %+v", got)
	}
	if _, err := studyTypes(got); err != nil {
		t.Fatalf("a valid design refused: %v", err)
	}
	for _, bad := range []TrackDesign{
		{Name: "T", Fields: []TrackField{{ID: "a", Name: "A", Kind: KindText, Unit: "kg"}}},
		{Name: "T", Fields: []TrackField{{ID: "a", Name: "A", Kind: KindNumber, Meaning: "average"}}},
		{Name: "T", Fields: []TrackField{{ID: "a", Name: "A", Kind: "decimal"}}},
		{Name: "T", Fields: []TrackField{{ID: "a", Name: "A", Kind: KindNumber, Unit: "kg"}, {ID: "a_unit", Name: "A unit", Kind: KindText}}},
		{Name: "T"},
	} {
		if err := bad.validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestProjection(t *testing.T) {
	got := []struct {
		ID string `json:"id"`
	}{{"amount"}, {"when"}}
	order, err := projection([]string{"when", "amount"}, got)
	if err != nil || order[0] != 1 || order[1] != 0 {
		t.Fatalf("projection = %v, %v; want [1 0]", order, err)
	}
	if _, err := projection([]string{"missing"}, got); err == nil {
		t.Fatal("a field the server did not return was accepted")
	}
}

// TestRoutes: every route pattern is one the standard mux accepts. A bad one
// panics when the Console starts, which no handler test would reach.
func TestRoutes(t *testing.T) {
	(&app{}).routes()
}

// failingDocs saves nothing: every put fails.
type failingDocs struct{ fileDocs }

func (failingDocs) put(string, []byte) error { return errors.New("disk full") }

// TestSpendCodeNeedsSaving: a join code whose use could not be recorded does
// not work, or it would work again.
func TestSpendCodeNeedsSaving(t *testing.T) {
	dir := t.TempDir()
	good := &studyStore{docs: fileDocs{dir: dir}, dir: dir}
	code, err := good.newCode("sm")
	must(t, err)
	broken := &studyStore{docs: failingDocs{fileDocs{dir: dir}}, dir: dir}
	if broken.spendCode("sm", code) {
		t.Fatal("a code was accepted although its use could not be saved")
	}
	if !good.spendCode("sm", code) || good.spendCode("sm", code) {
		t.Fatal("a code must work once, then never")
	}
}

// TestSessionBounds: anonymous sessions are capped and short-lived, and a
// session holds only a few unfinished sign-ins.
func TestSessionBounds(t *testing.T) {
	s := &sessions{byID: map[string]*session{}, idle: 12 * time.Hour, anonIdle: 30 * time.Minute, max: 3}
	for i := 0; i < 3; i++ {
		if s.get(httptest.NewRecorder(), httptest.NewRequest("GET", "/login", nil), true) == nil {
			t.Fatalf("session %d refused below the cap", i)
		}
	}
	if s.get(httptest.NewRecorder(), httptest.NewRequest("GET", "/login", nil), true) != nil {
		t.Fatal("a session was made beyond the cap")
	}
	s.sweep(time.Now().Add(31 * time.Minute))
	if len(s.byID) != 0 {
		t.Fatalf("anonymous sessions outlived their half hour: %d left", len(s.byID))
	}
	sess := &session{Pending: map[string]pending{}}
	for i := 0; i < 10; i++ {
		s.addPending(sess, fmt.Sprint(i), pending{Expires: time.Now().Add(time.Duration(i) * time.Minute)})
	}
	if len(sess.Pending) != maxPending {
		t.Fatalf("%d sign-ins pending, want at most %d", len(sess.Pending), maxPending)
	}
}

// TestTokenFetchDoesNotBlockOthers: while one store's token is being fetched
// (here: a server that never answers), another store's cached token is
// handed out at once.
func TestTokenFetchDoesNotBlockOthers(t *testing.T) {
	release := make(chan struct{})
	stuck := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer stuck.Close()
	defer close(release) // before Close, which waits for the handler
	secret := filepath.Join(t.TempDir(), "secret")
	must(t, os.WriteFile(secret, []byte("s"), 0o600))
	c := newServiceTokens("home", secret)
	c.slot("cached").tok = tokens{Access: "a", Expires: time.Now().Add(time.Hour)}
	tess := &Tessera{internal: stuck.URL, http: stuck.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.token(ctx, tess, "slow")
	time.Sleep(50 * time.Millisecond) // the slow fetch now holds its own lock
	done := make(chan string, 1)
	go func() { tok, _ := c.token(context.Background(), tess, "cached"); done <- tok }()
	select {
	case tok := <-done:
		if tok != "a" {
			t.Fatalf("cached token %q, want a", tok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cached token waited behind another store's fetch")
	}
}

// TestChartBounds: a line's path is built without re-copying it per point, and
// a chart with more points than pixels is not drawn at all.
func TestChartBounds(t *testing.T) {
	series := func(n int) Result {
		res := Result{Spec: Spec{Measure: MeasureCount}}
		for i := 0; i < n; i++ {
			var tally Tally
			tally.addValue(big.NewRat(int64(i%7), 1))
			res.Points = append(res.Points, Point{Label: fmt.Sprint(i), Tally: tally})
		}
		return res
	}
	if c := buildChart(series(maxChartPoints+1), true); !c.TooMany || c.Path != "" {
		t.Fatalf("%d points were drawn", maxChartPoints+1)
	}
	// Bytes, not allocations: re-copying the path per point allocates once per
	// point either way, but the bytes grow with the square of the points.
	res := series(maxChartPoints)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	buildChart(res, true)
	runtime.ReadMemStats(&after)
	if used := after.TotalAlloc - before.TotalAlloc; used > 1<<20 {
		t.Fatalf("drawing %d points allocated %d bytes", maxChartPoints, used)
	}
}

func TestConnectLinkProduct(t *testing.T) {
	link, err := connectLink("https://home.example.org", "The Rivers", productOf("household"), "")
	if err != nil || link != "voptrack://connect?name=The%20Rivers&product=home&server=https%3A%2F%2Fhome.example.org" {
		t.Fatalf("household link %q, %v", link, err)
	}
	if productOf("research") != "study" || productOf("standard") != "" {
		t.Fatal("research is study, and a personal server names no product")
	}
}

// TestHomeVectors: the Home aggregation vectors the app imports
// (specs/vectors/home-aggregation-v1.json) are exactly what the Console's own
// arithmetic answers, so the app is checked against the reference, not
// against a copy that could drift from it.
func TestHomeVectors(t *testing.T) {
	raw, err := os.ReadFile("../../specs/vectors/home-aggregation-v1.json")
	must(t, err)
	var doc struct {
		Members map[string]struct {
			Shares  []string                       `json:"shares"`
			Records map[string][]map[string]string `json:"records"`
		} `json:"members"`
		Cases []struct {
			Name, Scope, Type, Field, Measure, Group string
			From                                     string             `json:"from"`
			To                                       string             `json:"to"`
			Expect                                   map[string]*string `json:"expect"`
		} `json:"cases"`
	}
	must(t, json.Unmarshal(raw, &doc))
	if len(doc.Cases) < 8 {
		t.Fatalf("only %d cases", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		var members []string
		for name, m := range doc.Members {
			switch {
			case c.Scope == "home" && slices.Contains(m.Shares, c.Type):
				members = append(members, name)
			case c.Scope == "mine:"+name:
				members = append(members, name)
			}
		}
		if c.Expect == nil {
			if len(members) != 0 {
				t.Errorf("%s: the track is shared, but the vector says it is absent", c.Name)
			}
			continue
		}
		groups := map[string]*Tally{}
		if c.Group == "month" {
			from, _ := time.Parse("2006-01-02", c.From)
			to, _ := time.Parse("2006-01-02", c.To)
			for m := from; !m.After(to); m = m.AddDate(0, 1, 0) {
				groups[m.Format("2006-01")] = &Tally{}
			}
		}
		for _, name := range members {
			for _, rec := range doc.Members[name].Records[c.Type] {
				key := "All"
				switch c.Group {
				case "member":
					key = name
				case "category":
					key = rec["category"]
				case "month":
					at, err := time.Parse(time.RFC3339Nano, rec["recorded_at"])
					must(t, err)
					key, _ = bucket(at, "month", time.UTC) // the Console's own bucketing
				}
				if groups[key] == nil {
					groups[key] = &Tally{}
				}
				v, err := rat(rec[c.Field])
				must(t, err)
				groups[key].addValue(v)
			}
		}
		for key, want := range c.Expect {
			tally := groups[key]
			var got *string
			if tally != nil && tally.Rows > 0 {
				s := exact(tally.Value(Measure(c.Measure), c.Field))
				got = &s
			}
			switch {
			case want == nil && got != nil:
				t.Errorf("%s / %s: a gap in the vector, but the Console answers %s", c.Name, key, *got)
			case want != nil && got == nil:
				t.Errorf("%s / %s: the vector says %s, the Console answers a gap", c.Name, key, *want)
			case want != nil && *got != *want:
				t.Errorf("%s / %s: the vector says %s, the Console answers %s", c.Name, key, *want, *got)
			}
		}
		if len(groups) != len(c.Expect) {
			t.Errorf("%s: the Console has groups %v, the vector %v", c.Name, len(groups), len(c.Expect))
		}
	}
}

func TestStoreUsage(t *testing.T) {
	u := StoreUsage{Bytes: 214748364, MaxBytes: 268435456}
	if u.Percent() != 79 || (StoreUsage{Bytes: 5}).Percent() != 0 {
		t.Fatalf("percent %d", u.Percent())
	}
	if sizeText(1023) != "1.0 KiB" || sizeText(268435456) != "256.0 MiB" || sizeText(3<<30) != "3.0 GiB" {
		t.Fatalf("sizes %s %s %s", sizeText(1023), sizeText(268435456), sizeText(3<<30))
	}
}

func TestDistribution(t *testing.T) {
	r := func(n int64) *big.Rat { return big.NewRat(n, 1) }
	few := distribution([]*big.Rat{r(3), r(1), r(3), r(5)})
	if len(few) != 3 || few[0].Label != "1" || few[1].Tally.Rows != 2 || few[2].Label != "5" {
		t.Fatalf("few values: %+v", few)
	}
	var many []*big.Rat
	for i := int64(0); i <= 100; i++ {
		many = append(many, r(i))
	}
	bars := distribution(many)
	total := int64(0)
	for _, b := range bars {
		total += b.Tally.Rows
	}
	if len(bars) != rangeBars || total != 101 || bars[0].Label != "0–10" || bars[9].Tally.Rows != 11 {
		t.Fatalf("ranges: %d bars, %d values, first %q, last holds %d", len(bars), total, bars[0].Label, bars[9].Tally.Rows)
	}
	if distribution(nil) != nil {
		t.Fatal("no values should draw nothing")
	}
}

func TestMaterialConsentCoverage(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var legacy ConsentVersion
	must(t, json.Unmarshal([]byte(`{"version":1,"text":"First"}`), &legacy))
	if legacy.Editorial {
		t.Fatal("legacy consent must be material")
	}
	st := Study{Consent: []ConsentVersion{{Version: 1, Published: at}, {Version: 2, Editorial: true, Published: at.Add(time.Hour)}, {Version: 3, Published: at.Add(2 * time.Hour)}, {Version: 4, Editorial: true, Published: at.Add(3 * time.Hour)}}}
	h := agreementHistory{consents: []ConsentRecord{{Version: 1, At: at}}}
	for _, tc := range []struct {
		hour    int
		missing bool
	}{{0, false}, {1, false}, {2, true}, {3, true}} {
		if got := h.missing(st, at.Add(time.Duration(tc.hour)*time.Hour)); got != tc.missing {
			t.Errorf("hour %d: missing %v", tc.hour, got)
		}
	}
	if st.LatestMaterial() != 3 {
		t.Fatal("editorial version changed agreement requirement")
	}
	h.consents = append(h.consents, ConsentRecord{Version: 4, At: at.Add(4 * time.Hour)})
	if h.missing(st, at.Add(4*time.Hour)) {
		t.Fatal("current agreement does not cover material predecessor")
	}
	if !h.missing(st, at.Add(-time.Hour)) || !h.missing(st, time.Time{}) {
		t.Fatal("records without a dated agreement must be flagged")
	}
}

func TestRejoinAndRepeatedWithdrawal(t *testing.T) {
	s := &studyStore{docs: fileDocs{dir: t.TempDir()}}
	at := time.Now().UTC().Add(-time.Hour)
	must(t, s.recordConsent("sm", ConsentRecord{Account: "person", Version: 1, At: at}))
	must(t, s.recordWithdrawal("sm", Withdrawal{Account: "person", At: at.Add(time.Minute)}))
	st := Study{Consent: []ConsentVersion{{Version: 1, Published: at}}}
	if !s.histories("sm")["person"].missing(st, at.Add(2*time.Minute)) {
		t.Fatal("withdrawal must require agreement")
	}
	must(t, s.recordConsent("sm", ConsentRecord{Account: "person", Version: 1, At: at.Add(3 * time.Minute)}))
	if s.histories("sm")["person"].missing(st, at.Add(4*time.Minute)) || len(s.withdrawals("sm")) != 0 {
		t.Fatal("rejoining must clear current withdrawal")
	}
	must(t, s.recordWithdrawal("sm", Withdrawal{Account: "person", At: at.Add(5 * time.Minute)}))
	if !s.histories("sm")["person"].missing(st, at.Add(6*time.Minute)) || len(s.withdrawalHistory("sm")) != 2 {
		t.Fatal("second withdrawal lost")
	}
}

func TestEditorialSaveAndRunBy(t *testing.T) {
	s := &studyStore{docs: fileDocs{dir: t.TempDir()}}
	edit := StudyEdit{Title: "Study", ConsentText: "First", Editorial: true, RunBy: strings.Repeat("ä", 120)}
	st, err := s.save("sm", edit, true)
	must(t, err)
	if st.Consent[0].Editorial {
		t.Fatal("first version must be material")
	}
	edit.ConsentText = "Second"
	st, err = s.save("sm", edit, false)
	must(t, err)
	if !st.Consent[1].Editorial || st.LatestMaterial() != 1 {
		t.Fatal("editorial edit requires agreement")
	}
	edit.RunBy += "ä"
	if _, err = s.save("sm", edit, false); err == nil {
		t.Fatal("run_by longer than 120 characters accepted")
	}
}

func TestUsedCodeAPI(t *testing.T) {
	s := &studyStore{docs: fileDocs{dir: t.TempDir()}}
	st, err := s.save("sm", StudyEdit{Title: "Study", ConsentText: "First"}, true)
	must(t, err)
	st.Status = "open"
	raw, _ := json.Marshal(st)
	must(t, s.docs.put("studies/sm.json", raw))
	code, err := s.newCode("sm")
	must(t, err)
	if !s.spendCode("sm", code) {
		t.Fatal("code could not be spent")
	}
	a := &app{studies: s}
	a.cfg.Variant = "research"
	for _, tc := range []struct{ code, want string }{{code, "code_used"}, {"unknown", "invite_only"}, {"", "invite_only"}} {
		body, _ := json.Marshal(map[string]any{"version": 1, "code": tc.code})
		r := httptest.NewRequest("POST", "/api/v1/studies/sm/join", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("slug", "sm")
		w := httptest.NewRecorder()
		a.apiJoin(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("expected %s refusal", tc.want)
		}
	}
	// A failed active-list removal must still leave the spent marker authoritative.
	path, _ := s.sidecar("sm", "codes")
	must(t, s.writeList(path, []string{code}))
	if s.hasCode("sm", code) || s.spendCode("sm", code) {
		t.Fatal("spent marker did not prevent reuse")
	}
}

func TestConsentAPIReagreement(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/account" || r.Header.Get("Authorization") != "Bearer own" {
			w.WriteHeader(401)
			return
		}
		writeJSON(w, 200, map[string]string{"account": "person"})
	}))
	defer remote.Close()
	s := &studyStore{docs: fileDocs{dir: t.TempDir()}}
	st, err := s.save("sm", StudyEdit{Title: "Study", ConsentText: "First"}, true)
	must(t, err)
	st.Status, st.IssuerID = "open", "issuer"
	st.Consent = append(st.Consent, ConsentVersion{Version: 2, Published: time.Now().Add(-time.Minute)})
	save := func() { raw, _ := json.Marshal(st); must(t, s.docs.put("studies/sm.json", raw)) }
	save()
	must(t, s.recordConsent("sm", ConsentRecord{Account: "person", Version: 1, At: time.Now().Add(-time.Hour)}))
	a := &app{studies: s, tessera: &Tessera{internal: remote.URL, http: remote.Client()}}
	a.cfg.Variant = "research"
	call := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/studies/sm/consent", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer own")
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("slug", "sm")
		w := httptest.NewRecorder()
		a.apiConsent(w, r)
		return w
	}
	refuse := func(version int, code string) {
		before := len(s.consents("sm"))
		w := call("POST", fmt.Sprintf(`{"version":%d}`, version))
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) || len(s.consents("sm")) != before {
			t.Fatalf("expected %s without recording agreement", code)
		}
	}
	refuse(1, "stale_version")
	for i := 0; i < 3; i++ {
		w := call("POST", `{"version":2}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"agreement_needed":false`) || len(s.consents("sm")) != 2 {
			t.Fatal("repeated agreement must write exactly one new record")
		}
		if w.Body.String() != call("GET", "").Body.String() {
			t.Fatal("POST must answer with the GET body")
		}
	}
	st.Consent = append(st.Consent, ConsentVersion{Version: 3, Editorial: true, Published: time.Now()})
	save()
	refuse(2, "stale_version") // Stale wins even though the agreement covers this editorial edit.
	w := call("POST", `{"version":3}`)
	if w.Code != 200 || len(s.consents("sm")) != 2 {
		t.Fatal("covered editorial version must not write agreement")
	}
	st.Consent = append(st.Consent, ConsentVersion{Version: 4, Published: time.Now()})
	save()
	w = call("POST", `{"version":4}`)
	if w.Code != 200 || len(s.consents("sm")) != 3 {
		t.Fatal("material change must write a new agreement")
	}
	w = call("POST", `{"version":4}`)
	if w.Code != 200 || len(s.consents("sm")) != 3 {
		t.Fatal("material agreement retry must not write")
	}
	w = call("POST", strings.Repeat(" ", 4097)+`{"version":4}`)
	if w.Code != http.StatusBadRequest {
		t.Fatal("oversized agreement body accepted")
	}
	r := httptest.NewRequest("POST", "/api/v1/studies/sm/consent", strings.NewReader(`{"version":4}`))
	r.SetPathValue("slug", "sm")
	r.Header.Set("Authorization", "Bearer own")
	r.Header.Set("Content-Type", "application/json-not-really")
	w = httptest.NewRecorder()
	a.apiConsent(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatal("non-JSON agreement body accepted")
	}
	st.Status = "closed"
	save()
	refuse(4, "study_closed")
	refuse(3, "study_closed")
	w = call("GET", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"closed"`) {
		t.Fatal("closed study GET must remain available")
	}
	must(t, s.recordWithdrawal("sm", Withdrawal{Account: "person", At: time.Now()}))
	refuse(4, "study_closed") // Closed wins over withdrawn.
	st.Status = "open"
	save()
	refuse(3, "stale_version") // Stale is refused before any rejoining is tried.
	// Rejoining must unfreeze first; this fake Tessera has no store to
	// unfreeze, so nothing is recorded and the participant stays withdrawn.
	before := len(s.consents("sm"))
	if w := call("POST", `{"version":4}`); w.Code == 200 || len(s.consents("sm")) != before {
		t.Fatal("rejoining recorded agreement without unfreezing the study's reader")
	}
	w = call("GET", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"agreement_needed":true`) {
		t.Fatal("a failed rejoin must remain withdrawn")
	}
	must(t, s.recordConsent("sm", ConsentRecord{Account: "person", Version: 4, At: time.Now()}))
	st.Consent = append(st.Consent, ConsentVersion{Version: 5, Published: time.Now()})
	save()
	before = len(s.consents("sm"))
	s.docs = failingDocs{s.docs.(fileDocs)}
	w = call("POST", `{"version":5}`)
	if w.Code != http.StatusBadGateway || len(s.consents("sm")) != before {
		t.Fatal("failed agreement write was accepted")
	}
}

// failCodeRemoval allows the spent marker to persist, then fails removal of
// the active code. This is the partial-write case, including after restart.
type failCodeRemoval struct{ fileDocs }

func (d failCodeRemoval) put(name string, raw []byte) error {
	if strings.HasSuffix(name, ".codes.json") {
		return errors.New("disk full")
	}
	return d.fileDocs.put(name, raw)
}

func TestSpentCodeSurvivesPartialWrite(t *testing.T) {
	dir := t.TempDir()
	s := &studyStore{docs: fileDocs{dir: dir}}
	code, err := s.newCode("sm")
	must(t, err)
	broken := &studyStore{docs: failCodeRemoval{fileDocs{dir: dir}}}
	if broken.spendCode("sm", code) {
		t.Fatal("failed removal must fail closed")
	}
	restarted := &studyStore{docs: fileDocs{dir: dir}}
	if !restarted.codeUsed("sm", code) || restarted.hasCode("sm", code) || restarted.spendCode("sm", code) {
		t.Fatal("spent marker must survive a failed removal and restart")
	}
	spent, _ := s.sidecar("sm", "codes-spent")
	must(t, s.docs.put(spent, []byte(`{"invalid":"sidecar"}`)))
	if s.spendCode("sm", code) || s.hasCode("sm", code) {
		t.Fatal("unreadable spent history must fail closed")
	}
}

// TestWithdrawAndRejoin: withdrawing freezes the study's reader on the
// participant's own store with their own token and records the withdrawal
// once; agreeing again unfreezes it and records a fresh agreement; a closed
// study says when it ended.
func TestWithdrawAndRejoin(t *testing.T) {
	var calls []string
	frozen := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer own" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/oauth/account":
			writeJSON(w, 200, map[string]string{"account": "person"})
		case r.URL.Path == "/v1/datasets":
			writeJSON(w, 200, map[string]any{"datasets": []map[string]any{
				{"id": "theirs", "access": map[string]bool{"read": true, "write": true}},
				{"id": "shared", "access": map[string]bool{"read": true}}}})
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/datasets/theirs/readers/analysis:"):
			action := strings.TrimPrefix(r.URL.Path, "/v1/datasets/theirs/readers/analysis:")
			calls = append(calls, action)
			if action == "unfreeze" && !frozen {
				writeAPIError(w, 409, "conflict")
				return
			}
			frozen = action == "freeze"
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	s := &studyStore{docs: fileDocs{dir: t.TempDir()}}
	st, err := s.save("sw", StudyEdit{Title: "Study", ConsentText: "First"}, true)
	must(t, err)
	st.Status, st.IssuerID = "open", "issuer"
	raw, _ := json.Marshal(st)
	must(t, s.docs.put("studies/sw.json", raw))
	must(t, s.recordConsent("sw", ConsentRecord{Account: "person", Version: 1, At: time.Now().Add(-time.Hour)}))
	a := &app{studies: s, tessera: &Tessera{internal: remote.URL, http: remote.Client()}}
	a.cfg.Variant, a.cfg.Analysis = "research", "analysis"
	call := func(handler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer own")
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("slug", "sw")
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	withdraw := func() *httptest.ResponseRecorder {
		return call(a.apiWithdraw, "POST", "/api/v1/studies/sw/withdraw", "")
	}

	for i := 0; i < 2; i++ {
		w := withdraw()
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"withdrawn":`) || !strings.Contains(w.Body.String(), `"agreement_needed":true`) {
			t.Fatalf("withdraw answered %d %s", w.Code, w.Body)
		}
	}
	if !frozen || len(s.withdrawalHistory("sw")) != 1 || strings.Join(calls, ",") != "freeze,freeze" {
		t.Fatalf("withdrawing twice: frozen=%v, %d withdrawals, calls %v; want frozen, one record", frozen, len(s.withdrawalHistory("sw")), calls)
	}
	for i := 0; i < 2; i++ {
		w := call(a.apiConsent, "POST", "/api/v1/studies/sw/consent", `{"version":1}`)
		if w.Code != 200 || strings.Contains(w.Body.String(), `"withdrawn":`) || !strings.Contains(w.Body.String(), `"agreement_needed":false`) {
			t.Fatalf("rejoin answered %d %s", w.Code, w.Body)
		}
	}
	if frozen || len(s.consents("sw")) != 2 || strings.Join(calls, ",") != "freeze,freeze,unfreeze" {
		t.Fatalf("rejoining twice: frozen=%v, %d consents, calls %v; want unfrozen, one new agreement", frozen, len(s.consents("sw")), calls)
	}

	// After an erase the study holds no share on the replacement store:
	// Tessera answers 404 not_found. Withdrawing is then already true; only
	// rejoining is impossible.
	erased := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/account":
			writeJSON(w, 200, map[string]string{"account": "person"})
		case r.URL.Path == "/v1/datasets":
			writeJSON(w, 200, map[string]any{"datasets": []map[string]any{{"id": "replacement", "access": map[string]bool{"read": true, "write": true}}}})
		default:
			writeAPIError(w, 404, "not_found")
		}
	}))
	defer erased.Close()
	a.tessera.internal = erased.URL
	if w := withdraw(); w.Code != 200 || !strings.Contains(w.Body.String(), `"withdrawn":`) {
		t.Fatalf("withdrawing where the study holds no share answered %d %s; want done", w.Code, w.Body)
	}
	if w := call(a.apiConsent, "POST", "/api/v1/studies/sw/consent", `{"version":1}`); w.Code != 409 || !strings.Contains(w.Body.String(), "store_unavailable") {
		t.Fatalf("rejoining where the study holds no share answered %d %s; want 409 store_unavailable", w.Code, w.Body)
	}
	a.tessera.internal = remote.URL
	_ = withdraw() // back to a frozen share for what follows
	if w := call(a.apiConsent, "POST", "/api/v1/studies/sw/consent", `{"version":1}`); w.Code != 200 {
		t.Fatalf("rejoin answered %d %s", w.Code, w.Body)
	}

	// A token for nobody's store (erased, or not approved to write) cannot
	// withdraw or rejoin, and nothing is recorded.
	a.tessera.internal = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/account" {
			writeJSON(w, 200, map[string]string{"account": "person"})
			return
		}
		writeJSON(w, 200, map[string]any{"datasets": []any{}})
	})).URL
	before := len(s.withdrawalHistory("sw"))
	if w := withdraw(); w.Code != 409 || !strings.Contains(w.Body.String(), "store_unavailable") || len(s.withdrawalHistory("sw")) != before {
		t.Fatalf("withdrawing without a store answered %d %s", w.Code, w.Body)
	}

	// Closed: 410 with the date, on the public study and on joining; the
	// participant's status carries it too. Reopening clears it.
	a.tessera.internal = remote.URL
	st, err = s.save("sw", StudyEdit{Title: "Study", ConsentText: "First", Status: "closed"}, false)
	must(t, err)
	if st.ClosedAt == nil {
		t.Fatal("closing did not record when")
	}
	for _, h := range []http.HandlerFunc{a.apiStudy, a.apiJoin} {
		w := call(h, "POST", "/api/v1/studies/sw", `{"version":1}`)
		if w.Code != 410 || !strings.Contains(w.Body.String(), `"code":"study_closed"`) || !strings.Contains(w.Body.String(), `"closed_at":`) {
			t.Fatalf("closed study answered %d %s; want 410 study_closed with closed_at", w.Code, w.Body)
		}
	}
	if w := call(a.apiConsent, "GET", "/api/v1/studies/sw/consent", ""); !strings.Contains(w.Body.String(), `"closed_at":`) {
		t.Fatalf("participant status of a closed study has no closed_at: %s", w.Body)
	}
	st.ClosedAt = nil // closed before the date was recorded
	raw, _ = json.Marshal(st)
	must(t, s.docs.put("studies/sw.json", raw))
	if w := call(a.apiStudy, "GET", "/api/v1/studies/sw", ""); w.Code != 410 || strings.Contains(w.Body.String(), "closed_at") {
		t.Fatalf("a study closed without a date answered %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/api/v1/studies/nope", nil)
	r.SetPathValue("slug", "nope")
	w := httptest.NewRecorder()
	a.apiStudy(w, r)
	if w.Code != 404 {
		t.Fatalf("an unknown study answered %d", w.Code)
	}
}

// TestPinInLinks: a local Home's links carry its CA pin, percent-encoded so
// base64's / + = survive a URL, and a public server's carry none.
func TestPinInLinks(t *testing.T) {
	pin := "sha256/Ab+c/d=="
	link, err := connectLink("https://192.168.1.10", "Home", "home", pin)
	if err != nil || !strings.Contains(link, "pin=sha256%2FAb%2Bc%2Fd%3D%3D") {
		t.Fatalf("connect link %q does not carry the pin percent-encoded", link)
	}
	u, _ := url.Parse(link)
	if u.Query().Get("pin") != pin {
		t.Fatalf("the pin does not decode back: %q", u.Query().Get("pin"))
	}
	if l, _ := connectLink("https://home.example.org", "Home", "home", ""); strings.Contains(l, "pin=") {
		t.Fatal("a public server's link carries a pin")
	}
	redeem := withPin("https://192.168.1.10/oauth/invitation?code=abc", pin)
	r, _ := url.Parse(redeem)
	if r.Query().Get("code") != "abc" || r.Query().Get("pin") != pin {
		t.Fatalf("an invitation with the pin is %q; want its code kept and the pin added", redeem)
	}
	if withPin("https://h/oauth/invitation?code=abc", "") != "https://h/oauth/invitation?code=abc" {
		t.Fatal("an invitation changed although there is no pin")
	}
	if l := studyAppLink("https://192.168.1.10", "S", "s", "c", pin); !strings.Contains(l, "pin=") {
		t.Fatalf("a study's app link %q carries no pin", l)
	}
}
