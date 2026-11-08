package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"net/http"
)

// fieldCover is the compare table's extra row: not a metadata field, so it is not in
// metadata.ApplyFields, but a ticked "cover" starts a cover_adopt job.
const fieldCover = "cover"

// compareDescriptionRunes is how much of a description the table shows before "Show all".
const compareDescriptionRunes = 200

// compareGoodScore is the score from which a match is "good": its values are ticked by default.
const compareGoodScore = 0.8

// compareMaxCandidates is how many matches the list shows.
const compareMaxCandidates = 5

// seriesText writes a series line: "Harbour Lights · Book 2".
func seriesText(name, seq string) string {
	if l := bookSeqLabel(seq); l != "" {
		return name + " · " + l
	}
	return name
}

func personNames(l []library.PersonRef) string {
	names := make([]string, 0, len(l))
	for _, p := range l {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

func abridgedText(known, abridged bool) string {
	switch {
	case !known:
		return ""
	case abridged:
		return detailsAbridged
	}
	return detailsUnabridged
}

// compareNow is the current text of each applicable field of the book.
func compareNow(cur library.BookDetail) map[string]string {
	b := cur.Book
	series := make([]string, 0, len(cur.Series))
	for _, s := range cur.Series {
		series = append(series, seriesText(s.Name, s.Sequence))
	}
	year := ""
	if b.PublishedYear > 0 {
		year = strconv.Itoa(b.PublishedYear)
	}
	return map[string]string{
		metadata.FieldTitle:         b.Title,
		metadata.FieldSubtitle:      b.Subtitle,
		metadata.FieldAuthors:       personNames(cur.Card.Authors),
		metadata.FieldNarrators:     personNames(cur.Card.Narrators),
		metadata.FieldSeries:        strings.Join(series, ", "),
		metadata.FieldPublishedYear: year,
		metadata.FieldPublisher:     b.Publisher,
		metadata.FieldDescription:   b.Description,
		metadata.FieldLanguage:      b.Language,
		metadata.FieldGenres:        strings.Join(cur.Genres, ", "),
		metadata.FieldISBN:          b.ISBN,
		metadata.FieldASIN:          b.ASIN,
		metadata.FieldAbridged:      abridgedText(b.Abridged, b.Abridged),
	}
}

// compareOnline is the text of each applicable field of a match.
func compareOnline(d metadata.Details) map[string]string {
	series := make([]string, 0, len(d.Series))
	for _, s := range d.Series {
		series = append(series, seriesText(s.Name, s.Sequence))
	}
	year := ""
	if d.Year > 0 {
		year = strconv.Itoa(d.Year)
	}
	ab := ""
	if d.Abridged != nil {
		ab = abridgedText(true, *d.Abridged)
	}
	return map[string]string{
		metadata.FieldTitle:         d.Title,
		metadata.FieldSubtitle:      d.Subtitle,
		metadata.FieldAuthors:       strings.Join(d.Authors, ", "),
		metadata.FieldNarrators:     strings.Join(d.Narrators, ", "),
		metadata.FieldSeries:        strings.Join(series, ", "),
		metadata.FieldPublishedYear: year,
		metadata.FieldPublisher:     d.Publisher,
		metadata.FieldDescription:   d.Description,
		metadata.FieldLanguage:      d.Language,
		metadata.FieldGenres:        strings.Join(d.Genres, ", "),
		metadata.FieldISBN:          d.ISBN,
		metadata.FieldASIN:          d.ASIN,
		metadata.FieldAbridged:      ab,
	}
}

// compareFields are the rows of the table, in order, with their labels.
var compareFields = []struct{ field, label string }{
	{metadata.FieldTitle, detailsLabelTitle},
	{metadata.FieldSubtitle, detailsLabelSubtitle},
	{metadata.FieldAuthors, detailsLabelAuthors},
	{metadata.FieldNarrators, detailsLabelNarrators},
	{metadata.FieldSeries, detailsLabelSeries},
	{metadata.FieldPublishedYear, detailsLabelYear},
	{metadata.FieldPublisher, detailsLabelPublisher},
	{metadata.FieldDescription, detailsLabelDescription},
	{metadata.FieldLanguage, detailsLabelLanguage},
	{metadata.FieldGenres, detailsLabelGenres},
	{metadata.FieldISBN, detailsLabelISBN},
	{metadata.FieldASIN, detailsLabelASIN},
	{metadata.FieldAbridged, detailsLabelAbridged},
}

func shortDate(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2 Jan")
}

// nowBadge says where the current value came from.
func nowBadge(info metadata.SourceInfo, has bool) (badge string, you bool) {
	switch {
	case !has:
		return detailsFromFile, false
	case info.Source == metadata.SourceUser:
		return detailsEdited, true
	case info.Source == metadata.SourceOnline:
		return joinNonEmpty(" · ", "Online", shortDate(info.ObservedAt)), false
	}
	return detailsFromFile, false
}

// cut shortens a long text to its first runes; more is the whole text, or "" when nothing was cut.
func cut(s string) (short, more string) {
	r := []rune(s)
	if len(r) <= compareDescriptionRunes {
		return s, ""
	}
	return string(r[:compareDescriptionRunes]) + "…", s
}

// compareRows builds the table: one row per field either side has a value for, then the cover row.
// A row is ticked by default when the online value fills a gap (or replaces a folder-name guess) on
// a good match and the person has not edited the field. After a tick or untick (picked) the
// request's use list decides instead. Rows with the same value or nothing online cannot be ticked.
// coverNow and coverOnline are the picture addresses; coverOnline is "" when no picture was fetched.
func compareRows(cur library.BookDetail, src map[string]metadata.SourceInfo, cand metadata.Candidate, fetchedAt int64,
	use map[string]bool, picked bool, coverNow, coverOnline string) []pages.CompareRow {
	now, found := compareNow(cur), compareOnline(cand.Details)
	good := cand.Exact || cand.Score >= compareGoodScore
	var rows []pages.CompareRow
	for _, f := range compareFields {
		n, o := now[f.field], found[f.field]
		if n == "" && o == "" {
			continue
		}
		info, has := src[f.field]
		if n == "" {
			has = false
		}
		row := pages.CompareRow{Field: f.field, Label: f.label}
		row.Now, row.NowMore = cut(n)
		row.Online, row.OnlineMore = cut(o)
		if n != "" {
			row.NowBadge, row.NowYou = nowBadge(info, has)
		}
		if o != "" {
			row.OnlineBadge = joinNonEmpty(" · ", "Online", shortDate(fetchedAt))
		}
		row.Diff = n != o && o != ""
		row.Disabled = !row.Diff
		if !row.Disabled {
			if picked {
				row.Ticked = use[f.field]
			} else {
				row.Ticked = good && info.Source != metadata.SourceUser && (n == "" || info.Source == metadata.SourceFolder)
			}
		}
		rows = append(rows, row)
	}
	if coverOnline != "" {
		row := pages.CompareRow{Field: fieldCover, Label: detailsLabelCover, OnlineImg: coverOnline, Diff: true}
		if cur.Card.Cover != nil {
			row.NowImg = coverNow
		}
		if picked {
			row.Ticked = use[fieldCover]
		} else {
			row.Ticked = cur.Card.Cover == nil
		}
		rows = append(rows, row)
	}
	return rows
}

// tickedCount is how many rows are ticked: the number the main button says.
func tickedCount(rows []pages.CompareRow) int {
	n := 0
	for _, r := range rows {
		if r.Ticked && !r.Disabled {
			n++
		}
	}
	return n
}

// compareHref is the canonical address of a compare sheet.
func compareHref(bookID, jobID string, c int, hasC bool) string {
	href := "/books/" + bookID + "/details?sheet=compare&job=" + url.QueryEscape(jobID)
	if hasC {
		href += "&c=" + strconv.Itoa(c)
	}
	return href
}

func lookupHref(bookID string) string { return "/books/" + bookID + "/details?sheet=lookup" }

// lookupAgainHref is the address of the look-up form carrying the query a job searched, so Try again does not
// lose what the person typed. All four fields are sent, even an emptied one, so a cleared author stays cleared.
func lookupAgainHref(bookID string, q online.Query) string {
	v := url.Values{"sheet": {"lookup"}, "title": {q.Title}, "author": {q.Author}, "asin": {q.ASIN}, "isbn": {q.ISBN}}
	return "/books/" + bookID + "/details?" + v.Encode()
}

func detailsTabHref(bookID string) string { return "/books/" + bookID + "/details" }

// logMarks are the signs of a step; the words beside them carry the state too, never colour alone.
var logMarks = map[string][2]string{
	online.StepWaiting: {"·", "Waiting: "},
	online.StepRunning: {"▸", "Searching: "},
	online.StepDone:    {"✓", "Result: "},
	online.StepNone:    {"–", "Result: "},
	online.StepFailed:  {"✕", "Problem: "},
}

// logFailure is a failed step in plain words, naming the place: a pause for 429 and 5xx, a
// network problem, or a short reason. Nothing here comes from the site's own answer; the details
// go to the server log.
func logFailure(place, code string) string {
	switch code {
	case "offline":
		return "Could not reach " + place + "."
	case "busy", "server":
		return place + " is busy right now, try again later."
	case "too_large":
		return "The answer from " + place + " was too large, so Tendshelf ignored it."
	case "bad_answer":
		return place + " sent an answer Tendshelf could not read."
	case "refused":
		return place + " is not on the list of sites Tendshelf may ask."
	}
	return "Could not search " + place + ", try again later."
}

// logLines words the steps of a lookup, one line each.
func logLines(steps []online.Step) []pages.LogLine {
	out := make([]pages.LogLine, 0, len(steps))
	for _, st := range steps {
		mark, ok := logMarks[st.State]
		if !ok {
			continue
		}
		place, result := st.Label, ""
		if st.Region != "" {
			place += " (" + st.Region + ")"
		}
		if st.State == online.StepFailed {
			// The sentence names the place itself, so the line is that sentence alone.
			out = append(out, pages.LogLine{State: st.State, Mark: mark[0], Words: mark[1], Text: logFailure(st.Label, st.Code)})
			continue
		}
		switch st.State {
		case online.StepWaiting:
			result = detailsLogWaiting
		case online.StepDone:
			result = countText(st.Count, detailsLogOne, detailsLogMany)
		case online.StepNone:
			result = detailsLogNone
		}
		if st.Cached && (st.State == online.StepDone || st.State == online.StepNone) {
			result += detailsLogSaved
		}
		out = append(out, pages.LogLine{State: st.State, Mark: mark[0], Words: mark[1], Text: joinNonEmpty(" — ", place, st.Query, result)})
	}
	return out
}

// candidateMeta is the second line of a match in the list: provider label and year.
func candidateMeta(c metadata.Candidate) string {
	by := ""
	if len(c.Details.Authors) > 0 {
		by = strings.Join(c.Details.Authors, ", ")
	}
	year := ""
	if c.Details.Year > 0 {
		year = strconv.Itoa(c.Details.Year)
	}
	return joinNonEmpty(" · ", by, c.ProviderLabel, year)
}

// lookupFailure is the plain-words reason a finished lookup found nothing, or "" when it simply
// found no match: every place that was asked failed, and each is named once with its own reason.
func lookupFailure(res metadata.LookupResult, steps []online.Step) string {
	if len(res.Errors) == 0 || len(res.Errors) < len(res.Searched) {
		return ""
	}
	var seen []string
	var lines []string
	for _, st := range steps {
		if st.State != online.StepFailed || slices.Contains(seen, st.Label) {
			continue
		}
		seen = append(seen, st.Label)
		lines = append(lines, logFailure(st.Label, st.Code))
	}
	return strings.Join(lines, " ")
}

// compareSheetView builds the compare sheet for a lookup job. The selected candidate is c (hasC);
// ok is false when c is out of range, which the caller answers as 404. The "now" side is read
// fresh, so the table always compares with what the book holds at this moment.
func (s *server) compareSheetView(r *http.Request, cur library.BookDetail, j jobs.Job, res metadata.LookupResult, c int, hasC bool, use map[string]bool, picked bool) (pages.CompareSheetView, bool, error) {
	bookID := cur.Book.ID
	v := pages.CompareSheetView{
		CSRF: s.csrf.TokenFrom(r.Context()), Title: detailsSheetTitle, CloseLabel: detailsSheetClose,
		CloseHref: detailsTabHref(bookID), Styles: s.sheetStyles(pages.DetailsStyles...),
		Waiting: detailsWaiting, CheckAgain: detailsCheckAgain, TryAgain: detailsTryAgain,
		SheetHref: compareHref(bookID, j.ID, 0, false), TryAgainHref: tryAgainHref(bookID, j),
		PollHref: "/hx/jobs/" + j.ID + "?book=" + bookID, ExactLabel: detailsExact,
		ColField: detailsColField, ColNow: detailsColNow, ColOnline: detailsColOnline, ColUse: detailsColUse,
		Caption: detailsCaption, Same: detailsSame, ShowAll: detailsShowAll, NoValue: detailsNoValue,
		KeepLabel: detailsKeep, PickOther: detailsPickOther, ApplyAction: "/books/" + bookID + "/match/apply",
		JobID:    j.ID,
		LogTitle: detailsLogTitle, LogPrivacy: detailsLogPrivacy, LogReview: detailsLogReview,
		Log: logLines(metadata.LookupSteps(j, res)),
	}
	switch j.State {
	case jobs.StateDone:
	case jobs.StateFailed, jobs.StateCancelled:
		v.State, v.Message = "failed", j.ErrorText
		if v.Message == "" {
			v.Message = detailsFailed
		}
		return v, true, nil
	default:
		v.State = "wait"
		return v, true, nil
	}
	cands := res.Candidates
	if len(cands) > compareMaxCandidates {
		cands = cands[:compareMaxCandidates]
	}
	if len(cands) == 0 {
		v.State, v.Message = "none", detailsNone
		if m := lookupFailure(res, metadata.LookupSteps(j, res)); m != "" {
			v.State, v.Message = "failed", m
		}
		return v, true, nil
	}
	if !hasC {
		v.State, v.Intro = "list", detailsListIntro
		for i, cd := range cands {
			v.Candidates = append(v.Candidates, pages.CompareCandidate{
				Href: compareHref(bookID, j.ID, i, true), Title: cd.Details.Title, Meta: candidateMeta(cd), Exact: cd.Exact})
		}
		return v, true, nil
	}
	if c < 0 || c >= len(cands) {
		return v, false, nil
	}
	cd := cands[c]
	src, err := s.details.Sources(r.Context(), bookID)
	if err != nil {
		return v, false, err
	}
	coverNow := ""
	if cur.Card.Cover != nil {
		coverNow = coverURL(bookID, cur.Card.Cover.SHA, 128)
	}
	coverOnline := ""
	if cd.CoverCached {
		coverOnline = fmt.Sprintf("/books/%s/match/%s/cover/%d", bookID, j.ID, c)
	}
	v.State, v.C = "table", strconv.Itoa(c)
	v.SheetHref = compareHref(bookID, j.ID, c, true)
	v.Rows = compareRows(cur, src, cd, res.FetchedAt, use, picked, coverNow, coverOnline)
	v.UseCount = tickedCount(v.Rows)
	v.UseText = detailsUseText(v.UseCount)
	lead := detailsFound
	if !cd.Exact && cd.Score < compareGoodScore {
		lead = detailsMaybe
	}
	v.Intro = lead + joinNonEmpty(", ", cd.Details.Title, cd.Details.Publisher, yearText(cd.Details.Year)) + "." + detailsTick
	if len(cands) > 1 {
		v.PickHref = compareHref(bookID, j.ID, 0, false)
	}
	return v, true, nil
}

func yearText(y int) string {
	if y <= 0 {
		return ""
	}
	return strconv.Itoa(y)
}

// useSet reads the repeated use fields of a query.
func useSet(vals []string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

// tryAgainHref is lookupHref with the job's own query when its stored payload holds one.
func tryAgainHref(bookID string, j jobs.Job) string {
	var p metadata.LookupPayload
	if json.Unmarshal(j.Payload, &p) != nil || p.Query == (online.Query{}) {
		return lookupHref(bookID)
	}
	return lookupAgainHref(bookID, p.Query)
}
