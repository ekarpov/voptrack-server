package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"
	"time"
)

// Source is one store a dashboard reads, with the credential that reads it.
// A person's own dashboards read their connected stores with their own tokens;
// Home dashboards read each member's store with Home's token, which sees only
// the tracks that member chose to share.
type Source struct {
	Home        bool // check current Home-visible metadata before querying a track
	Dataset     string
	Participant string // stable study account; empty outside Research
	Name        string
	// Cohort is a study participant's consent version ("v2"), for grouping
	// a study's panels by what people agreed to; empty elsewhere.
	Cohort string
	Token  func(context.Context) (string, error)
	// Forget, when set, discards a cached token so the next Token fetches a
	// fresh one: used once when the server refuses a token (401), because a
	// share can change -- and revoke that token -- from outside the Console.
	Forget func()
}

// Spec is one panel's question. It travels in the page's query string, so a
// panel is a link: bookmarkable, shareable within the household, and
// reproducible from the export.
type Spec struct {
	Type     string
	Field    string // numeric field; empty counts records
	Measure  Measure
	Group    string // "store", "cohort", "period", "spread", "none", or "field:<id>"
	Period   string // "day", "week", "month" when Group is "period"
	TZ       *time.Location
	From     time.Time // inclusive
	To       time.Time // exclusive
	Research bool      // internal computation mode, never a query parameter
	Date     string    // the date_time field the window and periods use
}

// Point is one bar or one point on a line.
type Point struct {
	Label         string
	Tally         Tally
	contributors  map[string]bool
	Status        string
	LatestValue   *big.Rat
	ReleasedValue *big.Rat
	Records       string
	Values        string
}

// Result is a panel's answer, with what the reader needs to trust it.
type Result struct {
	Spec        Spec
	Research    bool
	K           int
	Revision    uint64
	Status      string
	Unsupported bool
	calculation string // internal: empty, no_numeric_values, complete, incomplete
	Points      []Point
	Truncated   bool     // a period panel stopped reading at its row limit
	Skipped     []string // stores that could not answer, named
}

// rowLimit bounds a period panel. Tessera will not bucket time, so a period
// panel reads rows; this keeps one from reading a large store whole. When it
// is hit the panel says so rather than showing a partial total as a total.
const rowLimit = 200_000

// ErrUnreadable is a type or field the reader cannot use for this question.
var ErrUnreadable = errors.New("that question does not fit this track")

func (s Spec) window() any {
	if s.Date == "" || (s.From.IsZero() && s.To.IsZero()) {
		return nil
	}
	rng := map[string]any{}
	if !s.From.IsZero() {
		rng["lower"] = map[string]any{"type": "date_time", "value": s.From.UTC().Format("2006-01-02T15:04:05.000Z")}
	}
	if !s.To.IsZero() {
		// The protocol's range is inclusive at both ends; the window is
		// half-open, so the upper bound is the last millisecond before To.
		rng["upper"] = map[string]any{"type": "date_time", "value": s.To.Add(-time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z")}
	}
	return map[string]any{"field": map[string]any{"id": s.Date, "range": rng}}
}

// compute answers a panel over every source. A store that fails is named and
// left out, rather than failing the whole panel or silently counting as zero:
// "Sara's store did not answer" and "Sara recorded nothing" are different facts.
func compute(ctx context.Context, t *Tessera, sources []Source, spec Spec) (res Result, resultErr error) {
	res = Result{Spec: spec}
	var matchedRows int64
	defer func() {
		if !spec.Research {
			return
		}
		if resultErr != nil || res.Truncated || len(res.Skipped) > 0 {
			res.calculation = "incomplete"
			return
		}
		if matchedRows == 0 {
			res.calculation = "empty"
			return
		}
		var values int64
		for _, p := range res.Points {
			values += p.Tally.Values
		}
		if spec.Field != "" && values == 0 && !res.Unsupported {
			res.calculation = "no_numeric_values"
		} else {
			res.calculation = "complete"
		}
	}()
	var spread []*big.Rat // every value, for a distribution
	byKey := map[string]*Point{}
	var order []string
	contribute := func(p *Point, src Source, qualifies bool) {
		if !qualifies || !spec.Research {
			return
		}
		if p.contributors == nil {
			p.contributors = map[string]bool{}
		}
		p.contributors[src.Participant] = true
	}
	point := func(key, label string) *Point {
		if p, ok := byKey[key]; ok {
			return p
		}
		p := &Point{Label: label}
		byKey[key] = p
		order = append(order, key)
		return p
	}
	for _, src := range sources {
		if spec.Research && src.Participant == "" {
			return res, errors.New("unknown participant identity")
		}
		token, err := src.Token(ctx)
		if err != nil {
			res.Skipped = append(res.Skipped, src.Name)
			continue
		}
		if src.Home {
			var metas []Meta
			err := retryRefused(ctx, src, &token, func(tok string) error { var e error; metas, e = t.types(ctx, tok, src.Dataset); return e })
			if err != nil {
				res.Skipped = append(res.Skipped, src.Name)
				continue
			}
			present := false
			for _, m := range metas {
				if m.ID == spec.Type {
					present = true
				}
			}
			// A successful filtered metadata read proves this store does not share
			// this track with Home. It remains a source for its other shared tracks.
			// A failed read above is always reported as incomplete, never as absence.
			if !present {
				continue
			}
		}
		switch {
		case spec.Group == "spread":
			var rows []Row
			var truncated bool
			err := retryRefused(ctx, src, &token, func(tok string) error {
				var e error
				rows, truncated, e = t.rows(ctx, tok, src.Dataset, spec.Type, []string{spec.Field}, spec.window(), rowLimit)
				return e
			})
			if err != nil {
				logSkip("reading rows", src.Dataset, err)
				if skippable(err) || refusedReader(src, err) {
					res.Skipped = append(res.Skipped, src.Name)
					continue
				}
				return res, err
			}
			res.Truncated = res.Truncated || truncated
			matchedRows += int64(len(rows))
			for _, row := range rows {
				if len(row) == 0 {
					continue
				}
				v, err := rat(row[0].Value)
				if err != nil && spec.Research {
					return res, err
				}
				if err == nil && v != nil {
					spread = append(spread, v)
					if spec.Research {
						pp := point(v.RatString(), display(v, 2))
						pp.Tally.Rows++
						pp.Tally.Values++
						contribute(pp, src, true)
					}
				}
			}
		case spec.Group == "period":
			var rows []Row
			var truncated bool
			err := retryRefused(ctx, src, &token, func(tok string) error {
				var e error
				rows, truncated, e = t.rows(ctx, tok, src.Dataset, spec.Type, rowFields(spec), spec.window(), rowLimit)
				return e
			})
			if err != nil {
				logSkip("reading rows", src.Dataset, err)
				if skippable(err) || refusedReader(src, err) {
					res.Skipped = append(res.Skipped, src.Name)
					continue
				}
				return res, err
			}
			res.Truncated = res.Truncated || truncated
			for _, row := range rows {
				at, ok := rowTime(row)
				if !ok {
					continue
				}
				matchedRows++
				key, label := bucket(at, spec.Period, spec.TZ)
				var value *big.Rat
				if spec.Field != "" && len(row) > 1 {
					var e error
					value, e = rat(row[1].Value)
					if e != nil && spec.Research {
						return res, e
					}
				}
				p := point(key, label)
				if spec.Field == "" {
					p.Tally.Rows++
					contribute(p, src, true)
					continue
				}
				p.Tally.addValue(value)
				contribute(p, src, value != nil)
			}
		default:
			groupBy := ""
			if strings.HasPrefix(spec.Group, "field:") {
				groupBy = strings.TrimPrefix(spec.Group, "field:")
			}
			var aggs []Aggregate
			err := retryRefused(ctx, src, &token, func(tok string) error {
				var e error
				aggs, e = t.aggregate(ctx, tok, src.Dataset, spec.Type, spec.Field, groupBy, spec.window())
				return e
			})
			if err != nil {
				logSkip("aggregating", src.Dataset, err)
				if skippable(err) || refusedReader(src, err) {
					res.Skipped = append(res.Skipped, src.Name)
					continue
				}
				return res, err
			}
			for _, a := range aggs {
				key, label := "all", ""
				switch {
				case spec.Group == "store":
					key, label = "store:"+src.Dataset, src.Name
				case spec.Group == "cohort":
					key, label = "cohort:"+src.Cohort, src.Cohort
				case groupBy != "":
					key, label = groupLabel(a.Group)
				}
				p := point(key, label)
				if spec.Research {
					rows, e1 := a.RowCount.Int64()
					values, e2 := a.ValueCount.Int64()
					if e1 != nil || e2 != nil || rows < 0 || values < 0 || values > rows {
						return res, errors.New("invalid aggregate counts")
					}
					if spec.Field != "" && values > 0 {
						for _, raw := range []any{a.Sum, a.Minimum, a.Maximum} {
							v, e := rat(raw)
							if e != nil || v == nil {
								return res, errors.New("incomplete numeric aggregate")
							}
						}
					}
					matchedRows += rows
					contribute(p, src, (spec.Field == "" && rows > 0) || (spec.Field != "" && values > 0))
				}
				if err := p.Tally.addAggregate(a); err != nil {
					return res, err
				}
			}
		}
	}
	if spec.Group == "spread" {
		if !spec.Research {
			res.Points = distribution(spread)
			return res, nil
		}
		if len(order) > distinctBars {
			res.Unsupported = true
			return res, nil
		}
		sort.Slice(order, func(i, j int) bool {
			a, _ := new(big.Rat).SetString(order[i])
			b, _ := new(big.Rat).SetString(order[j])
			return a.Cmp(b) < 0
		})
	}
	if spec.Group == "period" {
		sort.Strings(order) // period keys sort chronologically by construction
	}
	for _, k := range order {
		res.Points = append(res.Points, *byKey[k])
	}
	return res, nil
}

func rowFields(spec Spec) []string {
	if spec.Field == "" {
		return []string{spec.Date}
	}
	return []string{spec.Date, spec.Field}
}

func rowTime(row Row) (time.Time, bool) {
	if len(row) == 0 {
		return time.Time{}, false
	}
	s, ok := row[0].Value.(string)
	if !ok {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	return at, err == nil
}

// bucket places an instant in the household's or study's own calendar. This is
// the decision Tessera leaves to its callers on purpose: where a day ends
// depends on a timezone, and a week on where it starts. Weeks start on Monday,
// as ISO 8601 and every Voptrack locale have it.
func bucket(at time.Time, period string, tz *time.Location) (key, label string) {
	if tz == nil {
		tz = time.UTC
	}
	local := at.In(tz)
	switch period {
	case "day":
		return local.Format("2006-01-02"), local.Format("2006-01-02")
	case "week":
		offset := (int(local.Weekday()) + 6) % 7
		start := time.Date(local.Year(), local.Month(), local.Day()-offset, 0, 0, 0, 0, tz)
		year, week := start.ISOWeek()
		return start.Format("2006-01-02"), fmt.Sprintf("%d-W%02d", year, week)
	default:
		// Numeric labels on purpose: a month name would be English whatever
		// language the person reads the Console in.
		return local.Format("2006-01"), local.Format("2006-01")
	}
}

// groupLabel turns an aggregate group into a key and something to show. A
// reference groups by its whole identity -- two objects agreeing on object_id
// in different types are different groups -- and is shown by object id until
// the Console resolves names.
func groupLabel(raw json.RawMessage) (string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "null", "(none)"
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return "text:" + text, text
	}
	var ref struct {
		MetaID    string `json:"meta_id"`
		DatasetID string `json:"dataset_id"`
		ObjectID  string `json:"object_id"`
	}
	if json.Unmarshal(raw, &ref) == nil && ref.ObjectID != "" {
		return "ref:" + ref.MetaID + "/" + ref.DatasetID + "/" + ref.ObjectID, ref.ObjectID
	}
	return "raw:" + string(raw), string(raw)
}

// retryRefused runs a read once, and if the server refuses the token itself
// (401) and the source can discard a cached token, fetches a fresh one and
// runs it once more. Only an authentication refusal is retried: anything else
// is an answer about the data, not about the credential.
func retryRefused(ctx context.Context, src Source, token *string, read func(string) error) error {
	err := read(*token)
	var api *apiError
	if src.Forget == nil || !errors.As(err, &api) || api.Status != 401 {
		return err
	}
	src.Forget()
	fresh, terr := src.Token(ctx)
	if terr != nil {
		return err
	}
	*token = fresh
	return read(fresh)
}

// logSkip records why a store left a panel, for the operator: the server's
// status and code -- never the store, whose identifier stands for a person,
// nor a name, a type or anything recorded.
func logSkip(what, dataset string, err error) {
	log.Printf("console: a store did not answer while %s: %v", what, err)
}

// refusedReader: a source that reads on somebody's behalf (Home) and is
// refused even with a fresh token may no longer read that store -- its member
// stopped sharing some other way. That store is not answering, not an error.
func refusedReader(src Source, err error) bool {
	var api *apiError
	return src.Forget != nil && errors.As(err, &api) && api.Status == 401
}

func skippable(err error) bool {
	var api *apiError
	if !errors.As(err, &api) {
		return false
	}
	// Only "not here" is skipped: a type this store does not have (404) or
	// one outside the reader's view (403). A 400 means the Console asked
	// wrongly, and is an error -- counting it as an empty store is how a bug
	// in the Console's own paging once showed a month-by-month chart with
	// nothing in it, rather than failing.
	return api.Status == 403 || api.Status == 404
}

// Catalog is what a dashboard can ask about: every type the sources hold,
// with the fields that can be measured, dated and grouped.
type Catalog struct {
	Metadata map[string][]Meta // exact, read-only metadata snapshot by dataset
	Types    []TypeInfo
}

type TypeInfo struct {
	CompiledMetric *valueMetric // immutable presentation compiled once per catalog read
	SchemaConflict bool         // disagreeing installed declarations are never combined silently
	Schema         Meta         // first exact effective declaration, never rewritten
	ID             string
	Name           string
	Numeric        []Field
	Dates          []Field
	Groups         []Field // text and reference fields
	Stores         []string
	Fields         []Field
}

func (c Catalog) find(id string) (TypeInfo, bool) {
	for _, t := range c.Types {
		if t.ID == id {
			return t, true
		}
	}
	return TypeInfo{}, false
}

func catalog(ctx context.Context, t *Tessera, sources []Source) (Catalog, []string) {
	byID := map[string]*TypeInfo{}
	var skipped []string
	metadata := map[string][]Meta{}
	visited := map[string]bool{}
	for _, src := range sources {
		if visited[src.Dataset] {
			continue
		}
		visited[src.Dataset] = true
		token, err := src.Token(ctx)
		if err != nil {
			skipped = append(skipped, src.Name)
			continue
		}
		var metas []Meta
		err = retryRefused(ctx, src, &token, func(tok string) error { var e error; metas, e = t.types(ctx, tok, src.Dataset); return e })
		if err != nil {
			skipped = append(skipped, src.Name)
			continue
		}
		metadata[src.Dataset] = metas
		// Tessera answers effective metadata: a study track's fields include
		// the Track root's, timestamp among them, so each type is complete.
		parents := map[string]bool{}
		for _, m := range metas {
			if m.ParentID != nil {
				parents[*m.ParentID] = true
			}
		}
		for _, m := range metas {
			if appTracks.Internal[m.ID] {
				continue
			}
			// A type others inherit from -- the app's Track root -- is not a
			// track anybody records into; its fields reach dashboards through
			// the tracks that inherit them.
			if parents[m.ID] {
				continue
			}
			info, ok := byID[m.ID]
			if !ok {
				info = &TypeInfo{Schema: m, ID: m.ID, Name: m.Name, Fields: m.Fields}
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
				byID[m.ID] = info
			}
			if !sameReadMeta(info.Schema, m) {
				candidate := TypeInfo{ID: m.ID, Schema: m}
				if !(catalogueReadCompatible(*info) && catalogueReadCompatible(candidate)) {
					info.SchemaConflict = true
				}
			}
			info.Stores = append(info.Stores, src.Name)
		}
	}
	out := Catalog{Metadata: metadata}
	for _, info := range byID {
		reading := metricFor(*info)
		info.CompiledMetric = &reading
		out.Types = append(out.Types, *info)
	}
	sort.Slice(out.Types, func(i, j int) bool {
		return strings.ToLower(out.Types[i].Name) < strings.ToLower(out.Types[j].Name)
	})
	return out, skipped
}

// defaultMeasure picks what a panel shows before anybody chooses. A fixed
// decimal is money, which adds up; a float or integer is usually a reading --
// a weight, a pressure -- where a total means nothing and an average does. It
// is a starting point on a form, and a person changes it with one select.
func defaultMeasure(f Field) Measure {
	if f.Type == "fixed" {
		return MeasureSum
	}
	return MeasureAverage
}

// distribution counts values: one bar per value when there are few distinct
// values (a 1-5 rating), otherwise ten equal ranges between the smallest and
// the largest. Counts are exact; only a range's edges are rounded for its label.
const distinctBars, rangeBars = 12, 10

func distribution(values []*big.Rat) []Point {
	if len(values) == 0 {
		return nil
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Cmp(values[j]) < 0 })
	var distinct []*big.Rat
	for _, v := range values {
		if len(distinct) == 0 || distinct[len(distinct)-1].Cmp(v) != 0 {
			distinct = append(distinct, v)
		}
	}
	var out []Point
	if len(distinct) <= distinctBars {
		for _, d := range distinct {
			p := Point{Label: display(d, 2)}
			for _, v := range values {
				if v.Cmp(d) == 0 {
					p.Tally.Rows++
					p.Tally.Values++
				}
			}
			out = append(out, p)
		}
		return out
	}
	lo, hi := values[0], values[len(values)-1]
	width := new(big.Rat).Quo(new(big.Rat).Sub(hi, lo), big.NewRat(rangeBars, 1))
	for i := 0; i < rangeBars; i++ {
		from := new(big.Rat).Add(lo, new(big.Rat).Mul(width, big.NewRat(int64(i), 1)))
		to := new(big.Rat).Add(from, width)
		p := Point{Label: display(from, 2) + "–" + display(to, 2)}
		for _, v := range values {
			// Each range holds [from, to); the last also holds the largest value.
			if v.Cmp(from) >= 0 && (v.Cmp(to) < 0 || (i == rangeBars-1 && v.Cmp(hi) == 0)) {
				p.Tally.Rows++
				p.Tally.Values++
			}
		}
		out = append(out, p)
	}
	return out
}
