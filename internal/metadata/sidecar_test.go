package metadata

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSidecarParseNFO(t *testing.T) {
	full := "Title: The Tide: A Subtitle\nAuthor: Nfo Author\nNarrator: Dee Narr, Ex Tra\nSeries Name: Nfo Series\nPosition in Series: 7\n" +
		"Abridged: no\nASIN: b0015t963c\nPublisher: Nfo Pub\nCopyright: 1999\nRelease Date: 06-01-2004\nGenre: Fantasy, Epic\nLanguage: English\n" +
		"Book Description\n=====\nNfo says dramatized.\n\nSecond line.\n"
	tests := []struct {
		name string
		in   string
		want Details
	}{
		{"empty", "", Details{}},
		{"no colon lines", "just words\nmore words", Details{}},
		{"full", full, Details{
			Title: "The Tide", Subtitle: "A Subtitle", Authors: []string{"Nfo Author"}, Narrators: []string{"Dee Narr", "Ex Tra"},
			Series: []SeriesRef{{Name: "Nfo Series", Sequence: "7"}}, Abridged: boolp(false), ASIN: "B0015T963C", Publisher: "Nfo Pub",
			Year: 2004, Genres: []string{"Fantasy", "Epic"}, Language: "English", Description: "Nfo says dramatized. Second line.",
		}},
		{"position before name", "Position in Series: 2\nSeries Name: S\n", Details{Series: []SeriesRef{{Name: "S", Sequence: "2"}}}},
		{"unabridged yes", "Unabridged: Yes", Details{Abridged: boolp(false)}},
		{"unabridged no", "Unabridged: No", Details{Abridged: boolp(true)}},
		{"abridged yes", "Abridged: yes", Details{Abridged: boolp(true)}},
		{"blank value skipped", "Title:   \nAuthor: A B", Details{Authors: []string{"A B"}}},
		{"title without subtitle", "Title: Plain", Details{Title: "Plain"}},
		{"isbn keys", "ISBN-13: 978-1-234-56789-7", Details{ISBN: "9781234567897"}},
		{"bad asin dropped", "ASIN: nope", Details{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNFO(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseNFO = %+v\nwant     %+v", got, tc.want)
			}
		})
	}
}

// The XML of the Extractor's test_details.py precedence test.
const extractorOPF = `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf"><metadata ` +
	`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">` +
	`<dc:title>Opf Title</dc:title><dc:creator opf:role="aut">Opf Author</dc:creator>` +
	`<dc:creator opf:role="nrt">Opf Narr One</dc:creator><dc:creator opf:role="nrt">Opf Narr Two</dc:creator>` +
	`<dc:publisher>Opf Pub</dc:publisher><dc:date>2021-06-01</dc:date><dc:language>en</dc:language>` +
	`<dc:identifier opf:scheme="ISBN">9781234567897</dc:identifier><dc:subject>Romance</dc:subject>` +
	`<meta name="calibre:series" content="Opf Series"/><meta name="calibre:series_index" content="4"/>` +
	`</metadata></package>`

func opfDoc(body string) string {
	return `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf"><metadata ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">` + body + `</metadata></package>`
}

func TestSidecarParseOPF(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Details
	}{
		{"extractor fixture", extractorOPF, Details{
			Title: "Opf Title", Authors: []string{"Opf Author"}, Narrators: []string{"Opf Narr One", "Opf Narr Two"},
			Publisher: "Opf Pub", Year: 2021, Language: "en", ISBN: "9781234567897", Genres: []string{"Romance"},
			Series: []SeriesRef{{Name: "Opf Series", Sequence: "4"}},
		}},
		{"refines narrator", opfDoc(`<dc:creator id="c1">Some One</dc:creator><dc:creator id="c2">Au Thor</dc:creator>` +
			`<meta refines="#c1" property="role">nrt</meta>`), Details{Authors: []string{"Au Thor"}, Narrators: []string{"Some One"}}},
		{"calibre narrators json", opfDoc(`<dc:creator>Au Thor</dc:creator>` +
			`<meta name="calibre:user_metadata:#narrators" content="{&quot;#value#&quot;: [&quot;N One&quot;, &quot;N Two&quot;]}"/>`),
			Details{Authors: []string{"Au Thor"}, Narrators: []string{"N One", "N Two"}}},
		{"urn isbn and asin", opfDoc(`<dc:identifier>urn:isbn:978-1-234-56789-7</dc:identifier><dc:identifier opf:scheme="asin">B0015T963C</dc:identifier>`),
			Details{ISBN: "9781234567897", ASIN: "B0015T963C"}},
		{"unknown role ignored", opfDoc(`<dc:creator opf:role="ill">Drawer</dc:creator>`), Details{}},
		{"series index far away", opfDoc(`<meta name="calibre:series" content="S"/><dc:subject>x</dc:subject><meta name="other" content="y"/><meta name="calibre:series_index" content="2.0"/>`),
			Details{Series: []SeriesRef{{Name: "S", Sequence: "2"}}, Genres: []string{"x"}}},
		{"description html", opfDoc(`<dc:description>&lt;p&gt;Hello   &lt;b&gt;there&lt;/b&gt;&lt;/p&gt;</dc:description>`), Details{Description: "Hello there"}},
		{"duplicate subjects", opfDoc(`<dc:subject>A</dc:subject><dc:subject>B</dc:subject><dc:subject>A</dc:subject>`), Details{Genres: []string{"A", "B"}}},
		{"malformed xml", `<package><metadata><dc:title>x</metadata>`, Details{}},
		{"no metadata element", `<package/>`, Details{}},
		{"empty", ``, Details{}},
		{"external entity is not resolved", `<?xml version="1.0"?><!DOCTYPE p [<!ENTITY x SYSTEM "file:///etc/passwd">]><package><metadata><title>&x;</title></metadata></package>`, Details{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseOPF([]byte(tc.in)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseOPF = %+v\nwant      %+v", got, tc.want)
			}
		})
	}
}

func TestSidecarParseMetadataJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Details
	}{
		{"flat", `{"title":"Json Title","subtitle":"Sub","authors":["Json Author"],"narrators":["Json N"],"series":["Json Series #9"],
			"publishedYear":"2015","abridged":true,"description":"From json.","genres":["A","B"],"publisher":"P","isbn":"978-1-234-56789-7","asin":"B0015T963C","language":"en"}`,
			Details{Title: "Json Title", Subtitle: "Sub", Authors: []string{"Json Author"}, Narrators: []string{"Json N"},
				Series: []SeriesRef{{Name: "Json Series", Sequence: "9"}}, Year: 2015, Abridged: boolp(true), Description: "From json.",
				Genres: []string{"A", "B"}, Publisher: "P", ISBN: "9781234567897", ASIN: "B0015T963C", Language: "en"}},
		{"nested metadata", `{"metadata":{"title":"Nested","authors":["N Author"],"publishedYear":2001},"title":"Outer"}`,
			Details{Title: "Nested", Authors: []string{"N Author"}, Year: 2001}},
		{"abridged string true", `{"abridged":"true"}`, Details{Abridged: boolp(true)}},
		{"abridged string false", `{"abridged":"False"}`, Details{Abridged: boolp(false)}},
		{"abridged junk", `{"abridged":"maybe"}`, Details{}},
		{"published date fallback", `{"publishedDate":"2019-03-04"}`, Details{Year: 2019}},
		{"series without number", `{"series":["Just A Name"]}`, Details{Series: []SeriesRef{{Name: "Just A Name"}}}},
		{"blank list entries dropped", `{"authors":["", "  ", "A B", 3]}`, Details{Authors: []string{"A B"}}},
		{"bool is not text", `{"title":true}`, Details{}},
		{"array", `[1,2]`, Details{}},
		{"string", `"x"`, Details{}},
		{"not json", `{nope`, Details{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseMetadataJSON([]byte(tc.in)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseMetadataJSON = %+v\nwant            %+v", got, tc.want)
			}
		})
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSidecarFindInFolder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"Metadata.JSON", "DESC.txt", "reader.txt", "b.opf", "a.OPF", "z.nfo", "c.nfo", "tide.m4b", "notes.txt"} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	got := findSidecars(dir, false)
	want := sidecarFiles{
		JSON: filepath.Join(dir, "Metadata.JSON"), Desc: filepath.Join(dir, "DESC.txt"), Reader: filepath.Join(dir, "reader.txt"),
		OPF: filepath.Join(dir, "a.OPF"), NFO: filepath.Join(dir, "c.nfo"),
	}
	if got != want {
		t.Fatalf("findSidecars = %+v\nwant %+v", got, want)
	}
	if got := findSidecars(filepath.Join(dir, "missing"), false); got != (sidecarFiles{}) {
		t.Fatalf("missing folder gave %+v", got)
	}
}

func TestSidecarLooseFile(t *testing.T) {
	dir := t.TempDir()
	book := filepath.Join(dir, "My Book.v2.m4b")
	for _, n := range []string{"My Book.v2.m4b", "My Book.v2.opf", "My Book.v2.nfo", "My Book.v2.metadata.json", "cover.opf", "other.nfo", "metadata.json", "desc.txt"} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	got := findSidecars(book, true)
	want := sidecarFiles{
		OPF: filepath.Join(dir, "My Book.v2.opf"), NFO: filepath.Join(dir, "My Book.v2.nfo"), JSON: filepath.Join(dir, "My Book.v2.metadata.json"),
	}
	if got != want {
		t.Fatalf("findSidecars = %+v\nwant %+v", got, want)
	}
}

func TestSidecarLimits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "big.opf"), strings.Repeat("a", sidecarMaxBytes+1))
	writeFile(t, filepath.Join(dir, "real.txt"), "secret")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "desc.txt")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "x.nfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := findSidecars(dir, false)
	if f.Desc != "" || f.NFO != "" {
		t.Fatalf("a symlink or a directory was taken: %+v", f)
	}
	if f.OPF == "" {
		t.Fatal("the oversized file is still listed; only reading skips it")
	}
	if got := readSidecar(f.OPF); got != "" {
		t.Fatalf("read %d bytes of an oversized sidecar", len(got))
	}
	// The same checks hold for a loose file.
	book := filepath.Join(dir, "solo.m4b")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "solo.opf")); err != nil {
		t.Fatal(err)
	}
	if got := findSidecars(book, true); got != (sidecarFiles{}) {
		t.Fatalf("a symlinked loose sidecar was taken: %+v", got)
	}
	if got := readSidecar(filepath.Join(dir, "desc.txt")); got != "" {
		t.Fatalf("read through a symlink: %q", got)
	}
	// A file of exactly 1 MiB is read; bad UTF-8 is replaced.
	writeFile(t, filepath.Join(dir, "edge.txt"), strings.Repeat("a", sidecarMaxBytes))
	if got := readSidecar(filepath.Join(dir, "edge.txt")); len(got) != sidecarMaxBytes {
		t.Fatalf("read %d bytes of a 1 MiB file", len(got))
	}
	writeFile(t, filepath.Join(dir, "bad.txt"), "ok\xffend")
	if got := readSidecar(filepath.Join(dir, "bad.txt")); got != "ok�end" {
		t.Fatalf("invalid UTF-8 became %q", got)
	}
}

func TestSidecarFromSidecars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "desc.txt"), "<p>From   desc.txt, an unabridged edition.</p>")
	writeFile(t, filepath.Join(dir, "reader.txt"), "\n\nReader One\nignored second line")
	got := fromSidecars(findSidecars(dir, false))
	if len(got) != 4 {
		t.Fatalf("keys = %d, want json opf txt nfo", len(got))
	}
	txt := got[SourceTxt]
	if txt.Description != "From desc.txt, an unabridged edition." || !reflect.DeepEqual(txt.Narrators, []string{"Reader One"}) {
		t.Fatalf("txt = %+v", txt)
	}
	if txt.Abridged == nil || *txt.Abridged {
		t.Fatal("unabridged in the description should give abridged=false")
	}
	for _, src := range []string{SourceJSON, SourceOPF, SourceNFO} {
		if len(got[src].Values()) != 0 {
			t.Errorf("%s has values without a file", src)
		}
	}
}

func TestSidecarSignature(t *testing.T) {
	dir := t.TempDir()
	if sig := sidecarSignature(findSidecars(dir, false)); sig != "" {
		t.Fatalf("no sidecars gave %q", sig)
	}
	desc := filepath.Join(dir, "desc.txt")
	writeFile(t, desc, "one")
	base := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(desc, base, base); err != nil {
		t.Fatal(err)
	}
	s1 := sidecarSignature(findSidecars(dir, false))
	if len(s1) != 64 {
		t.Fatalf("signature %q", s1)
	}
	if s2 := sidecarSignature(findSidecars(dir, false)); s2 != s1 {
		t.Fatal("signature is not stable")
	}
	if err := os.Chtimes(desc, base.Add(time.Second), base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	s3 := sidecarSignature(findSidecars(dir, false))
	if s3 == s1 {
		t.Fatal("a new modification time did not change the signature")
	}
	writeFile(t, filepath.Join(dir, "book.nfo"), "Title: x")
	s4 := sidecarSignature(findSidecars(dir, false))
	if s4 == s3 {
		t.Fatal("an added file did not change the signature")
	}
	if err := os.Remove(filepath.Join(dir, "book.nfo")); err != nil {
		t.Fatal(err)
	}
	if sidecarSignature(findSidecars(dir, false)) != s3 {
		t.Fatal("removing the file did not restore the signature")
	}
}
