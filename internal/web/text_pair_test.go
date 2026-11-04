package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// textPairEPUB is a small synthetic EPUB with the given title and author.
func textPairEPUB(t *testing.T, seed, title, author string) []byte {
	t.Helper()
	chs := []ebooktest.EPUBChapter{{Title: "Title Page", EpubType: "titlepage", Paras: []string{title}}}
	for i := 1; i <= 3; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Chapter %d", i), EpubType: "chapter",
			Paras: []string{fmt.Sprintf("Line %d of %s. It goes on for a while.", i, title), "A second paragraph " + seed + "."}})
	}
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: title, Authors: []string{author}, Chapters: chs})
}

// textPairAdd uploads an EPUB to the book, waits until it is read, and sets how it is paired:
// pairedBy "" leaves it unpaired (a suggestion), anything else relabels the pairing.
func (l *textLab) textPairAdd(t *testing.T, name, title, author, pairedBy string) ebook.File {
	t.Helper()
	f, err := l.svc.AddUpload(context.Background(), l.book, l.ownerID, name, bytes.NewReader(textPairEPUB(t, name+title, title, author)))
	if err != nil {
		t.Fatal(err)
	}
	f = l.waitState(t, f.ID, "ready")
	q, args := `UPDATE ebook_files SET paired_by = ? WHERE id = ?`, []any{pairedBy, f.ID}
	if pairedBy == "" {
		q = `UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`
		args = []any{f.ID}
	}
	if _, err := l.a.st.Writer().Exec(q, args...); err != nil {
		t.Fatal(err)
	}
	return f
}

func (l *textLab) textPairFile(t *testing.T, id string) ebook.File {
	t.Helper()
	f, ok, err := l.svc.Store().File(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("file %s: %v %v", id, ok, err)
	}
	return f
}

func TestTextPairCard(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	same := l.textPairAdd(t, "same.epub", "Textword Lantern", "Imogen Textauthor", "folder")
	other := l.textPairAdd(t, "other.epub", "A Different Tale", "Zed Else", "title")
	body := l.get(l.owner, l.path(""), false).Body.String()
	for _, want := range []string{
		"Same title and author: Imogen Textauthor", "Not the same book",
		"Different title or author: A Different Tale by Zed Else", "ts-pair__checks", "ts-pair__ebook",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tab lacks %q", want)
		}
	}
	if !strings.Contains(body, `action="`+l.path("/"+same.ID+"/unpair")+`"`) || !strings.Contains(body, l.path("/"+other.ID+"/unpair")) {
		t.Error("tab lacks the Not the same book forms")
	}
	// An upload paired by a person has the form under its row but no card.
	up := l.textPairAdd(t, "mine.epub", "Mine", "Me Too", "person")
	body = l.get(l.owner, l.path(""), false).Body.String()
	if !strings.Contains(body, l.path("/"+up.ID+"/unpair")) {
		t.Error("a person-paired upload has no Not the same book form")
	}
	if strings.Contains(body, "pair-title-"+up.ID) {
		t.Error("a person-paired upload got a pairing card")
	}
}

func TestTextPairSuggestionsEditorsOnly(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	l.textPairAdd(t, "paired.epub", "Textword Lantern", "Imogen Textauthor", "title")
	sg := l.textPairAdd(t, "found.epub", "Textword Lantern", "Imogen Textauthor", "")
	body := l.get(l.owner, l.path(""), false).Body.String()
	for _, want := range []string{"Pair with this audiobook", "Found in your folders", l.path("/" + sg.ID + "/pair"), l.path("/" + sg.ID + "/not-same")} {
		if !strings.Contains(body, want) {
			t.Errorf("owner tab lacks %q", want)
		}
	}
	if strings.Contains(body, `class="ts-pair__chip"`) {
		t.Error("one edition drew edition chips")
	}
	for name, ck := range map[string]*http.Cookie{"reader": l.reader, "guest": l.guest} {
		rec := l.get(ck, l.path(""), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		b := rec.Body.String()
		for _, no := range []string{"Pair with this audiobook", "Found in", "ts-pair__checks", sg.ID, "unpair"} {
			if strings.Contains(b, no) {
				t.Errorf("%s sees %q", name, no)
			}
		}
	}
}

func TestTextPairActions(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	post := func(path string, htmx bool) *http.Response {
		return l.postForm(t, l.owner, path, htmx).Result()
	}
	// Pair a suggestion, plain form: 303 back to the tab; the file is on the book, paired by a person.
	sg := l.textPairAdd(t, "one.epub", "Textword Lantern", "Imogen Textauthor", "")
	if r := post(l.path("/"+sg.ID+"/pair"), false); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != l.path("") {
		t.Fatalf("pair: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if f := l.textPairFile(t, sg.ID); f.BookID != l.book.ID || f.PairedBy != "person" {
		t.Fatalf("after pair: %+v", f)
	}
	// Not the same book on a paired file: htmx gets the page (200, push url); the file leaves with a hold.
	rec := l.postForm(t, l.owner, l.path("/"+sg.ID+"/unpair"), true)
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Push-Url") != l.path("") {
		t.Fatalf("unpair htmx: %d %q", rec.Code, rec.Header().Get("HX-Push-Url"))
	}
	if f := l.textPairFile(t, sg.ID); f.BookID != "" || f.Hold != "person" {
		t.Fatalf("after unpair: %+v", f)
	}
	// The unpaired file is not offered again for this book (the unpair recorded it).
	if strings.Contains(l.get(l.owner, l.path(""), false).Body.String(), l.path("/"+sg.ID+"/pair")) {
		t.Error("an unpaired file is offered again")
	}
	// Not the same book on a suggestion.
	sg2 := l.textPairAdd(t, "two.epub", "Textword Lantern", "Imogen Textauthor", "")
	if r := post(l.path("/"+sg2.ID+"/not-same"), false); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("not-same: %d", r.StatusCode)
	}
	if strings.Contains(l.get(l.owner, l.path(""), false).Body.String(), l.path("/"+sg2.ID+"/pair")) {
		t.Error("a not-the-same suggestion is offered again")
	}
	// Default and choose need a paired, ready file.
	a := l.addReady(t, "a.epub", "Alpha", 2)
	b := l.addReady(t, "b.epub", "Beta", 2)
	if r := post(l.path("/"+b.ID+"/default"), false); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("default: %d", r.StatusCode)
	}
	if !l.textPairFile(t, b.ID).BookDefault {
		t.Error("default did not stick")
	}
	if r := post(l.path("/"+a.ID+"/choose"), true); r.StatusCode != http.StatusOK {
		t.Fatalf("choose: %d", r.StatusCode)
	}
	if id, _ := l.svc.Store().Choice(context.Background(), l.ownerID, l.book.ID); id != a.ID {
		t.Errorf("choice is %q", id)
	}
	// Errors: unknown file 404; not ready 409 (toast for htmx, the alert in the tab otherwise); unpair of a file on no book 404.
	if r := post(l.path("/nope/pair"), false); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown file: %d", r.StatusCode)
	}
	pend := l.addPlaced(t, "slow.epub", "pending", "")
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, pend); err != nil {
		t.Fatal(err)
	}
	rec = l.postForm(t, l.owner, l.path("/"+pend+"/pair"), true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `class="ts-toast"`) || !strings.Contains(rec.Body.String(), "still being read") {
		t.Errorf("not ready htmx: %d %s", rec.Code, rec.Body.String())
	}
	rec = l.postForm(t, l.owner, l.path("/"+pend+"/pair"), false)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `role="alert"`) || !strings.Contains(rec.Body.String(), "still being read") {
		t.Errorf("not ready plain: %d", rec.Code)
	}
	if r := post(l.path("/"+sg.ID+"/unpair"), false); r.StatusCode != http.StatusNotFound {
		t.Errorf("unpair of a file on no book: %d", r.StatusCode)
	}
	// An upload the book already has: 409 with the deck sentence.
	dup := l.addReady(t, "dup.epub", "Gamma", 2)
	dupB := l.textPairAdd(t, "dup2.epub", "Gamma", "Zed Else", "")
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET sha256 = (SELECT sha256 FROM ebook_files WHERE id = ?) WHERE id = ?`, dup.ID, dupB.ID); err != nil {
		t.Fatal(err)
	}
	rec = l.postForm(t, l.owner, l.path("/"+dupB.ID+"/pair"), true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This ebook is already on this book.") {
		t.Errorf("duplicate: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTextPairRoles(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	a := l.addReady(t, "a.epub", "Alpha", 2)
	l.addReady(t, "b.epub", "Beta", 2)
	n := 0
	for _, who := range []struct {
		name string
		ck   *http.Cookie
		ok   bool
	}{{"owner", l.owner, true}, {"admin", l.admin, true}, {"member", l.reader, false}, {"member no read", l.noRead, false}, {"guest", l.guest, false}, {"guest no read", l.guestNoR, false}} {
		for _, verb := range []string{"pair", "not-same", "default", "unpair"} {
			id := a.ID
			if verb == "pair" || verb == "not-same" {
				n++
				id = l.textPairAdd(t, fmt.Sprintf("found-%d.epub", n), "Textword Lantern", "Imogen Textauthor", "").ID
			}
			want := http.StatusNotFound
			if who.ok && verb != "unpair" { // unpair is checked last, below, so the editions stay for choose
				want = http.StatusSeeOther
			}
			if who.ok && verb == "unpair" {
				continue
			}
			if got := l.postForm(t, who.ck, l.path("/"+id+"/"+verb), false).Code; got != want {
				t.Errorf("%s %s: %d, want %d", who.name, verb, got, want)
			}
		}
	}
	for _, who := range []struct {
		name string
		ck   *http.Cookie
		want int
	}{{"owner", l.owner, 303}, {"admin", l.admin, 303}, {"member", l.reader, 303}, {"member no read", l.noRead, 404}, {"guest", l.guest, 303}, {"guest no read", l.guestNoR, 404}} {
		if got := l.postForm(t, who.ck, l.path("/"+a.ID+"/choose"), false).Code; got != who.want {
			t.Errorf("%s choose: %d, want %d", who.name, got, who.want)
		}
	}
	// unpair is the last editor action, so the two editions stay for the choose checks above.
	if got := l.postForm(t, l.reader, l.path("/"+a.ID+"/unpair"), false).Code; got != http.StatusNotFound {
		t.Errorf("member unpair: %d", got)
	}
	if got := l.postForm(t, l.admin, l.path("/"+a.ID+"/unpair"), false).Code; got != http.StatusSeeOther {
		t.Errorf("admin unpair: %d", got)
	}
	// A choice on a file that is not on this book is a 404 (and writes nothing).
	if got := l.postForm(t, l.reader, l.path("/nope/choose"), false).Code; got != http.StatusNotFound {
		t.Errorf("choose unknown: %d", got)
	}
}

func TestTextEditionChoice(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	a := l.addReady(t, "a.epub", "Alpha", 2)
	b := l.addReady(t, "b.epub", "Beta", 2)
	ctx := context.Background()
	st := l.svc.Store()
	edition := func(user string) string {
		f, _, ok, err := st.EditionFor(ctx, user, l.book.ID)
		if err != nil || !ok {
			t.Fatalf("edition: %v %v", ok, err)
		}
		return f.ID
	}
	if got := edition(l.ownerID); got != a.ID {
		t.Fatalf("fallback is the oldest ready file: %s", got)
	}
	body := l.get(l.owner, l.path(""), false).Body.String()
	if !strings.Contains(body, "The book&#39;s edition") || !strings.Contains(body, "Your edition") || !strings.Contains(body, "Read this edition") || !strings.Contains(body, "Make it the book&#39;s edition") {
		t.Error("two editions lack their chips and buttons")
	}
	l.postForm(t, l.owner, l.path("/"+b.ID+"/default"), false)
	if got := edition(l.ownerID); got != b.ID {
		t.Fatalf("book default: %s", got)
	}
	l.postForm(t, l.owner, l.path("/"+a.ID+"/choose"), false)
	if got := edition(l.ownerID); got != a.ID {
		t.Fatalf("own choice: %s", got)
	}
	if got := edition("someone-else"); got != b.ID {
		t.Fatalf("another person keeps the default: %s", got)
	}
	// Choosing the book's edition clears the choice.
	l.postForm(t, l.owner, l.path("/"+b.ID+"/choose"), false)
	if id, _ := st.Choice(ctx, l.ownerID, l.book.ID); id != "" {
		t.Errorf("choice of the default stayed: %s", id)
	}
	// A choice whose file was unpaired falls back to the default.
	l.postForm(t, l.owner, l.path("/"+a.ID+"/choose"), false)
	l.postForm(t, l.owner, l.path("/"+a.ID+"/unpair"), false)
	if got := edition(l.ownerID); got != b.ID {
		t.Fatalf("unpaired choice: %s", got)
	}
	// A reader sees the chips and their own button, never the editor's.
	l.addReady(t, "c.epub", "Gamma", 2)
	body = l.get(l.reader, l.path(""), false).Body.String()
	if !strings.Contains(body, "Read this edition") || strings.Contains(body, "Make it the book") {
		t.Error("reader buttons are wrong")
	}
}
