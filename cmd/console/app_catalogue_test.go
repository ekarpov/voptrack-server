package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	appcatalogue "github.com/ekarpov/voptrack-server/catalogue"
)

func catalogueTypeForTest(t *testing.T, id string) TypeInfo {
	t.Helper()
	m, ok := catalogueEffective(id)
	if !ok {
		t.Fatal("missing app declaration")
	}
	info := TypeInfo{ID: m.ID, Name: m.Name, Schema: m, Fields: m.Fields}
	for _, f := range m.Fields {
		switch f.Type {
		case "integer", "float", "fixed":
			info.Numeric = append(info.Numeric, f)
		case "date_time":
			info.Dates = append(info.Dates, f)
		case "text", "reference":
			info.Groups = append(info.Groups, f)
		}
	}
	return info
}
func TestCatalogueRejectsSchemaDriftButReadsOldBookkeeping(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.expense")
	if !catalogueReadCompatible(typ) || !qualifiedHomeMetric(typ) {
		t.Fatal("app type refused")
	}
	old := typ
	old.Schema.Fields = nil
	for _, f := range typ.Fields {
		if f.ID != "addedAt" && f.ID != "writeToken" {
			old.Schema.Fields = append(old.Schema.Fields, f)
		}
	}
	if !catalogueReadCompatible(old) {
		t.Fatal("optional historical bookkeeping blocked read-only presentation")
	}
	drift := typ
	drift.Schema.Fields = append([]Field(nil), typ.Schema.Fields...)
	for i := range drift.Schema.Fields {
		if drift.Schema.Fields[i].ID == "amount" {
			drift.Schema.Fields[i].Type = "float"
		}
	}
	if qualifiedHomeMetric(drift) {
		t.Fatal("same ID with another declaration accepted")
	}
	drift = typ
	drift.SchemaConflict = true
	if qualifiedHomeMetric(drift) {
		t.Fatal("disagreeing member schemas combined")
	}
}
func TestCatalogueUsesAppRecordVectors(t *testing.T) {
	for _, name := range []string{"pet_feeding", "chore_completion", "weight", "expense"} {
		t.Run(name, func(t *testing.T) {
			id := "com.voptrack.track." + name
			typ := catalogueTypeForTest(t, id)
			raw, e := os.ReadFile("../../catalogue/2/vectors/" + id + ".json")
			must(t, e)
			var vector struct {
				Fields map[string]struct{ Value any }
			}
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.UseNumber()
			must(t, dec.Decode(&vector))
			row := readRecord{Dataset: "household", Type: id, When: time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC), Fields: typ.Fields, Values: map[string]any{}}
			for key, f := range vector.Fields {
				row.Values[key] = f.Value
			}
			series := viewSeries([]readRecord{row}, typ, time.UTC, time.Time{}, false, "month")
			if len(series) != 1 || seriesValue(series[0]) == nil {
				t.Fatal("app vector not presented")
			}
			m := series[0].Metric
			if (name == "pet_feeding" || name == "chore_completion") && m.Measure != MeasureCount {
				t.Fatal("app count became numeric sum")
			}
			if name == "weight" && (!m.Latest || m.Field != "weight") {
				t.Fatal("weight is not latest")
			}
			if name == "expense" && (!m.Money || m.Field != "amount" || m.Measure != MeasureSum) {
				t.Fatal("expense semantics lost")
			}
		})
	}
}
func TestCatalogueRuntimeAndInternalTypesAreNotInvented(t *testing.T) {
	for _, name := range []string{"walking", "step_counter", "sleep", "pet_vet_visit"} {
		if qualifiedHomeMetric(catalogueTypeForTest(t, "com.voptrack.track."+name)) {
			t.Fatal("unqualified runtime/composite calculation enabled")
		}
	}
	if !homeInfrastructure("com.voptrack.profile_track") || !catalogueInternalField("com.voptrack.track.expense", "bankReference") || !catalogueInternalField("com.voptrack.track.expense", "writeToken") {
		t.Fatal("internal app data exposed")
	}
}

func TestCatalogueAllHistoryHasNoInventedComparison(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	w := periodWindow("all", now)
	if w.Key != "all" || !w.From.IsZero() || w.Comparable || w.Current || w.To != now {
		t.Fatal("invalid catalogue all-history period")
	}
}
func TestCatalogueLatestWeightDoesNotAverageMembersOrUnits(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.weight")
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"weight": json.Number("60"), "unit": "kg"}), syntheticValueRecord(2, map[string]any{"weight": json.Number("61"), "unit": "kg"}), syntheticValueRecord(3, map[string]any{"weight": json.Number("75000"), "unit": "g"})}
	series := viewSeries(rows, typ, time.UTC, time.Time{}, false, "month")
	if len(series) != 2 {
		t.Fatal("units combined")
	}
	for _, s := range series {
		if s.Unit == "kg" && seriesValue(s).String() != "61/1" {
			t.Fatal("weight was averaged instead of latest")
		}
	}
}
func TestCatalogueOdometerModesRemainSeparate(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.odometer_reading")
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"distance": json.Number("100"), "distanceUnit": "km", "meterID": "car"}), syntheticValueRecord(2, map[string]any{"hours": json.Number("9"), "hoursUnit": "h", "meterID": "boat"})}
	series := viewSeries(rows, typ, time.UTC, time.Time{}, false, "month")
	if len(series) != 2 {
		t.Fatal("odometer distance and engine time combined or lost")
	}
	rows[0].Values["hours"] = json.Number("10")
	for _, s := range viewSeries(rows, typ, time.UTC, time.Time{}, false, "month") {
		if !s.Result.Truncated {
			t.Fatal("invalid two-mode record was not withheld")
		}
	}
}
func TestCatalogueInternalFieldsDoNotParticipateInSearch(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.expense")
	row := readRecord{Type: typ.ID, Fields: typ.Fields, Values: map[string]any{"purpose": "Visible", "bankReference": "private bookkeeping"}}
	if len(filterRecords([]readRecord{row}, "", "bookkeeping")) != 0 {
		t.Fatal("internal field leaked into text search")
	}
}

func TestCatalogueCountsHaveWholeRecordAxisTicks(t *testing.T) {
	res := Result{Spec: Spec{Measure: MeasureCount}, Points: []Point{{Label: "day", Tally: Tally{Rows: 2}}}}
	chart := buildChart(res, false)
	if len(chart.Ticks) != 3 || chart.Ticks[0].Label != "0" || chart.Ticks[1].Label != "1" || chart.Ticks[2].Label != "2" {
		t.Fatal("count axis implies fractional records")
	}
}
func TestCatalogueMoneyStorageScaleMatchesQualifiedReader(t *testing.T) {
	for id, d := range appTracks.Documents {
		if d.Kind != "track" || !moneyType(id) {
			continue
		}
		raw, e := os.ReadFile("../../catalogue/2/types/" + id + ".json")
		must(t, e)
		var doc struct {
			Fields []struct {
				ID    string
				Shows struct {
					Analytics *struct {
						MoneyScale      int
						CurrencyFieldID string
					}
				}
			}
		}
		must(t, json.Unmarshal(raw, &doc))
		found := false
		for _, field := range doc.Fields {
			if field.ID == "amount" {
				a := field.Shows.Analytics
				found = a != nil && a.MoneyScale == 2 && a.CurrencyFieldID == "currency"
			}
		}
		if !found {
			t.Fatal("catalogue money encoding needs a new reader")
		}
	}
}

func TestHouseholdExcludesCatalogueParts(t *testing.T) {
	// Exercise the announced v3 classification without manufacturing or changing
	// a generated export. Loading/authenticating v3 remains a separate pin change.
	c := &appcatalogue.Catalogue{Documents: map[string]appcatalogue.Document{}}
	for _, id := range []string{"com.voptrack.track.workout_effort", "com.voptrack.track.gps_position", "com.voptrack.track.exercise_set", "custom.part"} {
		c.Documents[id] = appcatalogue.Document{ID: id, Kind: "part", CatalogueVersion: 3}
		if !catalogueNonTrack(c, id) {
			t.Fatalf("part %s offered as a Track", id)
		}
	}
	c.Documents["custom.track"] = appcatalogue.Document{ID: "custom.track", Kind: "track", CatalogueVersion: 3}
	if catalogueNonTrack(c, "custom.track") || catalogueNonTrack(c, "unknown.custom.track") {
		t.Fatal("standalone or unknown custom Track hidden")
	}
	// These two IDs are still marked track in our pinned v2. The household
	// filter must already exclude them before the next immutable export arrives.
	for _, id := range []string{"com.voptrack.track.workout_effort", "com.voptrack.track.gps_position"} {
		if !homeInfrastructure(id) {
			t.Fatalf("v2 compatibility exclusion missing for %s", id)
		}
	}
	if homeInfrastructure("com.voptrack.track.workout_session") || homeInfrastructure("com.voptrack.track.running") {
		t.Fatal("parent workout/run hidden")
	}
}
