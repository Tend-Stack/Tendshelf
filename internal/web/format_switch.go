package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/textmatch"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// Choosing where to start in the other format. The chapter of the audiobook and the chapter of the
// ebook are matched by their titles alone, never by position or count: when the match is unsure it
// says nothing, because offering no chapter is better than offering a wrong one.

// fmtChapter is a text chapter as the matching sees it.
type fmtChapter struct {
	Title string
	Front bool // opening pages (cover, title page, copyright) are never matched
}

// chapterMap pairs the chapters of the two formats. Both slices are empty when no safe pairing exists.
type chapterMap struct {
	AudioToText []int // per audio chapter: the 1-based text chapter, 0 for none
	TextToAudio []int // per text chapter: the 0-based audio chapter, -1 for none
}

// fmtKeys holds the three keys a title can be matched on; empty means "no key".
type fmtKeys struct{ title, number, word string }

// fmtKeysOf reduces a title to its matching keys.
func fmtKeysOf(title string) fmtKeys {
	k := fmtKeys{title: rdrTitleKey(title)}
	if m := rdrOwnNumber.FindStringSubmatch(textmatch.Norm(title)); m != nil {
		k.number = m[1]
	}
	if rdrIsUnnumbered(title) {
		words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) })
		k.word = rdrTitleKey(words[0])
	}
	return k
}

// uniqueKeys maps each non-empty key to its index, leaving out keys that occur twice.
func uniqueKeys(keys []string, skip []bool) map[string]int {
	seen := map[string]int{}
	dup := map[string]bool{}
	for i, k := range keys {
		if k == "" || (skip != nil && skip[i]) {
			continue
		}
		if _, ok := seen[k]; ok {
			dup[k] = true
		}
		seen[k] = i
	}
	for k := range dup {
		delete(seen, k)
	}
	return seen
}

// mapChapters matches audio chapter titles with text chapters in three passes (equal title key, equal own
// number, equal opening word of an unnumbered title). Each needs a key that is unique on both sides.
// If the matched pairs do not rise in both indexes the result is empty.
func mapChapters(audio []string, text []fmtChapter) chapterMap {
	if len(audio) == 0 || len(text) == 0 {
		return chapterMap{}
	}
	ak := make([]fmtKeys, len(audio))
	for i, t := range audio {
		ak[i] = fmtKeysOf(t)
	}
	tk := make([]fmtKeys, len(text))
	front := make([]bool, len(text))
	for i, c := range text {
		tk[i] = fmtKeysOf(c.Title)
		front[i] = c.Front
	}
	pairs := map[int]int{} // audio index -> text index (0-based)
	usedText := map[int]bool{}
	pass := func(pick func(fmtKeys) string) {
		as := make([]string, len(ak))
		ts := make([]string, len(tk))
		for i := range ak {
			if _, done := pairs[i]; !done {
				as[i] = pick(ak[i])
			}
		}
		for i := range tk {
			if !usedText[i] {
				ts[i] = pick(tk[i])
			}
		}
		au, tu := uniqueKeys(as, nil), uniqueKeys(ts, front)
		for k, ai := range au {
			if ti, ok := tu[k]; ok {
				pairs[ai] = ti
				usedText[ti] = true
			}
		}
	}
	pass(func(k fmtKeys) string { return k.title })
	pass(func(k fmtKeys) string { return k.number })
	pass(func(k fmtKeys) string { return k.word })
	if len(pairs) == 0 {
		return chapterMap{}
	}
	ais := make([]int, 0, len(pairs))
	for ai := range pairs {
		ais = append(ais, ai)
	}
	sort.Ints(ais)
	for i := 1; i < len(ais); i++ {
		if pairs[ais[i]] <= pairs[ais[i-1]] {
			return chapterMap{}
		}
	}
	m := chapterMap{AudioToText: make([]int, len(audio)), TextToAudio: make([]int, len(text))}
	for i := range m.TextToAudio {
		m.TextToAudio[i] = -1
	}
	for _, ai := range ais {
		m.AudioToText[ai] = pairs[ai] + 1
		m.TextToAudio[pairs[ai]] = ai
	}
	return m
}

// audioChapterAt is the index of the last chapter that starts at or before ms, or -1.
func audioChapterAt(chs []library.Chapter, ms int64) int {
	cur := -1
	for i, c := range chs {
		if c.StartMs <= ms {
			cur = i
		}
	}
	return cur
}

// formatOption is one row of the choice before it becomes markup.
type formatOption struct {
	Label, Where, Href string
	PlayAtMs           int64
	Play               bool
	Primary            bool
	at                 int64 // client time of the place, for the order
}

// orderOptions puts the places newest first (the lastFormat rule, decision 0018), then the start; the first
// option is the primary one. The sort is stable, so equal times keep the order they came in.
func orderOptions(places []formatOption, start formatOption) []formatOption {
	out := append([]formatOption(nil), places...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].at > out[j].at })
	out = append(out, start)
	for i := range out {
		out[i].Primary = i == 0
	}
	return out
}

// listeningPlace says whether the person has an unfinished listening place to continue from.
func listeningPlace(p *progress.Progress) bool {
	return p != nil && !p.Finished() && p.PositionMs > 0
}

// listenMap loads the audiobook's chapters and maps them onto the reader's edition.
func (s *server) listenMap(ctx context.Context, rc readerCtx) ([]library.Chapter, chapterMap, error) {
	chs, err := s.lib.Chapters(ctx, rc.book.ID)
	if err != nil {
		return nil, chapterMap{}, err
	}
	audio := make([]string, len(chs))
	for i, c := range chs {
		audio[i] = c.Title
	}
	text := make([]fmtChapter, len(rc.doc.Chapters))
	for i, c := range rc.doc.Chapters {
		text[i] = fmtChapter{Title: c.Title, Front: c.Front}
	}
	return chs, mapChapters(audio, text), nil
}

// hasStart says whether some text chapter maps onto an audio chapter that does not start at 0:
// the reader's "Listen from where you're reading" could then offer something.
func (m chapterMap) hasStart(chs []library.Chapter) bool {
	for _, a := range m.TextToAudio {
		if a >= 0 && a < len(chs) && chs[a].StartMs > 0 {
			return true
		}
	}
	return false
}

// listenChoice builds the reader's "Listen from" sheet for the chapter being read. ok is false when the
// person may not play the book or it has nothing to play.
func (s *server) listenChoice(r *http.Request, rc readerCtx, readingCh int) (components.FormatChoiceView, bool, error) {
	ctx, u := r.Context(), currentUser(r)
	v := components.FormatChoiceView{Title: chooseListenTitle, CloseLabel: readerCloseSheet, BookID: rc.book.ID,
		CloseHref: rc.href("ch", strconv.Itoa(readingCh))}
	p, err := s.playProgress(ctx, u.ID, rc.book.ID)
	if err != nil {
		return v, false, err
	}
	link, err := s.playLinkFor(ctx, u, rc.book, p)
	if err != nil || link == nil {
		return v, false, err
	}
	files, err := s.lib.AudioFiles(ctx, rc.book.ID)
	if err != nil {
		return v, false, err
	}
	chs, m, err := s.listenMap(ctx, rc)
	if err != nil {
		return v, false, err
	}
	option := func(label, where string, ms, at int64) formatOption {
		o := formatOption{Label: label, Where: where, PlayAtMs: ms, Play: true, at: at}
		if id, off, ok := playTarget(rc.book, files, ms); ok {
			o.Href = "/stream/" + id + "#t=" + strconv.FormatFloat(float64(off)/1000, 'f', 3, 64)
		}
		return o
	}
	title := func(a int) string {
		if a >= 0 && a < len(chs) {
			return chs[a].Title
		}
		return ""
	}
	listening := -1
	var places []formatOption
	if listeningPlace(p) {
		listening = audioChapterAt(chs, p.PositionMs)
		places = append(places, option(listenContinue, title(listening), p.PositionMs, p.ClientUpdatedAt))
	}
	if readingCh >= 1 && readingCh <= len(m.TextToAudio) {
		if a := m.TextToAudio[readingCh-1]; a >= 0 && chs[a].StartMs > 0 && a != listening {
			at := time.Now().UnixMilli() // the live place, when nothing is saved yet
			if rp, ok, err := s.progress.Reading(ctx, u.ID, rc.book.ID); err == nil && ok {
				at = rp.ClientUpdatedAt
			}
			places = append(places, option(listenFromReading, title(a), chs[a].StartMs, at))
		}
	}
	for _, o := range orderOptions(places, option(listenFromStart, "", 0, 0)) {
		v.Options = append(v.Options, components.FormatChoiceOption{Label: o.Label, Where: o.Where, Href: o.Href,
			PlayAtMs: o.PlayAtMs, Play: o.Play, Primary: o.Primary})
	}
	return v, true, nil
}

// getReadFrom answers the player's read control with the "Read from" sheet. With fewer than two options
// the person goes straight to the reader (HX-Location, a boosted swap of #main, so the player keeps playing).
// Access is the reader's own: the book must be visible and readable, else 404.
func (s *server) getReadFrom(w http.ResponseWriter, r *http.Request) {
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.ViewBook)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	may, err := s.mayReadBook(ctx, u, book)
	if err != nil {
		s.serverError(w, r, "read-from access", err)
		return
	}
	if !may {
		s.notFound(w, r)
		return
	}
	rc, found, err := s.readerLoad(ctx, u, book, "")
	if err != nil {
		s.serverError(w, r, "read-from reader", err)
		return
	}
	if !found || rc.doc == nil {
		s.notFound(w, r)
		return
	}
	first := 1 // the first chapter that is not front matter, 1-based
	for i, c := range rc.doc.Chapters {
		if !c.Front {
			first = i + 1
			break
		}
	}
	rp, err := s.readingOf(ctx, u.ID, book.ID)
	if err != nil {
		s.serverError(w, r, "read-from reading place", err)
		return
	}
	var places []formatOption
	if s.canPlay(ctx, u, book) {
		p, err := s.playProgress(ctx, u.ID, book.ID)
		if err != nil {
			s.serverError(w, r, "read-from listening place", err)
			return
		}
		ms, at, have := int64(0), int64(0), false
		if n, err := strconv.ParseInt(r.URL.Query().Get("ms"), 10, 64); err == nil && n >= 0 {
			ms, at, have = n, time.Now().UnixMilli(), true // the live position; a bad value is ignored
		} else if listeningPlace(p) {
			ms, at, have = p.PositionMs, p.ClientUpdatedAt, true
		}
		if have {
			chs, m, err := s.listenMap(ctx, rc)
			if err != nil {
				s.serverError(w, r, "read-from chapters", err)
				return
			}
			if a := audioChapterAt(chs, ms); a >= 0 && a < len(m.AudioToText) {
				if k := m.AudioToText[a]; k >= 1 && k <= len(rc.doc.Chapters) && k != first && (rp == nil || rp.ChapterIdx != k) {
					places = append(places, formatOption{Label: readFromListening, Where: rc.doc.Chapters[k-1].Title,
						Href: readerHref(book.ID, rc.ed.ID, "ch", strconv.Itoa(k)), at: at})
				}
			}
		}
	}
	if rp != nil && !rp.Finished() && rp.PercentBP > 0 {
		places = append(places, formatOption{Label: readContinue, Where: readWhereText(s.readPlace(r.Context(), rp.UserID, rp.BookID, rp.EditionID, rp.ChapterIdx), rp.PercentBP),
			Href: readHref(book.ID), at: rp.ClientUpdatedAt})
	}
	opts := orderOptions(places, formatOption{Label: readFromStart, Href: readerHref(book.ID, rc.ed.ID, "ch", strconv.Itoa(first))})
	if len(opts) < 2 {
		loc, _ := json.Marshal(map[string]string{"path": readHref(book.ID), "target": "#main", "select": "#main", "swap": "outerSync"})
		w.Header().Set("HX-Location", string(loc))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}
	v := components.FormatChoiceView{Title: chooseReadTitle, CloseLabel: readerCloseSheet, BookID: book.ID}
	for _, o := range opts {
		v.Options = append(v.Options, components.FormatChoiceOption{Label: o.Label, Where: o.Where, Href: o.Href, Primary: o.Primary})
	}
	s.fragment(w, r, http.StatusOK, components.FormatChoice(v))
}
