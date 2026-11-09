package main

import (
	"encoding/json"
	"testing"
)

func TestTrackColumnsKeepUnitsMoneyAndDescription(t *testing.T) {
	for _, tc := range []struct {
		name          string
		typ           TypeInfo
		values        map[string]any
		value, detail string
	}{
		{"pet portion", catalogueTypeForTest(t, "com.voptrack.track.pet_feeding"), map[string]any{"mass": 80.0, "unit": "g", "food": "Demo pet food"}, "80 g", "Demo pet food"},
		{"minor-unit expense", catalogueTypeForTest(t, "com.voptrack.track.expense"), map[string]any{"amount": json.Number("1234"), "currency": "EUR", "purpose": "Groceries"}, "EUR 12.34", "Groceries"},
		{"text event", TypeInfo{ID: "custom.event"}, map[string]any{"description": "Walk around the park"}, "—", "Walk around the park"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, detail := trackRecordColumns(readRecord{Values: tc.values}, tc.typ)
			if value != tc.value || detail != tc.detail {
				t.Fatalf("got %q / %q, want %q / %q", value, detail, tc.value, tc.detail)
			}
		})
	}
}
