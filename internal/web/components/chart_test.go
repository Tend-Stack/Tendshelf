package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func bars(n int, ms ...int64) []ChartBar {
	out := make([]ChartBar, n)
	for i := range out {
		out[i] = ChartBar{Label: "x", Title: "t"}
		if i < len(ms) {
			out[i].Ms = ms[i]
		}
	}
	return out
}

// TestBarChartGeometry: slot 10, base 156, top 4, cap 6 up to 12 bars and 3 above; no line for zero;
// a tiny value is a dot one hundredth tall; the maximum reaches 4 + cap.
func TestBarChartGeometry(t *testing.T) {
	for _, c := range []struct {
		name string
		v    BarChartView
		want []BarLine
	}{
		{"zero draws nothing", BarChartView{Bars: bars(7, 0), MaxMs: 100}, nil},
		{"tiny is a dot", BarChartView{Bars: bars(7, 1), MaxMs: 1_000_000}, []BarLine{{Title: "t", X: "5", Y1: "150.00", Y2: "149.99"}}},
		{"max, 12 bars, cap 6", BarChartView{Bars: bars(12, 0, 100), MaxMs: 100}, []BarLine{{Title: "t", X: "15", Y1: "150.00", Y2: "10.00"}}},
		{"max, 13 bars, cap 3", BarChartView{Bars: bars(13, 0, 100), MaxMs: 100}, []BarLine{{Title: "t", X: "15", Y1: "153.00", Y2: "7.00"}}},
		{"half", BarChartView{Bars: bars(7, 50), MaxMs: 100}, []BarLine{{Title: "t", X: "5", Y1: "150.00", Y2: "86.00"}}},
		{"no maximum", BarChartView{Bars: bars(7, 5), MaxMs: 0}, nil},
	} {
		got := c.v.Lines()
		if len(got) != len(c.want) {
			t.Errorf("%s: %d lines, want %d", c.name, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: line %d = %+v, want %+v", c.name, i, got[i], c.want[i])
			}
		}
	}
	if !(BarChartView{Bars: bars(12)}).Wide() || (BarChartView{Bars: bars(13)}).Wide() {
		t.Error("Wide is true up to 12 bars only")
	}
	if got := (BarChartView{Bars: bars(24)}).ViewBox(); got != "0 0 240 160" {
		t.Errorf("24 bars viewBox = %q", got)
	}
	night := BarChartView{Bars: bars(24), Night: true}.Rects()
	if len(night) != 2 || night[0] != [2]int{0, 60} || night[1] != [2]int{220, 20} {
		t.Errorf("night rects = %v", night)
	}
	if (BarChartView{Bars: bars(24)}).Rects() != nil {
		t.Error("a chart without Night draws no band")
	}
}

func TestBarChartMarkup(t *testing.T) {
	v := BarChartView{
		Bars:     []ChartBar{{Label: "S", Title: "Sunday: 1 h", Ms: 3_600_000}, {Label: "M", Icon: "sun"}},
		MaxMs:    7_200_000,
		MaxLabel: "2 h", MidLabel: "1 h", Caption: "Listening by day", Heads: []string{"Day", "Time"},
		Rows: []ChartRow{{Head: "Sunday", Cells: []string{"1 h 0 m"}}},
	}
	var buf bytes.Buffer
	if err := BarChart(v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`class="ts-chart ts-chart--wide"`, `<span class="ts-chart__tick">2 h</span>`, `<span class="ts-chart__tick">1 h</span>`,
		`aria-hidden="true" focusable="false"`, `<title>Sunday: 1 h</title>`, `preserveAspectRatio="none"`,
		`<div class="ts-sr"><table><caption>Listening by day</caption>`, `<th scope="col">Day</th>`, `<th scope="row">Sunday</th><td>1 h 0 m</td>`,
		`class="ts-icon"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chart lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "style=") || strings.Contains(out, "<text") {
		t.Error("chart has a style attribute or SVG text")
	}
	if strings.Count(out, `class="ts-chart__bar"`) != 1 {
		t.Errorf("want one bar line (the empty bar has none):\n%s", out)
	}
}
