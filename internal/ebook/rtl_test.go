package ebook

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

func TestRTLLangDir(t *testing.T) {
	for _, c := range []struct{ tag, want string }{
		{"ar", "rtl"}, {"ar-EG", "rtl"}, {"AR", "rtl"}, {"he", "rtl"}, {"iw", "rtl"}, {"fa-IR", "rtl"},
		{"ur_PK", "rtl"}, {"yi", "rtl"}, {"az-Arab", "rtl"}, {"pa-Arab", "rtl"}, {"ku-Arab-IQ", "rtl"},
		{"az-Latn", ""}, {"az", ""}, {"ku", ""}, {"pa", ""}, {"en", ""}, {"en-US", ""}, {"ar-Latn", ""},
		{"und", ""}, {"", ""}, {"  he  ", "rtl"},
	} {
		if got := LangDir(c.tag); got != c.want {
			t.Errorf("LangDir(%q) = %q, want %q", c.tag, got, c.want)
		}
	}
}

func TestRTLDecideDir(t *testing.T) {
	for _, c := range []struct {
		name, hint, lang string
		nR, nL           int
		want             string
	}{
		{"letters decide rtl against a ltr hint", "ltr", "en", 400, 100, "rtl"},
		{"letters decide ltr against an rtl hint", "rtl", "ar", 100, 400, "ltr"},
		{"200 letters decide", "ltr", "", 200, 0, "rtl"},
		{"199 letters do not; hint wins", "ltr", "", 199, 0, "ltr"},
		{"199 letters, no hint, language wins", "", "he", 0, 199, "rtl"},
		{"ratio short of 2:1 falls to the hint", "ltr", "", 300, 160, "ltr"},
		{"ratio short of 2:1, no hint, language", "", "ar", 100, 100, "rtl"},
		{"no hint, no language: more letters", "", "", 90, 60, "rtl"},
		{"no hint, no language: more letters ltr", "", "", 60, 90, "ltr"},
		{"nothing at all is ltr", "", "", 0, 0, "ltr"},
		{"default hint is ignored", "default", "", 0, 10, "ltr"},
		{"auto hint is ignored, language used", "auto", "ar", 0, 0, "rtl"},
	} {
		if got := decideDir(c.hint, c.lang, c.nR, c.nL); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func rtlEPUB(t *testing.T, o ebooktest.EPUBOptions) *Doc {
	t.Helper()
	o.Version = 3
	o.Title = "Sample"
	return mustRead(t, "b.epub", ebooktest.EPUB(t, o))
}

func rawChapter(body string) []ebooktest.EPUBChapter {
	return []ebooktest.EPUBChapter{{Title: "One", Raw: body}}
}

func paras(ps ...string) string {
	var sb strings.Builder
	for _, p := range ps {
		sb.WriteString("<p>" + p + "</p>")
	}
	return sb.String()
}

func TestRTLDetectEPUB(t *testing.T) {
	english := strings.Repeat("The harbour was quiet and the boats rested. ", 8)
	short := strings.Repeat("lorem ", 25) // 125 letters
	arabic := paras(ebooktest.ArabicParas(6)...)
	hebrew := paras(ebooktest.HebrewParas(6)...)
	for _, c := range []struct {
		name string
		o    ebooktest.EPUBOptions
		want string
	}{
		{"arabic text and ar", ebooktest.EPUBOptions{Lang: "ar", Chapters: rawChapter(arabic)}, "rtl"},
		{"hebrew text, no language", ebooktest.EPUBOptions{Chapters: rawChapter(hebrew)}, "rtl"},
		{"arabic text, en tag", ebooktest.EPUBOptions{Lang: "en", Chapters: rawChapter(arabic)}, "rtl"},
		{"english, rtl spine", ebooktest.EPUBOptions{Dir: "rtl", Chapters: rawChapter(paras(english))}, ""},
		{"few letters, rtl spine", ebooktest.EPUBOptions{Dir: "rtl", Chapters: rawChapter(paras(short))}, "rtl"},
		{"few letters, html dir rtl", ebooktest.EPUBOptions{HTMLDir: "rtl", Chapters: rawChapter(paras(short))}, "rtl"},
		{"spine wins over html dir", ebooktest.EPUBOptions{Dir: "ltr", HTMLDir: "rtl", Chapters: rawChapter(paras(short))}, ""},
		{"few letters, ar tag", ebooktest.EPUBOptions{Lang: "ar", Chapters: rawChapter(paras(short))}, "rtl"},
		{"english", ebooktest.EPUBOptions{Lang: "en", Chapters: rawChapter(paras(english))}, ""},
	} {
		d := rtlEPUB(t, c.o)
		if d.Meta.Dir != c.want {
			t.Errorf("%s: Meta.Dir = %q, want %q", c.name, d.Meta.Dir, c.want)
		}
	}
	d := rtlEPUB(t, ebooktest.EPUBOptions{Lang: "en", Chapters: rawChapter(paras(english))})
	js, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(js, []byte(`"dir"`)) {
		t.Error("a left-to-right document carries a dir field")
	}
}

func TestRTLBlockDir(t *testing.T) {
	findBlock := func(d *Doc, prefix string) *Block {
		for _, c := range d.Chapters {
			for i := range c.Blocks {
				if strings.HasPrefix(c.Blocks[i].Text, prefix) {
					return &c.Blocks[i]
				}
			}
		}
		t.Fatalf("no block starting %q", prefix)
		return nil
	}
	ar := ebooktest.ArabicParas(6)
	d := rtlEPUB(t, ebooktest.EPUBOptions{Lang: "ar", Chapters: rawChapter(
		paras(ar...) + paras("This is an English paragraph in the middle.") +
			paras("iPhone "+ar[0]))})
	if d.Meta.Dir != "rtl" {
		t.Fatalf("book dir %q", d.Meta.Dir)
	}
	if got := findBlock(d, "This is").Dir; got != "ltr" {
		t.Errorf("English paragraph in an Arabic book: Dir = %q, want ltr", got)
	}
	if got := findBlock(d, "iPhone").Dir; got != "" {
		t.Errorf("Arabic paragraph opening with iPhone: Dir = %q, want none", got)
	}
	if got := findBlock(d, ar[1][:10]).Dir; got != "" {
		t.Errorf("Arabic paragraph in an Arabic book: Dir = %q, want none", got)
	}

	english := strings.Repeat("The harbour was quiet and the boats rested. ", 8)
	d = rtlEPUB(t, ebooktest.EPUBOptions{Lang: "en", Chapters: rawChapter(
		paras(english) + paras(ebooktest.HebrewParas(1)[0]))})
	if d.Meta.Dir != "" {
		t.Fatalf("book dir %q", d.Meta.Dir)
	}
	if got := findBlock(d, ebooktest.HebrewParas(1)[0][:6]).Dir; got != "rtl" {
		t.Errorf("Hebrew paragraph in an English book: Dir = %q, want rtl", got)
	}
	if got := findBlock(d, "The harbour").Dir; got != "" {
		t.Errorf("English paragraph in an English book: Dir = %q, want none", got)
	}
}

func TestRTLDetectText(t *testing.T) {
	txt := strings.Join(ebooktest.ArabicParas(6), "\n\n") + "\n"
	d := mustRead(t, "kitab.txt", []byte(txt))
	if d.Meta.Dir != "rtl" {
		t.Errorf("Arabic .txt: Meta.Dir = %q, want rtl", d.Meta.Dir)
	}
	d = mustRead(t, "plain.txt", []byte(strings.Repeat("The harbour was quiet.\n\n", 20)))
	if d.Meta.Dir != "" {
		t.Errorf("English .txt: Meta.Dir = %q", d.Meta.Dir)
	}
}

func TestRTLDirectionLegacy(t *testing.T) {
	for _, c := range []struct {
		name string
		d    *Doc
		want string
	}{
		{"v3 with he", &Doc{Recipe: "tendshelf-ebook-v3", Meta: Meta{Language: "he"}}, "rtl"},
		{"v1 with ar-EG", &Doc{Recipe: "tendshelf-ebook-v1", Meta: Meta{Language: "ar-EG"}}, "rtl"},
		{"v3 with en", &Doc{Recipe: "tendshelf-ebook-v3", Meta: Meta{Language: "en"}}, ""},
		{"v4 with ar and no Dir", &Doc{Recipe: Recipe, Meta: Meta{Language: "ar"}}, ""},
		{"v4 with Dir", &Doc{Recipe: Recipe, Meta: Meta{Dir: "rtl"}}, "rtl"},
		{"nil", nil, ""},
	} {
		if got := Direction(c.d); got != c.want {
			t.Errorf("%s: Direction = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRTLSegment(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       int
	}{
		{"arabic question mark", "ذهب الولد إلى المدرسة؟ عاد إلى البيت. ثم نام الولد.", 3},
		{"urdu full stop", "لڑکا اسکول گیا۔ وہ گھر واپس آیا۔", 2},
		{"arabic initial", "د. أحمد وصل. ثم ذهب.", 2},
		{"arabic initial with tatweel", "هـ. أحمد وصل. ثم ذهب.", 2},
		{"hebrew", "הילד הלך לבית הספר. אחר כך חזר הביתה.", 2},
		{"hebrew sof pasuq", "הילד הלך לבית הספר׃ אחר כך חזר הביתה׃", 2},
		{"latin unchanged", "Dr. Voss arrived at dawn. Then she left.", 2},
	} {
		s, w := segmentText(c.text, false)
		if len(s) != c.want {
			t.Errorf("%s: %d sentences %v, want %d", c.name, len(s), s, c.want)
		}
		if len(w) == 0 {
			t.Errorf("%s: no words", c.name)
		}
	}
}

func TestRTLRecipeV4Reread(t *testing.T) {
	e := newIEnv(t, true)
	paraAr := ebooktest.ArabicParas(6)
	opts := ebooktest.EPUBOptions{Version: 3, Title: "Kitab", Authors: []string{"Ann Author"}, Chapters: []ebooktest.EPUBChapter{
		{Title: "One", Raw: `<h1>One</h1>` + paras(paraAr...)},
	}}
	path := e.write("Book/kitab.epub", ebooktest.EPUB(t, opts))
	e.scan()
	f := e.waitState(e.fileAt(path).ID)
	if f.State != "ready" {
		t.Fatalf("state %s", f.State)
	}
	pairTExec(e, `INSERT INTO users(id, username, role, created_at) VALUES ('u1', 'Ana', 'owner', 1)`)
	pairTBook(e, "b1", "Kitab", "Ann Author")
	pairTExec(e, `UPDATE ebook_files SET book_id = 'b1', book_default = 1 WHERE id = ?`, f.ID)

	// The stored edition becomes what recipe v3 made: the same text and a language, no direction.
	od := mustRead(t, "older.epub", ebooktest.EPUB(t, opts))
	if od.Meta.Dir != "rtl" {
		t.Fatalf("new parse: Meta.Dir = %q", od.Meta.Dir)
	}
	od.Meta.Dir = ""
	od.Meta.Language = "ar"
	od.Recipe = "tendshelf-ebook-v3"
	od.SourceSHA256 = f.SHA256
	if Direction(od) != "rtl" {
		t.Fatal("the legacy fallback does not turn a tagged Arabic v3 book right to left")
	}
	js, _ := Encode(od)
	oldSHA, err := WriteDoc(e.svc.textDir(), js)
	if err != nil {
		t.Fatal(err)
	}
	var oldID string
	if err := e.st.Reader().QueryRow(`SELECT id FROM editions WHERE ebook_file_id = ?`, f.ID).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	pairTExec(e, `UPDATE editions SET recipe = 'tendshelf-ebook-v3', doc_sha256 = ?, doc_bytes = ? WHERE id = ?`, oldSHA, len(js), oldID)
	quote := strings.Join(strings.Fields(paraAr[0])[:2], " ")
	end := len([]rune(quote))
	pairTExec(e, `INSERT INTO highlights(id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, quote_prefix, quote_suffix, created_at, updated_at)
VALUES ('h1','u1','b1',?,1,'c1.p2.s0',0,'c1.p2.s0',?,?,'','',1,1)`, oldID, end, quote)
	pairTExec(e, `INSERT INTO reading_positions(user_id, book_id, edition_id, chapter_idx, sid, off, quote, char_pos, percent_bp, client_updated_at, server_updated_at, device_id)
VALUES ('u1','b1',?,1,'c1.p2.s0',0,?,30,500,5,5,'dev')`, oldID, quote)

	e.waitIdle(t)
	if err := e.svc.EnqueueStale(bg); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnqueuePending(bg); err != nil {
		t.Fatal(err)
	}
	if got := e.waitState(f.ID); got.State != "ready" {
		t.Fatalf("after the re-read state %q", got.State)
	}
	var newID, recipe, sha string
	if err := e.st.Reader().QueryRow(`SELECT id, recipe, doc_sha256 FROM editions WHERE ebook_file_id = ?`, f.ID).Scan(&newID, &recipe, &sha); err != nil || recipe != Recipe {
		t.Fatalf("edition %q recipe %q err %v", newID, recipe, err)
	}
	nd, err := ReadDoc(e.svc.textDir(), sha)
	if err != nil {
		t.Fatal(err)
	}
	if nd.Meta.Dir != "rtl" || Direction(nd) != "rtl" {
		t.Errorf("new edition Meta.Dir = %q", nd.Meta.Dir)
	}
	e.waitDocGone(oldSHA)
	var hed, ped *string
	var hsid string
	var hoff, hend int
	if err := e.st.Reader().QueryRow(`SELECT edition_id, start_sid, start_off, end_off FROM highlights WHERE id='h1'`).Scan(&hed, &hsid, &hoff, &hend); err != nil {
		t.Fatal(err)
	}
	if hed == nil || *hed != newID || hsid != "c1.p2.s0" || hoff != 0 || hend != end {
		t.Errorf("highlight = %v %q %d-%d", hed, hsid, hoff, hend)
	}
	var psid string
	if err := e.st.Reader().QueryRow(`SELECT edition_id, sid FROM reading_positions WHERE user_id='u1'`).Scan(&ped, &psid); err != nil || ped == nil || *ped != newID || psid != "c1.p2.s0" {
		t.Errorf("position = %v %q %v", ped, psid, err)
	}
}
