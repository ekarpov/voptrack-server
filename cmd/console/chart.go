package main

import (
	"fmt"
	"math"
	"strings"
)

// Charts are drawn on the server as SVG, from a model this file computes, and
// the template writes the SVG elements -- so every label passes through
// html/template's escaping and no chart needs a script. A household's server
// has no front-end toolchain to keep patched.

type Chart struct {
	Interactive      bool
	Width, Height    int
	LastLabel        string
	Compact          bool
	Line             bool // a time series is a line; categories are bars
	Bars             []Bar
	Path             string // for a line
	Dots             []Dot
	Ticks            []Tick
	AxisY, AxisRight int
	TickX            int
	Baseline         float64
	Empty            bool
	// TooMany: more points than the chart has pixels. It is not drawn --
	// thinning the points would chart something other than the numbers --
	// and the table beside it has every value.
	TooMany bool
}

// maxChartPoints is the chart's drawable width: beyond it, points overlap.
const maxChartPoints = chartW - marginL - marginR

type Bar struct {
	AxisLabel  string
	URL        string
	X, Y, W, H float64
	Label      string
	Value      string
	LabelX     float64
}

type Dot struct {
	Selected bool
	URL      string
	X, Y     float64
	Label    string
	Value    string
}

type Tick struct {
	Y     float64
	Label string
}

const (
	chartW    = 720
	chartH    = 260
	marginL   = 64
	marginR   = 16
	marginTop = 16
	marginBot = 48
)

func buildChart(res Result, line bool) Chart { return buildChartSized(res, line, chartW, chartH) }

func buildCompactChart(res Result, line bool) Chart {
	c := buildChartSized(res, line, 320, 230)
	c.Compact = true
	return c
}

func buildChartSized(res Result, line bool, width, height int) Chart {
	c := Chart{Width: width, Height: height, Line: line, AxisY: marginL, AxisRight: width - marginR, TickX: marginL - 6}
	type pv struct {
		label string
		v     float64
		ok    bool
		text  string
	}
	if len(res.Points) > maxChartPoints {
		c.Empty, c.TooMany = true, true
		return c
	}
	if len(res.Points) > 0 {
		c.LastLabel = res.Points[len(res.Points)-1].Label
	}
	var pts []pv
	visible := false
	lo, hi := 0.0, 0.0
	for _, p := range res.Points {
		r := pointValue(res, p)
		if r == nil {
			pts = append(pts, pv{label: p.Label})
			continue
		}
		visible = true
		f := toFloat(r)
		pts = append(pts, pv{label: p.Label, v: f, ok: true, text: display(r, 2)})
		lo, hi = math.Min(lo, f), math.Max(hi, f)
	}
	if res.Research && !visible {
		c.Empty = true
		return c
	}
	if len(pts) == 0 || hi == lo {
		if len(pts) == 0 {
			c.Empty = true
			return c
		}
		hi = lo + 1
	}
	hi, step := niceScale(lo, hi)
	if res.Spec.Measure == MeasureCount {
		step = math.Max(1, math.Ceil(step))
		hi = math.Ceil(hi/step) * step
	}
	plotH := float64(height - marginTop - marginBot)
	plotW := float64(width - marginL - marginR)
	y := func(v float64) float64 { return marginTop + plotH*(1-(v-lo)/(hi-lo)) }
	for v := lo; v <= hi+step/2; v += step {
		c.Ticks = append(c.Ticks, Tick{Y: y(v), Label: trimFloat(v)})
	}
	c.Baseline = y(math.Max(lo, 0))
	slot := plotW / float64(len(pts))
	for i, p := range pts {
		cx := marginL + slot*float64(i) + slot/2
		if !line {
			if !p.ok {
				continue
			}
			w := math.Min(slot*0.7, 64)
			top, bottom := y(p.v), c.Baseline
			if top > bottom {
				top, bottom = bottom, top
			}
			c.Bars = append(c.Bars, Bar{X: cx - w/2, Y: top, W: w, H: math.Max(bottom-top, 1),
				Label: p.label, Value: p.text, LabelX: cx})
			continue
		}
		if p.ok {
			c.Dots = append(c.Dots, Dot{X: cx, Y: y(p.v), Label: p.label, Value: p.text})
		}
	}
	if line {
		// A gap in the data is a gap in the line: a month with no records is
		// not a month at zero, and joining across it would draw one.
		var path strings.Builder
		pen := "M"
		prev := -1
		for i, p := range pts {
			if !p.ok {
				pen = "M"
				continue
			}
			cx := marginL + slot*float64(i) + slot/2
			if prev == i-1 && pen == "L" {
				fmt.Fprintf(&path, "L%.1f,%.1f ", cx, y(p.v))
			} else {
				fmt.Fprintf(&path, "M%.1f,%.1f ", cx, y(p.v))
			}
			pen, prev = "L", i
		}
		c.Path = path.String()
		for i, p := range pts {
			c.Bars = append(c.Bars, Bar{Label: p.label, LabelX: marginL + slot*float64(i) + slot/2})
		}
	}
	return c
}

// niceScale rounds the axis to steps a person reads at a glance.
func niceScale(lo, hi float64) (float64, float64) {
	span := hi - lo
	raw := span / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := mag
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= m*mag {
			step = m * mag
			break
		}
	}
	return math.Ceil(hi/step) * step, step
}

func trimFloat(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}
