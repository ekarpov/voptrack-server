package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestOpaqueAnnotationsSurviveReadAndRoundtrip(t *testing.T) {
	raw := []byte(`{"id":"sample","annotations":{"meaning":"sample","flag":true,"number":9007199254740993,"object":{"b":false,"a":[1,"x"]},"nothing":null},"fields":[{"id":"value","meta_id":"sample","type":"integer","annotations":{"meaning":"additive","options":["a","b"]}}]}`)
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Annotations["meaning"] != "sample" || m.Fields[0].Annotations["meaning"] != "additive" || len(m.OpaqueAnnotations) != 4 || m.Fields[0].MetaID != "sample" {
		t.Fatal("lost annotations or field owner")
	}
	if string(m.OpaqueAnnotations["number"]) != "9007199254740993" {
		t.Fatal("annotation precision was lost")
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got Meta
	if err = json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, got) {
		t.Fatal("opaque values changed on roundtrip")
	}
}

func TestUnknownNumericHomeTrackKeepsHistoryWithoutGuessing(t *testing.T) {
	unknown := TypeInfo{ID: "custom", Numeric: []Field{{ID: "value", Type: "integer"}}}
	if qualifiedHomeMetric(unknown) {
		t.Fatal("an arbitrary integer was treated as a declared period metric")
	}
	unknown.Numeric[0].Annotations = map[string]string{"meaning": "additive"}
	if !qualifiedHomeMetric(unknown) {
		t.Fatal("declared additive metric was ignored")
	}
	money := catalogueTypeForTest(t, "com.voptrack.track.expense")
	if !qualifiedHomeMetric(money) {
		t.Fatal("supported money semantics were lost")
	}
}
