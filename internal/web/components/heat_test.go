package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHeatGeometry(t *testing.T) {
	// Weeks layout: a cell sits at week*12, weekday*12; the viewBox is 12 units a column by 7 rows.
	if x, y := heatWeeksXY(HeatCell{Week: 52, Weekday: 6}); x != 624 || y != 72 {
		t.Errorf("weeks xy = %d,%d", x, y)
	}
	for weeks, want := range map[int]string{53: "0 0 636 84", 54: "0 0 648 84"} {
		if got := (HeatGridView{Weeks: weeks}).weeksViewBox(); got != want {
			t.Errorf("%d weeks viewBox = %q, want %q", weeks, got, want)
		}
	}
	// Months layout: 31 December is x 360, y 176; 31 columns by 12 rows.
	if x, y := heatMonthsXY(HeatCell{Month: 11, Day: 31}); x != 360 || y != 176 {
		t.Errorf("months xy = %d,%d", x, y)
	}
	if x, y := heatMonthsXY(HeatCell{Month: 0, Day: 1}); x != 0 || y != 0 {
		t.Errorf("1 Jan xy = %d,%d", x, y)
	}
	if got := heatMonthsViewBox(); got != "0 0 372 192" {
		t.Errorf("months viewBox = %q", got)
	}
}

func TestHeatMarkup(t *testing.T) {
	v := HeatGridView{
		Weeks: 2, WeekLabels: []string{"Jan", ""},
		Cells: []HeatCell{{Week: 0, Weekday: 4, Month: 0, Day: 1, Level: 2, Title: "Thu 1 Jan: 20 m"}},
		Key:   []HeatKey{{0, "No listening"}, {4, "1 h 30 m or more"}}, Caption: "Listening by month",
		Heads: []string{"Month", "Days you listened", "Time"}, Rows: []ChartRow{{Head: "January", Cells: []string{"1", "20 m"}}},
	}
	var buf bytes.Buffer
	if err := HeatGrid(v).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`class="ts-heat ts-heat--weeks"`, `class="ts-heat ts-heat--months"`, `viewBox="0 0 24 84"`, `viewBox="0 0 372 192"`,
		`<rect class="ts-heat__c2" x="0" y="48" width="10" height="10" rx="2"><title>Thu 1 Jan: 20 m</title></rect>`,
		`<rect class="ts-heat__c2" x="0" y="0" width="10" height="10" rx="2">`,
		`<span>Mon</span>`, `<span>Dec</span>`, `<span>31</span>`,
		`class="ts-heat__swatch ts-heat__c4"`, `<caption>Listening by month</caption>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("heat grid lacks %q", want)
		}
	}
	// Month labels over the 31 columns: anchors at 1, 10, 20 and 31 only.
	cols := out[strings.Index(out, `ts-heat--months`):]
	cols = cols[:strings.Index(cols, `ts-heat__rows`)]
	if n := strings.Count(cols, "<span></span>"); n != 27 {
		t.Errorf("%d blank month columns, want 27", n)
	}
	if strings.Contains(out, "style=") || strings.Contains(out, "<text") || strings.Contains(out, "tabindex") {
		t.Error("heat grid has a style attribute, SVG text or a tab stop")
	}
}
