package align

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// normT turns "word" or "word." into WordSpans (a trailing dot sets DotAfter) and returns the units as
// "norm" strings, "*" for a wildcard, "src:" prefixed when a source index is checked.
func normT(lang string, in ...string) []string {
	var spans []ebook.WordSpan
	for _, w := range in {
		dot := strings.HasSuffix(w, ".")
		spans = append(spans, ebook.WordSpan{Text: strings.TrimSuffix(w, "."), DotAfter: dot})
	}
	var out []string
	for _, u := range Normalize(lang, spans) {
		if u.Wildcard {
			out = append(out, "*")
		} else {
			out = append(out, u.Norm)
		}
	}
	return out
}

func TestNormalizeEnglish(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"21", "twenty one"},
		{"3rd", "third"},
		{"21st", "twenty first"},
		{"12th", "twelfth"},
		{"40th", "fortieth"},
		{"100", "one hundred"},
		{"121", "one hundred twenty one"},
		{"0", "zero"},
		{"12345", "twelve thousand three hundred forty five"},
		{"1984", "*"},
		{"007", "*"},
		{"3.5", "*"},
		{"5k", "*"},
		{"Mr.", "mister"},
		{"Mrs.", "missus"},
		{"Dr.", "doctor"},
		{"vs.", "versus"},
		{"etc.", "et cetera"},
		{"St.", "*"},
		{"Mr", "mr"},
		{"St", "st"},
		{"Don’t", "don't"},
		{"well-known", "well known"},
		{"A—B", "a b"},
		{"naïve", "naïve"},
		{"—", "*"},
	} {
		got := strings.Join(normT("en", c.in), " ")
		if got != c.want {
			t.Errorf("%q -> %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeSpanish(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"121", "ciento veintiuno"},
		{"100", "ciento"},
		{"21", "veintiuno"},
		{"31", "treinta y uno"},
		{"1000", "*"},
		{"2000", "*"},
		{"1500000", "uno millón quinientos mil"},
		{"21000", "veintiuno mil"},
		{"999", "novecientos noventa y nueve"},
		{"2000000", "dos millón"},
		{"1º", "*"},
		{"2ª", "*"},
		{"un", "uno"},
		{"una", "uno"},
		{"veintiún", "veintiuno"},
		{"cien", "ciento"},
		{"quinientas", "quinientos"},
		{"millones", "millón"},
		{"Sr.", "señor"},
		{"Sra.", "señora"},
		{"Dr.", "doctor"},
		{"Dra.", "doctora"},
		{"Ud.", "usted"},
		{"Uds.", "ustedes"},
		{"etc.", "etcétera"},
		{"Sr", "sr"},
		{"naïve", "naïve"},
		{"Niño", "niño"},
	} {
		got := strings.Join(normT("es", c.in), " ")
		if got != c.want {
			t.Errorf("%q -> %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeOtherLanguage(t *testing.T) {
	if got := normT("fr", "Mr.", "21", "L’Été"); !reflect.DeepEqual(got, []string{"mr", "21", "l'été"}) {
		t.Errorf("got %q", got)
	}
	if got := normT("en-GB", "21"); !reflect.DeepEqual(got, []string{"twenty", "one"}) {
		t.Errorf("region tag: got %q", got)
	}
}

func TestNormalizeSources(t *testing.T) {
	units := Normalize("en", []ebook.WordSpan{{Text: "a-b"}, {Text: "1984"}, {Text: "etc", DotAfter: true}, {Text: "go"}})
	var src []int
	for _, u := range units {
		src = append(src, u.Src)
	}
	if want := []int{0, 0, 1, 2, 2, 3}; !reflect.DeepEqual(src, want) {
		t.Errorf("Src = %v, want %v", src, want)
	}
	if !units[2].Wildcard || units[2].Norm != "" {
		t.Errorf("wildcard unit = %+v", units[2])
	}
}
