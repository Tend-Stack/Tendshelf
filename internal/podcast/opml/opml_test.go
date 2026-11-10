package opml

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseNested(t *testing.T) {
	in := `<?xml version="1.0"?><opml version="2.0"><head><title>x</title></head><body>
	<outline text="News"><outline type="rss" text="One" xmlUrl="https://a.example/1.xml"/>
	  <outline text="Deeper"><outline type="rss" title="Two" text="ignored" xmlUrl=" https://a.example/2.xml "/></outline></outline>
	<outline type="rss" text="Dup" xmlUrl="https://a.example/1.xml"/>
	<outline text="Folder only"/>
	<outline type="link" text="Any type" xmlurl="https://a.example/3.xml"/>
	<outline text="Café &amp; Co" xmlUrl="https://a.example/4.xml?a=1&amp;b=2"/></body></opml>`
	got, err := Parse(strings.NewReader(in))
	want := []Outline{{"One", "https://a.example/1.xml"}, {"Two", "https://a.example/2.xml"}, {"Any type", "https://a.example/3.xml"}, {"Café & Co", "https://a.example/4.xml?a=1&b=2"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v\n got %+v\nwant %+v", err, got, want)
	}
}

func TestParseCuts201(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<opml><body>`)
	for i := 0; i < 201; i++ {
		fmt.Fprintf(&b, `<outline text="s%d" xmlUrl="https://a.example/%d.xml"/>`, i, i)
	}
	b.WriteString(`</body></opml>`)
	got, err := Parse(strings.NewReader(b.String()))
	if err != nil || len(got) != 200 || got[199].Title != "s199" {
		t.Fatalf("%v %d", err, len(got))
	}
}

func TestParseRefusals(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "html": "<html></html>", "text": "nope", "json": "{}"} {
		if _, err := Parse(strings.NewReader(in)); !errors.Is(err, ErrNotOPML) {
			t.Errorf("%s: %v", name, err)
		}
	}
	big := `<opml><body>` + strings.Repeat(`<outline text="x"/>`, 80000) + `</body></opml>`
	if _, err := Parse(strings.NewReader(big)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	in := []Outline{{"A & B \"quoted\" <x>", "https://a.example/f?a=1&b=2"}, {"Café", "http://b.example/rss"}}
	var buf bytes.Buffer
	if err := Write(&buf, "My <shows>", in); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "<?xml") || !strings.Contains(buf.String(), `version="2.0"`) {
		t.Errorf("%s", buf.String())
	}
	got, err := Parse(&buf)
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Fatalf("%v %+v", err, got)
	}
}
