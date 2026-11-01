package metadata

import (
	"reflect"
	"testing"
)

func TestParseNamesABS(t *testing.T) { // test/server/utils/parsers/parseNameString.test.js
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"John Smith", []string{"John Smith"}},
		{"Smith, John", []string{"John Smith"}},
		{"John Smith & Jane Doe", []string{"John Smith", "Jane Doe"}},
		{"John Smith and Jane Doe", []string{"John Smith", "Jane Doe"}},
		{"John Smith, Jane Doe and John Doe", []string{"John Smith", "Jane Doe", "John Doe"}},
		{"John Smith; Jane Doe", []string{"John Smith", "Jane Doe"}},
		{"Smith, John, Doe, Jane", []string{"John Smith", "Jane Doe"}},
		{"John Smith, Jones, James Doe, Ludwig von Mises", []string{"John Smith", "Jones", "James Doe", "Ludwig von Mises"}},
		{"Jones; John Smith; James Doe; Ludwig von Mises", []string{"Jones", "John Smith", "James Doe", "Ludwig von Mises"}},
		{"Smith, John Jr.", []string{"John Jr. Smith"}},
		{"von Mises, Ludwig", []string{"Ludwig von Mises"}},
		{"张三, 李四", []string{"张三", "李四"}},
		{"John Smith & John Smith", []string{"John Smith"}},
		{"John Smith,", []string{"John Smith"}},
	}
	for _, c := range cases {
		if got := ParseNames(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseNames(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSortNameABS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"John Smith", "Smith, John"},
		{"Smith", "Smith"},
		{"John Middle Smith", "Smith, John Middle"},
		{"Ludwig von Mises", "von Mises, Ludwig"},
		{"von Mises", "von Mises"},
		{"  ", ""},
		{"张三", "张三"},
	}
	for _, c := range cases {
		if got := SortName(c.in); got != c.want {
			t.Errorf("SortName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseNamesExtractor(t *testing.T) { // tests/test_details.py test_names_series_genres
	cases := []struct {
		in   string
		want []string
	}{
		{"Smith, Jon & Smith, Rose", []string{"Jon Smith", "Rose Smith"}},
		{"Max Meyers, Brittany Pressley", []string{"Max Meyers", "Brittany Pressley"}},
		{"A One and B Two", []string{"A One", "B Two"}},
	}
	for _, c := range cases {
		if got := ParseNames(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseNames(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseSeriesString(t *testing.T) {
	cases := []struct {
		in, name, seq string
		ok            bool
	}{
		{"Moon Series #1a", "Moon Series", "1a", true},
		{"Moon Series", "Moon Series", "", true},
		{"  ", "", "", false},
		{"A #1 #2", "A #1", "2", true},
	}
	for _, c := range cases {
		name, seq, ok := ParseSeriesString(c.in)
		if name != c.name || seq != c.seq || ok != c.ok {
			t.Errorf("ParseSeriesString(%q) = %q, %q, %v", c.in, name, seq, ok)
		}
	}
}
