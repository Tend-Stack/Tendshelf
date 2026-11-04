package components

import "fmt"

// The calendar heat grid of the listening dashboard. Two layouts of the same cells are rendered and CSS
// shows one: weeks as columns (768 px and up) and months as rows (below 768 px), so the year always
// fits the card and nothing scrolls. Like the bar chart, the SVG draws squares only.
const (
	heatStep      = 12 // a cell is 10 wide plus a 2 gap, in viewBox units
	heatMonthStep = 16 // a months row is 10 high plus a 6 gap, so a 13 px label has room
	heatWeekRows  = 7
	heatMonthRows = 12
	heatMonthCols = 31
)

// HeatCell is one day: its column and row in the weeks layout, its month (0-11) and day (1-31) in the
// months layout, its level 0-4 and its tooltip title.
type HeatCell struct {
	Week, Weekday, Month, Day, Level int
	Title                            string
}

// HeatKey is one step of the key: a level and its words.
type HeatKey struct {
	Level int
	Label string
}

// HeatGridView is the year's heat grid. WeekLabels has one entry per week column: the month name at the
// column that holds the 1st of the month, "" elsewhere.
type HeatGridView struct {
	Weeks      int
	WeekLabels []string
	Cells      []HeatCell
	Key        []HeatKey
	Caption    string
	Heads      []string
	Rows       []ChartRow
}

// heatWeekdayLabels are the row labels of the weeks layout: Monday, Wednesday and Friday only.
var heatWeekdayLabels = []string{"", "Mon", "", "Wed", "", "Fri", ""}

// heatMonthNames are the row labels of the months layout.
var heatMonthNames = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// heatWeeksXY is the cell's corner in the weeks layout.
func heatWeeksXY(c HeatCell) (x, y int) { return c.Week * heatStep, c.Weekday * heatStep }

// heatMonthsXY is the cell's corner in the months layout.
func heatMonthsXY(c HeatCell) (x, y int) { return (c.Day - 1) * heatStep, c.Month * heatMonthStep }

func (v HeatGridView) weeksViewBox() string {
	return fmt.Sprintf("0 0 %d %d", v.Weeks*heatStep, heatWeekRows*heatStep)
}

func heatMonthsViewBox() string {
	return fmt.Sprintf("0 0 %d %d", heatMonthCols*heatStep, heatMonthRows*heatMonthStep)
}

// heatMonthCol is the label over column day (1-31) of the months layout: a few anchors only.
func heatMonthCol(day int) string {
	switch day {
	case 1, 10, 20, 31:
		return fmt.Sprintf("%d", day)
	}
	return ""
}

func heatLevelClass(level int) string { return fmt.Sprintf("ts-heat__c%d", level) }

func heatXY(f func(HeatCell) (int, int), c HeatCell) (string, string) {
	x, y := f(c)
	return chartInt(x), chartInt(y)
}
