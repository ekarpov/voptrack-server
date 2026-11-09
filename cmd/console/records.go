package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Record identities are qualified by dataset AND concrete type. Values are
// mapped through the response projection, never the requested field order.
type recordIdentity struct {
	ID      string `json:"id"`
	Dataset string `json:"dataset_id"`
}
type wireRecord struct {
	ID     recordIdentity `json:"id"`
	Type   string         `json:"meta_id"`
	Values Row            `json:"values"`
}
type recordSet struct {
	Meta    Meta         `json:"meta"`
	Records []wireRecord `json:"records"`
	Next    *string      `json:"next_cursor"`
}
type readRecord struct {
	ID, Dataset, Type, Member, Name string
	HomeSubjectKey                  string
	When                            time.Time
	Fields                          []Field
	Values                          map[string]any
}

const recordReadLimit = 2000

func decodeRecords(set recordSet, dataset, member string) ([]readRecord, error) {
	var out []readRecord
	for _, row := range set.Records {
		if row.ID.ID == "" || row.ID.Dataset != dataset || row.Type == "" || len(row.Values) != len(set.Meta.Fields) {
			return nil, errors.New("invalid record projection")
		}
		rec := readRecord{ID: row.ID.ID, Dataset: dataset, Type: row.Type, Member: member, Name: localizeName(set.Meta.Name, translator("en")), Fields: set.Meta.Fields, Values: map[string]any{}}
		for i, f := range set.Meta.Fields {
			rec.Values[f.ID] = row.Values[i].Value
		}
		for _, key := range []string{"timestamp", "recorded_at"} {
			if text, ok := rec.Values[key].(string); ok {
				if at, e := time.Parse(time.RFC3339Nano, text); e == nil {
					rec.When = at
					break
				}
			}
		}
		out = append(out, rec)
	}
	return out, nil
}
func (t *Tessera) recordRead(ctx context.Context, token, dataset, typ, id string) (readRecord, error) {
	var set recordSet
	err := t.call(ctx, "GET", "/v1/datasets/"+url.PathEscape(dataset)+"/objects/"+url.PathEscape(typ)+"/"+url.PathEscape(id), token, "", nil, 200, &set)
	if err != nil {
		return readRecord{}, err
	}
	rows, err := decodeRecords(set, dataset, "")
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		return readRecord{}, errors.New("record unavailable")
	}
	return rows[0], nil
}
func (t *Tessera) recordRows(ctx context.Context, token, dataset, typ string, query map[string]any, limit int) ([]readRecord, bool, error) {
	var begun struct {
		Cursor string `json:"cursor"`
	}
	base := "/v1/datasets/" + url.PathEscape(dataset)
	if e := t.post(ctx, base+"/objects/"+url.PathEscape(typ)+":page", token, map[string]any{"query": query, "page_size": 100}, &begun); e != nil {
		return nil, false, e
	}
	cursor := begun.Cursor
	// Release even on a caller limit/error; cursors and their opaque values never
	// enter HTML, URLs, durable state or logs.
	defer func() {
		if cursor != "" {
			_ = t.call(context.WithoutCancel(ctx), "DELETE", base+"/pages/"+url.PathEscape(cursor), token, "", nil, 204, nil)
		}
	}()
	var out []readRecord
	for cursor != "" {
		var set recordSet
		if e := t.call(ctx, "POST", base+"/pages/"+url.PathEscape(cursor)+":next", token, "", nil, 200, &set); e != nil {
			return nil, false, e
		}
		rows, e := decodeRecords(set, dataset, "")
		if e != nil {
			return nil, false, e
		}
		for _, row := range rows {
			if len(out) == limit {
				return out, true, nil
			}
			out = append(out, row)
		}
		if set.Next == nil {
			return out, false, nil
		}
		cursor = *set.Next
	}
	return out, false, nil
}
func dateField(t TypeInfo) string {
	for _, key := range []string{"timestamp", "recorded_at"} {
		for _, f := range t.Dates {
			if f.ID == key {
				return key
			}
		}
	}
	if len(t.Dates) > 0 {
		return t.Dates[0].ID
	}
	return ""
}
func readTrackRecords(ctx context.Context, t *Tessera, sources []Source, typ TypeInfo, from, to time.Time) ([]readRecord, bool, error) {
	var all []readRecord
	incomplete := false
	for _, src := range sources {
		token, e := src.Token(ctx)
		if e != nil {
			incomplete = true
			continue
		}
		var metas []Meta
		e = retryRefused(ctx, src, &token, func(tok string) error { var err error; metas, err = t.types(ctx, tok, src.Dataset); return err })
		if e != nil {
			incomplete = true
			continue
		}
		present := false
		for _, m := range metas {
			if m.ID == typ.ID {
				present = true
			}
		}
		if !present {
			continue
		}
		query := map[string]any{"type_scope": map[string]any{"kind": "exact"}}
		date := dateField(typ)
		if date != "" {
			query["result_sort"] = []any{map[string]any{"field_id": date, "ascending": false}}
			filter := Spec{Date: date, From: from, To: to}.window()
			if filter != nil {
				query["filter"] = filter
			}
		}
		var rows []readRecord
		var cut bool
		e = retryRefused(ctx, src, &token, func(tok string) error {
			var err error
			rows, cut, err = t.recordRows(ctx, tok, src.Dataset, typ.ID, query, recordReadLimit)
			return err
		})
		if e != nil {
			incomplete = true
			continue
		}
		incomplete = incomplete || cut
		for _, row := range rows {
			row.Member = src.Name
			if text, ok := row.Values[date].(string); ok {
				if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
					row.When = at
				}
			}
			all = append(all, row)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].When.Equal(all[j].When) {
			return all[i].When.After(all[j].When)
		}
		return all[i].Dataset+"/"+all[i].Type+"/"+all[i].ID < all[j].Dataset+"/"+all[j].Type+"/"+all[j].ID
	})
	return all, incomplete, nil
}
func (a *app) recordSources(w http.ResponseWriter, r *http.Request) (*session, string, []Source, bool) {
	sess := a.sessions.get(w, r, false)
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "home"
	}
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil, scope, nil, false
	}
	if scope == "home" {
		if a.cfg.Variant == "research" || !a.viewerCurrent(r, sess) {
			a.fail(w, r, sess, 403, "expired", nil)
			return sess, scope, nil, false
		}
	} else if scope == "mine" {
	} else {
		a.fail(w, r, sess, 404, "no_tracks", nil)
		return sess, scope, nil, false
	}
	return sess, scope, a.scopeSources(r, sess, scope), true
}
func recordURL(scope string, rec readRecord) string {
	return "/record?" + url.Values{"scope": {scope}, "dataset": {rec.Dataset}, "type": {rec.Type}, "id": {rec.ID}}.Encode()
}
func (a *app) readableRecord(w http.ResponseWriter, r *http.Request) (*session, string, Source, readRecord, bool) {
	sess, scope, sources, ok := a.recordSources(w, r)
	if !ok {
		return sess, scope, Source{}, readRecord{}, false
	}
	q := r.URL.Query()
	for _, src := range sources {
		if src.Dataset != q.Get("dataset") {
			continue
		}
		token, e := src.Token(r.Context())
		if e != nil {
			break
		}
		var rec readRecord
		e = retryRefused(r.Context(), src, &token, func(tok string) error {
			var err error
			rec, err = a.tessera.recordRead(r.Context(), tok, src.Dataset, q.Get("type"), q.Get("id"))
			return err
		})
		if e == nil {
			rec.Member = src.Name
			return sess, scope, src, rec, true
		}
		break
	}
	a.fail(w, r, sess, 404, "no_tracks", nil)
	return sess, scope, Source{}, readRecord{}, false
}
func displayBlob(v any) string {
	text, ok := v.(string)
	if !ok {
		return ""
	}
	data, e := base64.StdEncoding.DecodeString(text)
	if e != nil {
		return ""
	}
	var decoded any
	if json.Unmarshal(data, &decoded) == nil {
		switch x := decoded.(type) {
		case string:
			return x
		case map[string]any:
			if literal, ok := x["literal"].(map[string]any); ok {
				if text, ok := literal["_0"].(string); ok {
					return text
				}
			}
			if external, ok := x["external"].(map[string]any); ok {
				if translations, ok := external["translations"].(map[string]any); ok {
					if text, ok := translations["en"].(string); ok {
						return text
					}
				}
				if text, ok := external["fallback"].(string); ok {
					return text
				}
			}
			for _, key := range []string{"text", "literal", "value"} {
				if s, ok := x[key].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}
func readableValue(f Field, v any) string {
	if v == nil {
		return "Not recorded"
	}
	switch f.Type {
	case "blob":
		if s := displayBlob(v); s != "" {
			return s
		}
		return "Stored data"
	case "media":
		return "Attachment"
	case "reference":
		return "Related record"
	case "boolean":
		if v == true {
			return "Yes"
		}
		return "No"
	case "date_time":
		if s, ok := v.(string); ok {
			if at, e := time.Parse(time.RFC3339Nano, s); e == nil {
				return at.Format("2 Jan 2006, 15:04 MST")
			}
		}
	}
	text := fmt.Sprint(v)
	if len([]rune(text)) > 2000 {
		text = string([]rune(text)[:2000]) + "…"
	}
	return text
}

type recordFieldView struct{ Name, Value string }
type recordView struct {
	HomeSubjectName, HomeSubjectKind     string
	RelatedUnavailable                   bool
	Title, Member, When, Back, BackLabel string
	Fields                               []recordFieldView
	Attachments                          []attachmentView
	AttachmentUnavailable                bool
}

func (a *app) recordPage(w http.ResponseWriter, r *http.Request) {
	sess, scope, src, rec, ok := a.readableRecord(w, r)
	if !ok {
		return
	}
	title := rec.Name
	moneyCompatible := false
	if title == "" || moneyType(rec.Type) {
		if token, err := src.Token(r.Context()); err == nil {
			if metas, err := a.tessera.types(r.Context(), token, src.Dataset); err == nil {
				for _, m := range metas {
					if m.ID == rec.Type {
						if title == "" {
							title = localizeName(m.Name, translator("en"))
						}
						moneyCompatible = catalogueReadCompatible(TypeInfo{ID: rec.Type, Schema: m})
						break
					}
				}
			}
		}
	}
	if title == "" {
		title = "Record"
	}
	if known, ok := homeTrackIdentities[rec.Type]; ok {
		title = known.Name
	}
	d := recordView{BackLabel: "Back to history", Title: title, Member: rec.Member, Back: "/tracks?" + url.Values{"scope": {scope}, "type": {rec.Type}}.Encode()}
	if scope == "home" {
		d.Title = homeEntryTitle(rec, d.Title)
		d.HomeSubjectName, d.HomeSubjectKind = a.homeRecordName(r.Context(), src, rec)
		_, hasRef := valueReference(rec.Values["subject"])
		d.RelatedUnavailable = hasRef && d.HomeSubjectName == ""
	}
	// Return state is data, never a redirect URL. Copy only known filters and
	// force the authorized scope/type so it cannot create an open redirect.
	if state, err := url.ParseQuery(r.URL.Query().Get("return")); err == nil && len(state) > 0 {
		back := url.Values{"scope": {scope}, "type": {rec.Type}}
		for _, key := range []string{"period", "month", "subject", "member", "q", "series", "bucket", "offset", "from", "to"} {
			if v := state.Get(key); v != "" && len(v) <= 2000 {
				back.Set(key, v)
			}
		}
		d.Back = "/tracks?" + back.Encode() + "#records"
	}

	if !rec.When.IsZero() {
		d.When = rec.When.In(a.tz).Format("2 Jan 2006, 15:04 MST")
	}
	for _, f := range rec.Fields {
		if catalogueInternalField(rec.Type, f.ID) {
			continue
		}
		if rec.Values[f.ID] == nil || f.ID == "timestamp" || f.ID == "recorded_at" || f.ID == "subject" || f.ID == "source" || f.ID == "plan" || f.ID == "planPeriod" || f.ID == "planOccurrence" {
			continue
		}
		name := localizeName(f.Name, translator("en"))
		if name == "" {
			name = f.ID
		}
		value := readableValue(f, rec.Values[f.ID])
		if f.ID == "amount" && f.Type == "integer" && moneyType(rec.Type) && moneyCompatible {
			if v, e := rat(rec.Values[f.ID]); e == nil && v != nil {
				value = fmt.Sprint(rec.Values["currency"]) + " " + moneyMajor(v).FloatString(2)
			}
		}
		if f.ID == "amount" && moneyType(rec.Type) && !moneyCompatible {
			name += " (stored value)"
		}
		d.Fields = append(d.Fields, recordFieldView{name, value})
	}
	if raw := r.URL.Query().Get("home_return"); scope == "home" && raw != "" && len(raw) <= 4096 {
		if state, err := url.ParseQuery(raw); err == nil {
			v := url.Values{}
			for _, key := range []string{"view", "period", "month", "from", "to", "member", "q", "subject", "spend", "currency", "bucket", "offset"} {
				if value := state.Get(key); value != "" && len(value) <= 2000 {
					v.Set(key, value)
				}
			}
			d.Back = "/home?" + v.Encode() + "#activity"
			d.BackLabel = "Back to Home"
		}
	}
	d.Attachments, d.AttachmentUnavailable = a.recordAttachments(r.Context(), scope, src, rec)
	a.render(w, r, sess, "record", 200, d)
}
func pageOffset(r *http.Request, max int) int {
	n, e := strconv.Atoi(r.URL.Query().Get("offset"))
	if e != nil || n < 0 || n > max {
		return 0
	}
	return n
}
