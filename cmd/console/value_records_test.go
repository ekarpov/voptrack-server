package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticValueRecord(day int, values map[string]any) readRecord {
	return readRecord{ID: strings.Repeat("x", day), Dataset: "one", Type: "track", Member: "Synthetic", When: time.Date(2026, 11, day, 9, 0, 0, 0, time.UTC), Values: values}
}
func TestMoneySeriesAreExactAndNeverCombineCurrencies(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.expense")
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"amount": json.Number("9007199254740993"), "currency": "EUR"}), syntheticValueRecord(2, map[string]any{"amount": json.Number("7"), "currency": "EUR"}), syntheticValueRecord(3, map[string]any{"amount": json.Number("1234"), "currency": "USD"})}
	series := seriesFor(rows, typ, time.UTC, time.Time{}, false)
	if len(series) != 2 {
		t.Fatal("currency partition missing")
	}
	for _, s := range series {
		value := pointValue(s.Result, s.Result.Points[0]).FloatString(2)
		if s.Unit == "EUR" && value != "90071992547410.00" {
			t.Fatal("minor units lost exactness")
		}
		if s.Unit == "USD" && value != "12.34" {
			t.Fatal("wrong currency value")
		}
	}
}
func TestFeedingCountDoesNotSumMassAndKeepsIndividualValues(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.pet_feeding")
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"mass": json.Number("80"), "unit": "g", "foodID": "a", "food": "Food A"}), syntheticValueRecord(2, map[string]any{"mass": json.Number("0.2"), "unit": "kg", "foodID": "a", "food": "Food A"}), syntheticValueRecord(3, map[string]any{"mass": json.Number("90"), "unit": "g", "foodID": "b", "food": "Food B"})}
	series := viewSeries(rows, typ, time.UTC, time.Time{}, false, "month")
	if len(series) != 1 || seriesValue(series[0]).String() != "3/1" || series[0].Metric.Measure != MeasureCount {
		t.Fatal("feeding headline did not count records")
	}
	for i, want := range []string{"80 g", "0.2 kg", "90 g"} {
		value, _ := trackRecordColumns(rows[i], typ)
		if value != want {
			t.Fatal("individual feeding value lost")
		}
	}
}

func TestCounterChartUsesLatestReadingPerMeter(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.electricity_meter_reading")
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"value": json.Number("100"), "unit": "kWh", "meterID": "a"}), syntheticValueRecord(2, map[string]any{"value": json.Number("110"), "unit": "kWh", "meterID": "a"}), syntheticValueRecord(3, map[string]any{"value": json.Number("990"), "unit": "kWh", "meterID": "b"})}
	series := seriesFor(rows, typ, time.UTC, time.Time{}, false)
	if len(series) != 2 {
		t.Fatal("meter identities combined")
	}
	for _, s := range series {
		v := pointValue(s.Result, s.Result.Points[0])
		if v.String() != "110/1" && v.String() != "990/1" {
			t.Fatal("meter readings were summed")
		}
	}
}
func TestDecodeRecordsUsesProjectionAndQualifiedIdentity(t *testing.T) {
	rows, e := decodeRecords(recordSet{Meta: Meta{Fields: []Field{{ID: "purpose"}, {ID: "amount"}}}, Records: []wireRecord{{ID: recordIdentity{"same", "one"}, Type: "expense", Values: Row{{Value: "Synthetic"}, {Value: json.Number("123")}}}}}, "one", "Synthetic")
	if e != nil || len(rows) != 1 || rows[0].Values["amount"] != json.Number("123") {
		t.Fatal("projection order lost")
	}
	_, e = decodeRecords(recordSet{Records: []wireRecord{{ID: recordIdentity{"same", "other"}, Type: "expense"}}}, "one", "")
	if e == nil {
		t.Fatal("foreign identity accepted")
	}
}
func TestRecordAndAttachmentRequestsRecheckAuthority(t *testing.T) {
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.Variant = "household"
	a.cfg.HomeService = "home"
	a.homeReg = &homeRegistry{docs: a.studies.docs}
	must(t, a.homeReg.set("one", true))
	a.homeCache = newServiceTokens("home", "")
	a.homeCache.secret = "synthetic"
	var denied atomic.Bool
	var resolves atomic.Int32
	image, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0e0AAAAASUVORK5CYII=")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/account":
			writeJSON(w, 200, map[string]any{"account": "synthetic", "server_administrator": true})
		case r.URL.Path == "/oauth/token":
			writeJSON(w, 200, map[string]any{"access_token": "shared-reader", "expires_in": 300})
		case r.URL.Path == "/v1/datasets":
			writeJSON(w, 200, map[string]any{"datasets": []any{}})
		case r.URL.Path == "/v1/datasets/one/meta":
			writeJSON(w, 200, map[string]any{"types": []Meta{{ID: "receipt", Name: "Receipt"}}})
		case r.URL.Path == "/v1/datasets/one/objects/receipt/first":
			if denied.Load() {
				w.WriteHeader(404)
				return
			}
			if r.Header.Get("Authorization") != "Bearer shared-reader" {
				t.Error("wrong record authority")
			}
			writeJSON(w, 200, recordSet{Meta: Meta{Fields: []Field{{ID: "receipt", Name: "Receipt image", Type: "media"}}}, Records: []wireRecord{{ID: recordIdentity{"first", "one"}, Type: "receipt", Values: Row{{Type: "media", Value: "store//image"}}}}})
		case r.URL.Path == "/v1/stores/store/items/image":
			writeJSON(w, 200, map[string]any{"content_type": "image/png", "byte_count": len(image)})
		case r.URL.Path == "/v1/stores/store/items/image:resolve":
			resolves.Add(1)
			writeJSON(w, 200, map[string]any{"url": server.URL + "/bytes?credential=must-stay-private", "method": "GET"})
		case r.URL.Path == "/bytes":
			w.Header().Set("Content-Type", "image/png")
			w.Write(image)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a.tessera = &Tessera{internal: server.URL, http: server.Client()}
	for _, page := range []string{"record"} {
		tpl, e := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html", "templates/"+page+".html")
		must(t, e)
		a.pages[page] = tpl
	}
	query := url.Values{"scope": {"home"}, "dataset": {"one"}, "type": {"receipt"}, "id": {"first"}}
	w := savedRequest(a, s, "GET", "/record?"+query.Encode(), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Download attachment") || strings.Contains(w.Body.String(), "credential=") {
		t.Fatal("record did not present a safe attachment link")
	}
	query.Set("attachment", attachmentKey(contentReference{"store", "image"}))
	query.Set("preview", "1")
	w = savedRequest(a, s, "GET", "/attachment?"+query.Encode(), nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "no-store" || w.Body.String() != string(image) {
		t.Fatal("authorized image preview failed")
	}
	count := resolves.Load()
	denied.Store(true)
	w = savedRequest(a, s, "GET", "/attachment?"+query.Encode(), nil)
	if w.Code == 200 || resolves.Load() != count {
		t.Fatal("revoked record still delivered attachment")
	}
	denied.Store(false)
	query.Set("dataset", "private")
	w = savedRequest(a, s, "GET", "/attachment?"+query.Encode(), nil)
	if w.Code == 200 || resolves.Load() != count {
		t.Fatal("tampered dataset delivered attachment")
	}
}
func TestContentTransferRejectsForeignOriginsAndRedirects(t *testing.T) {
	var outside atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outside.Add(1) }))
	defer foreign.Close()
	var redirect atomic.Bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, foreign.URL+"/private", http.StatusFound)
			return
		}
		target := foreign.URL + "/private"
		if redirect.Load() {
			target = srv.URL + "/redirect"
		}
		writeJSON(w, 200, map[string]any{"url": target, "method": "GET"})
	}))
	defer srv.Close()
	tessera := &Tessera{internal: srv.URL, http: srv.Client()}
	if _, _, e := tessera.attachmentBytes(context.Background(), "synthetic", contentReference{"store", "image"}); e == nil || outside.Load() != 0 {
		t.Fatal("foreign transfer origin was fetched")
	}
	redirect.Store(true)
	if _, _, e := tessera.attachmentBytes(context.Background(), "synthetic", contentReference{"store", "image"}); e == nil || outside.Load() != 0 {
		t.Fatal("transfer redirect was followed")
	}
}

func TestModifiedMoneyDefinitionIsNotSilentlyRescaled(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.expense")
	for i := range typ.Schema.Fields {
		if typ.Schema.Fields[i].ID == "amount" {
			typ.Schema.Fields[i].Type = "float"
		}
	}

	if metricFor(typ).Money {
		t.Fatal("modified field was treated as app minor units")
	}
}
func TestMissingMonthsBreakTheValueChartInsteadOfBecomingZero(t *testing.T) {
	typ := TypeInfo{ID: "custom", Numeric: []Field{{ID: "value", Type: "float"}}}
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"value": json.Number("10")}), syntheticValueRecord(2, map[string]any{"value": json.Number("20")})}
	rows[0].When = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	series := seriesFor(rows, typ, time.UTC, time.Time{}, false)
	if len(series) != 1 || len(series[0].Result.Points) != 3 || pointValue(series[0].Result, series[0].Result.Points[1]) != nil {
		t.Fatal("missing month was flattened or filled")
	}
	chart := buildChart(series[0].Result, true)
	if strings.Contains(chart.Path, "L") {
		t.Fatal("chart joined across missing month")
	}
}

func TestPublicNativeContentOriginUsesInternalRoute(t *testing.T) {
	image := []byte("synthetic bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":resolve") {
			writeJSON(w, 200, map[string]any{"url": "http://native.invalid/bytes?signature=private", "method": "GET"})
			return
		}
		if r.Host != "native.invalid" || r.URL.RawQuery != "signature=private" {
			t.Error("signed request changed")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write(image)
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = http.DefaultTransport.(*http.Transport).Clone()
	api := &Tessera{internal: server.URL, public: "http://native.invalid", http: client}
	data, _, err := api.attachmentBytes(context.Background(), "reader", contentReference{"store", "item"})
	if err != nil || string(data) != string(image) {
		t.Fatal("configured native route failed", err)
	}
}

func TestStudyScopeCannotUseHomeRecordRoutes(t *testing.T) {
	a, s, server := policyHTTPApp(t)
	defer server.Close()
	w := savedRequest(a, s, "GET", "/tracks?scope=study%3Aexample&type=receipt", nil)
	if w.Code != 404 {
		t.Fatal("study raw route accepted", w.Code)
	}
}

func TestBalancesKeepMembersSeparate(t *testing.T) {
	typ := catalogueTypeForTest(t, "com.voptrack.track.balance")
	one := syntheticValueRecord(1, map[string]any{"amount": json.Number("10000"), "currency": "EUR"})
	two := syntheticValueRecord(2, map[string]any{"amount": json.Number("20000"), "currency": "EUR"})
	two.Dataset = "two"
	series := seriesFor([]readRecord{one, two}, typ, time.UTC, time.Time{}, false)
	if len(series) != 2 {
		t.Fatal("unrelated balances collapsed")
	}
}
