package components

import "fmt"

// The bar chart of the listening dashboard (board ListeningDashboard). The SVG draws bars and lines
// only, stretched to the card (preserveAspectRatio="none"); every word is HTML beside it, so no text
// ever scales. All numbers here are viewBox units: a slot is 10 wide and the plot is 160 high.
const (
	chartSlot   = 10   // width of one bar's slot
	chartHeight = 160  // viewBox height
	chartBase   = 156  // y of the baseline
	chartSpan   = 152  // baseline to the top gridline: the height of the tallest bar
	chartWideN  = 12   // up to this many bars are drawn with the wide stroke (ts-chart--wide)
	chartDot    = 0.01 // a tiny bar still draws a dot: its top sits this far above its bottom
)

// ChartBar is one bar: the x label (and optional icon name), the tooltip title and the value.
type ChartBar struct {
	Label, Icon, Title string
	Ms                 int64
}

// ChartRow is one row of the text-equivalent table: a row head and its cells.
type ChartRow struct {
	Head  string
	Cells []string
}

// BarChartView is a bar chart: the bars, the two tick labels (the rounded maximum and its half), the
// optional night band and edge-aligned labels (Time of day), and the visually hidden table that is
// the chart's text equivalent.
type BarChartView struct {
	Bars               []ChartBar
	MaxMs              int64
	MaxLabel, MidLabel string
	Night, Edge        bool
	Caption            string
	Heads              []string
	Rows               []ChartRow
}

// BarLine is one drawn bar: a vertical line with a round cap, in viewBox units written to two places.
type BarLine struct {
	Title, X, Y1, Y2 string
}

// Wide reports whether the bars are few enough for the wide stroke.
func (v BarChartView) Wide() bool { return len(v.Bars) <= chartWideN }

// Width is the viewBox width.
func (v BarChartView) Width() int { return len(v.Bars) * chartSlot }

// ViewBox is the SVG viewBox attribute value.
func (v BarChartView) ViewBox() string { return fmt.Sprintf("0 0 %d %d", v.Width(), chartHeight) }

// capRadius is the radius of the round cap in viewBox units: the line is shortened by it at both ends so
// the cap, not the line, touches the baseline and the value.
func (v BarChartView) capRadius() float64 {
	if v.Wide() {
		return 6
	}
	return 3
}

// Lines returns the bars to draw. A bar with no time has no line, so an empty day shows only the baseline.
func (v BarChartView) Lines() []BarLine {
	var out []BarLine
	if v.MaxMs <= 0 {
		return out
	}
	c := v.capRadius()
	for i, b := range v.Bars {
		if b.Ms <= 0 {
			continue
		}
		h := min(float64(b.Ms)/float64(v.MaxMs), 1) * chartSpan
		y1 := chartBase - c
		y2 := min(chartBase-h+c, y1-chartDot)
		out = append(out, BarLine{
			Title: b.Title,
			X:     fmt.Sprintf("%d", i*chartSlot+chartSlot/2),
			Y1:    fmt.Sprintf("%.2f", y1),
			Y2:    fmt.Sprintf("%.2f", y2),
		})
	}
	return out
}

// Rects returns the night band as (x, width) pairs: 0 to 6 am and 10 pm to midnight of a 24-bar chart.
func (v BarChartView) Rects() [][2]int {
	if !v.Night {
		return nil
	}
	return [][2]int{{0, 6 * chartSlot}, {22 * chartSlot, 2 * chartSlot}}
}

func chartInt(n int) string { return fmt.Sprintf("%d", n) }
