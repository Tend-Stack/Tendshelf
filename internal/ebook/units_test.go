package ebook

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// slice16 cuts s by UTF-16 offsets.
func slice16(s string, span [2]int) string {
	u := utf16.Encode([]rune(s))
	return string(utf16.Decode(u[span[0]:span[1]]))
}

func TestEbookSegmenter(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"plain", "Hello world. This is it.", []string{"Hello world.", "This is it."}},
		{"mr", "Mr. Smith went home. He slept.", []string{"Mr. Smith went home.", "He slept."}},
		{"dr", "Dr. Ana Ruiz arrived. Then she left.", []string{"Dr. Ana Ruiz arrived.", "Then she left."}},
		{"initials", "J. R. Tolkien wrote it. Many read it.", []string{"J. R. Tolkien wrote it.", "Many read it."}},
		{"ellipsis", "Wait… Then he ran.", []string{"Wait…", "Then he ran."}},
		{"three dots", "Well... Fine, go.", []string{"Well...", "Fine, go."}},
		{"question bang", "Really?! Yes.", []string{"Really?!", "Yes."}},
		{"spanish", "¿Qué quieres? ¡Nada! Sra. López dijo que sí.", []string{"¿Qué quieres?", "¡Nada!", "Sra. López dijo que sí."}},
		{"closing quote", `He said "Stop." Then left.`, []string{`He said "Stop."`, "Then left."}},
		{"curly quote", "She asked, “Why?” He shrugged.", []string{"She asked, “Why?”", "He shrugged."}},
		{"eg", "It costs 5 e.g. apples. Next one.", []string{"It costs 5 e.g. apples.", "Next one."}},
		{"etc", "Pens, etc. Papers follow.", []string{"Pens, etc. Papers follow."}},
		{"lowercase follows", "He paid 3.5 dollars. then left.", []string{"He paid 3.5 dollars. then left."}},
		{"digit follows", "It ended. 1984 began.", []string{"It ended.", "1984 began."}},
		{"no ending", "A fragment without an end", []string{"A fragment without an end"}},
		{"emoji", "Hi 😀. Bye.", []string{"Hi 😀.", "Bye."}},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ss, _ := segmentText(c.in, false)
			var got []string
			for _, s := range ss {
				got = append(got, slice16(c.in, s))
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	ss, ws := segmentText("Hi 😀. Bye.", false)
	if ss[0] != [2]int{0, 6} || ss[1] != [2]int{7, 11} {
		t.Errorf("utf-16 sentence offsets = %v", ss)
	}
	if ws[len(ws)-1] != [2]int{7, 10} {
		t.Errorf("utf-16 word offsets = %v", ws)
	}
	one, _ := segmentText("Chapter One. Part Two.", true)
	if len(one) != 1 {
		t.Errorf("oneSentence = %v", one)
	}
}

func TestEbookWords(t *testing.T) {
	in := "Don’t stop, well-known 3.14 ‘x’ ok- end"
	_, ws := segmentText(in, false)
	var got []string
	for _, w := range ws {
		got = append(got, slice16(in, w))
	}
	want := "Don’t|stop|well-known|3|14|x|ok|end"
	if strings.Join(got, "|") != want {
		t.Errorf("words = %q, want %s", got, want)
	}
}

func TestEbookNormalize(t *testing.T) {
	cases := []struct {
		name, in, want string
		pre            bool
	}{
		{"soft hyphen zwsp bom", "co\u00ADop\u200Ber\uFEFFate", "cooperate", false},
		{"nfc", "é", "é", false},
		{"collapse", "  a \t b\n\n c  ", "a b c", false},
		{"pre keeps", "a  b\n c", "a  b\n c", true},
	}
	for _, c := range cases {
		var sb strings.Builder
		for _, n := range normalizeSegs([]seg{{Text: c.in, ByteOff: -1}}, c.pre) {
			sb.WriteRune(n.r)
		}
		if sb.String() != c.want {
			t.Errorf("%s: %q, want %q", c.name, sb.String(), c.want)
		}
	}
}

// words returns n invented words.
func words(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }

func TestEbookFront(t *testing.T) {
	b := newBuilder("txt", DefaultLimits())
	b.Chapter("A short preface", "", nil)
	b.Block(KindP, 0, []seg{{Text: words(10), ByteOff: -1}}, nil) // front: short, before the first body chapter
	b.Chapter("Chapter 1", "", nil)
	b.Block(KindP, 0, []seg{{Text: words(400), ByteOff: -1}}, nil) // body
	b.Chapter("Contents", "", nil)
	b.Block(KindP, 0, []seg{{Text: words(500), ByteOff: -1}}, nil) // front by title even though long
	b.Chapter("Cover art", "", []string{"cover"})
	b.Block(KindP, 0, []seg{{Text: words(500), ByteOff: -1}}, nil) // front by kind
	b.Chapter("Interlude", "", nil)
	b.Block(KindP, 0, []seg{{Text: words(10), ByteOff: -1}}, nil) // short but after the body starts
	b.Chapter("Índice", "", nil)
	b.Block(KindP, 0, []seg{{Text: "x"}}, nil)
	b.Chapter("Empty", "", nil) // dropped
	d, err := b.Finish(Meta{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range d.Chapters {
		got = append(got, c.Title+"="+map[bool]string{true: "front", false: "body"}[c.Front])
	}
	want := "A short preface=front|Chapter 1=body|Contents=front|Cover art=front|Interlude=body|Índice=front"
	if strings.Join(got, "|") != want {
		t.Errorf("got %v\nwant %s", got, want)
	}
	if d.Chapters[3].Idx != 4 || d.PageCount < 1 {
		t.Errorf("idx %d pages %d", d.Chapters[3].Idx, d.PageCount)
	}
	if _, err := newBuilder("txt", DefaultLimits()).Finish(Meta{}, 0); err == nil {
		t.Error("an empty builder must refuse")
	}
}

func fixtures(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"epub3.epub": ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "The Salt Road", Authors: []string{"Lena Park"}, ISBN: "9780306406157", PageList: 3,
			Chapters: []ebooktest.EPUBChapter{
				{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Salt Road"}},
				{Title: "Departure", Raw: `<h1>Departure</h1><p>The cart left at <em>dawn</em>. Mr. Hale drove; nobody spoke.</p><p>Salt &amp; wind.</p><img src="art/map.png" alt="A map"/>`}}}),
		"epub2.epub":  ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 2, Title: "Old Form", Chapters: []ebooktest.EPUBChapter{{Title: "Only", Paras: []string{"Text here. More text."}}}}),
		"sample.md":   []byte(mdSample),
		"sample.txt":  []byte(txtSample),
		"sample.html": []byte(htmlSample),
		"sample.docx": ebooktest.DOCX(t, ebooktest.DocxOptions{Title: "Ledger", Author: "Pia Roy", Paras: []ebooktest.DocxPara{
			ebooktest.Para("Heading1", "Opening"), {Runs: []ebooktest.DocxRun{{Text: "Cited here."}}, Footnote: 1}}, Footnotes: []string{"A note."}}),
		"sample.fb2": ebooktest.FB2(ebooktest.FB2Options{Title: "Moss", Sections: []ebooktest.FB2Section{{Title: "Rise", Paras: []string{"One. Two."}}}, Notes: []string{"n"}}),
	}
}

func TestEbookDeterministic(t *testing.T) {
	for name, data := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			var shas, docs []string
			for i := 0; i < 2; i++ {
				// A new temp dir (and so a new path) each time: the path must not reach the document.
				d := mustRead(t, name, data)
				js, err := Encode(d)
				if err != nil {
					t.Fatal(err)
				}
				docs = append(docs, string(js))
				shas = append(shas, DocSHA(js))
			}
			if docs[0] != docs[1] || shas[0] != shas[1] {
				t.Error("two reads of the same bytes differ")
			}
			if strings.HasSuffix(docs[0], "\n") {
				t.Error("trailing newline")
			}
		})
	}
}

func TestEbookWriteDoc(t *testing.T) {
	d := mustRead(t, "a.md", []byte(mdSample))
	js, _ := Encode(d)
	dir := filepath.Join(t.TempDir(), "text")
	sha, err := WriteDoc(dir, js)
	if err != nil || sha != DocSHA(js) {
		t.Fatalf("sha %q err %v", sha, err)
	}
	raw, err := os.ReadFile(DocPath(dir, sha))
	if err != nil {
		t.Fatal(err)
	}
	// Zero header: no flags (name, comment), no modification time.
	if raw[3] != 0 || !bytes.Equal(raw[4:8], []byte{0, 0, 0, 0}) {
		t.Errorf("gzip header = % x", raw[:10])
	}
	zr, _ := gzip.NewReader(bytes.NewReader(raw))
	back, _ := io.ReadAll(zr)
	if !bytes.Equal(back, js) {
		t.Error("round trip differs")
	}
	// Written twice gives the same bytes and keeps the first file.
	if _, err := WriteDoc(dir, js); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(DocPath(dir, sha))
	if !bytes.Equal(raw, raw2) {
		t.Error("second write changed the file")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("%d files in the text dir", len(ents))
	}
	var back2 Doc
	if err := json.Unmarshal(back, &back2); err != nil || back2.Chapters[0].Blocks[0].ID != "c1.p1" {
		t.Errorf("decode: %v", err)
	}
}

func TestEbookGolden(t *testing.T) {
	fx := fixtures(t)
	for _, name := range []string{"epub3.epub", "sample.md"} {
		t.Run(name, func(t *testing.T) {
			d := mustRead(t, name, fx[name])
			js, _ := Encode(d)
			path := filepath.Join("testdata", "golden", strings.ReplaceAll(name, ".", "-")+".json")
			if *update {
				if err := os.WriteFile(path, append(js, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(bytes.TrimSuffix(want, []byte("\n")), js) {
				t.Errorf("%s differs from the golden file; run go test ./internal/ebook -run TestEbookGolden -update and review", name)
			}
		})
	}
}

func TestEbookStore(t *testing.T) {
	ctx := context.Background()
	st, err := db.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, paired_by, ext, size, quick_hash, sha256, format, state, created_at, updated_at)
VALUES ('ef_1', 'library', 'lib_1', 'lf_1', '/audio/a/a.epub', 'bk_1', 'folder', 'epub', 10, '` + strings.Repeat("a", 64) + `', '` + strings.Repeat("b", 64) + `', 'epub', 'ready', 1, 1)`,
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, ext, size, quick_hash, state, created_at, updated_at)
VALUES ('ef_2', 'library', 'lib_1', 'lf_1', '/audio/a/b.pdf', 'bk_1', 'pdf', 10, '` + strings.Repeat("a", 64) + `', 'pending', 2, 2)`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json, chapter_count, word_count, char_count, created_at)
VALUES ('ed_old', 'ef_1', '` + strings.Repeat("c", 64) + `', '` + Recipe + `', '` + strings.Repeat("d", 64) + `', 5, 'epub', 'Old', '[]', 1, 1, 1, 1)`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json, chapter_count, word_count, char_count, created_at)
VALUES ('ed_1', 'ef_1', '` + strings.Repeat("b", 64) + `', '` + Recipe + `', '` + strings.Repeat("e", 64) + `', 5, 'epub', 'Now', '["X"]', 2, 3, 4, 2)`,
		`INSERT INTO edition_chapters(edition_id, idx, title, front, first_line, first_block_id, block_count, word_count, char_count) VALUES ('ed_1', 2, 'Two', 0, 'Line.', 'c2.p1', 1, 1, 1)`,
		`INSERT INTO edition_chapters(edition_id, idx, title, front, block_count, word_count, char_count) VALUES ('ed_1', 1, 'One', 1, 1, 1, 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s := NewStore(st)
	fs, err := s.FilesForBook(ctx, "bk_1")
	if err != nil || len(fs) != 2 || fs[0].ID != "ef_1" || fs[0].Path != "/audio/a/a.epub" || fs[0].PairedBy != "folder" || fs[1].SHA256 != "" {
		t.Fatalf("files = %+v, %v", fs, err)
	}
	f, ok, err := s.File(ctx, "ef_2")
	if err != nil || !ok || f.State != "pending" {
		t.Fatalf("file = %+v %v %v", f, ok, err)
	}
	if _, ok, _ := s.File(ctx, "nope"); ok {
		t.Error("missing file found")
	}
	e, ok, err := s.CurrentEdition(ctx, "ef_1")
	if err != nil || !ok || e.ID != "ed_1" || e.Title != "Now" || e.ChapterCount != 2 {
		t.Fatalf("edition = %+v %v %v", e, ok, err)
	}
	if _, ok, _ := s.CurrentEdition(ctx, "ef_2"); ok {
		t.Error("a file with no sha256 has no current edition")
	}
	chs, err := s.Chapters(ctx, "ed_1")
	if err != nil || len(chs) != 2 || chs[0].Title != "One" || !chs[0].Front || chs[1].FirstBlockID != "c2.p1" {
		t.Fatalf("chapters = %+v, %v", chs, err)
	}
	if ready, _ := s.ReadyForBook(ctx, "bk_1"); !ready {
		t.Error("book should be ready")
	}
	if ready, _ := s.ReadyForBook(ctx, "other"); ready {
		t.Error("unknown book is not ready")
	}
}
