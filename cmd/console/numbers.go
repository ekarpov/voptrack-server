package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Aggregation across stores is done here, exactly. Tessera answers each store
// separately and keeps no query across stores, so the Console combines the
// answers -- and a combination is only worth showing if it is the number a
// single store holding all the rows would have given.
//
// Every value is a big.Rat. An integer sum may exceed what a float holds
// exactly, and a fixed-decimal sum arrives as a string precisely so that it
// never passes through one.

// rat reads a number as Tessera sends it: a JSON number (kept as json.Number),
// or a string for fixed decimals. nil means the protocol said null, which is
// "no values", never zero.
func rat(v any) (*big.Rat, error) {
	var text string
	switch n := v.(type) {
	case nil:
		return nil, nil
	case json.Number:
		text = n.String()
	case string:
		text = n
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(n) == nil {
			return nil, errors.New("a value is not a finite number")
		}
		return r, nil
	default:
		return nil, fmt.Errorf("not a number: %T", v)
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, errors.New("a value is not a number") // never the value: it is a record
	}
	return r, nil
}

func count(v json.Number) int64 {
	n, _ := v.Int64()
	return n
}

// Measure is what a panel asks of a numeric field.
type Measure string

const (
	MeasureLatest  Measure = "latest"
	MeasureCount   Measure = "count"
	MeasureSum     Measure = "sum"
	MeasureAverage Measure = "average"
	MeasureMinimum Measure = "minimum"
	MeasureMaximum Measure = "maximum"
)

func validMeasure(m string) bool {
	switch Measure(m) {
	case MeasureCount, MeasureSum, MeasureAverage, MeasureMinimum, MeasureMaximum:
		return true
	}
	return false
}

// Tally accumulates what every measure needs, so combining is always exact:
// an average is the combined sum over the combined count, never an average of
// averages, which would weigh a store with three records like one with three
// thousand.
type Tally struct {
	Rows   int64
	Values int64
	Sum    *big.Rat
	Min    *big.Rat
	Max    *big.Rat
}

func (t *Tally) addAggregate(a Aggregate) error {
	t.Rows += count(a.RowCount)
	t.Values += count(a.ValueCount)
	sum, err := rat(a.Sum)
	if err != nil {
		return err
	}
	min, err := rat(a.Minimum)
	if err != nil {
		return err
	}
	max, err := rat(a.Maximum)
	if err != nil {
		return err
	}
	t.merge(sum, min, max)
	return nil
}

func (t *Tally) addValue(v *big.Rat) {
	t.Rows++
	if v == nil {
		return
	}
	t.Values++
	t.merge(v, v, v)
}

func (t *Tally) merge(sum, min, max *big.Rat) {
	if sum != nil {
		if t.Sum == nil {
			t.Sum = new(big.Rat)
		}
		t.Sum.Add(t.Sum, sum)
	}
	if min != nil && (t.Min == nil || min.Cmp(t.Min) < 0) {
		t.Min = new(big.Rat).Set(min)
	}
	if max != nil && (t.Max == nil || max.Cmp(t.Max) > 0) {
		t.Max = new(big.Rat).Set(max)
	}
}

// Value answers a measure, or nil when there is nothing to answer with -- a
// period with no records is a gap on a chart, not a zero.
func (t *Tally) Value(m Measure, field string) *big.Rat {
	switch m {
	case MeasureCount:
		if field == "" {
			return new(big.Rat).SetInt64(t.Rows)
		}
		return new(big.Rat).SetInt64(t.Values)
	case MeasureSum:
		return t.Sum
	case MeasureAverage:
		if t.Values == 0 || t.Sum == nil {
			return nil
		}
		return new(big.Rat).Quo(t.Sum, new(big.Rat).SetInt64(t.Values))
	case MeasureMinimum:
		return t.Min
	case MeasureMaximum:
		return t.Max
	}
	return nil
}

// display renders a number for a person: exact where it is short, rounded to
// the number of places the question needs where it is not. The export keeps
// full precision; this is only what fits in a table cell.
func display(r *big.Rat, places int) string {
	if r == nil {
		return "—"
	}
	if r.IsInt() {
		return r.Num().String()
	}
	s := r.FloatString(places)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// exact renders a number for an export: an integer as it is, anything else to
// twelve places, which is past the resolution of any Voptrack unit.
func exact(r *big.Rat) string {
	if r == nil {
		return ""
	}
	if r.IsInt() {
		return r.Num().String()
	}
	s := r.FloatString(12)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

func toFloat(r *big.Rat) float64 {
	if r == nil {
		return 0
	}
	f, _ := r.Float64()
	return f
}
