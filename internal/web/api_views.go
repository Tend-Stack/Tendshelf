package web

import (
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// This file holds the JSON shapes of the /api/v1 read routes and the functions that fill them from
// the library types the pages already use. Times are Unix ms, durations ms, names camelCase, and a
// list is never null (see docs/api.md).

type apiPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiSeriesRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Sequence string `json:"sequence"`
}

// apiCover: url is the alias address without a width (append &w=<px> for a thumbnail); it is empty
// for a book whose cover has no image file.
type apiCover struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Tint   string `json:"tint"`
}

type apiBookCard struct {
	ID            string        `json:"id"`
	LibraryID     string        `json:"libraryId"`
	Title         string        `json:"title"`
	Subtitle      string        `json:"subtitle"`
	Authors       []apiPerson   `json:"authors"`
	Narrators     []apiPerson   `json:"narrators"`
	Series        *apiSeriesRef `json:"series"`
	DurationMs    int64         `json:"durationMs"`
	AddedAt       int64         `json:"addedAt"`
	PublishedYear *int          `json:"publishedYear"`
	Cover         *apiCover     `json:"cover"`
}

type apiLibrary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Books int    `json:"books"`
}

type apiChapter struct {
	Idx     int    `json:"idx"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Title   string `json:"title"`
}

type apiFile struct {
	ID          string `json:"id"`
	Idx         int    `json:"idx"`
	OffsetMs    int64  `json:"offsetMs"`
	DurationMs  int64  `json:"durationMs"`
	Size        int64  `json:"size"`
	Mime        string `json:"mime"`
	NeedsCompat bool   `json:"needsCompat"`
	Playable    bool   `json:"playable"`
	StreamURL   string `json:"streamUrl"`
	PeaksURL    string `json:"peaksUrl"`
}

// apiProgress is the stored progress of one book for the signed-in person.
type apiProgress struct {
	BookID          string `json:"bookId"`
	PositionMs      int64  `json:"positionMs"`
	DurationMs      int64  `json:"durationMs"`
	Finished        bool   `json:"finished"`
	FinishedAt      int64  `json:"finishedAt"`
	ClientUpdatedAt int64  `json:"clientUpdatedAt"`
	ServerUpdatedAt int64  `json:"serverUpdatedAt"`
	DeviceID        string `json:"deviceId"`
	DeviceLabel     string `json:"deviceLabel"`
}

type apiShelf struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerName string `json:"ownerName"`
	Shared    bool   `json:"shared"`
	Books     int    `json:"books"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func apiPeople(in []library.PersonRef) []apiPerson {
	out := make([]apiPerson, 0, len(in))
	for _, p := range in {
		out = append(out, apiPerson{ID: p.ID, Name: p.Name})
	}
	return out
}

func apiSeriesOf(s library.SeriesRef) apiSeriesRef {
	return apiSeriesRef{ID: s.ID, Name: s.Name, Sequence: s.Sequence}
}

// apiCoverURL is the token-reachable cover address; v changes with the image so it can be cached.
func apiCoverURL(bookID, sha string) string {
	if sha == "" {
		return ""
	}
	return apiPrefix + "covers/" + url.PathEscape(bookID) + "?v=" + sha[:min(len(sha), 8)]
}

func apiCardOf(c library.BookCard) apiBookCard {
	out := apiBookCard{
		ID: c.ID, LibraryID: c.LibraryID, Title: c.Title, Subtitle: c.Subtitle,
		Authors: apiPeople(c.Authors), Narrators: apiPeople(c.Narrators),
		DurationMs: c.DurationMs, AddedAt: c.AddedAt,
	}
	if c.Series != nil {
		s := apiSeriesOf(*c.Series)
		out.Series = &s
	}
	if c.PublishedYear != 0 {
		y := c.PublishedYear
		out.PublishedYear = &y
	}
	if c.Cover != nil {
		out.Cover = &apiCover{URL: apiCoverURL(c.ID, c.Cover.SHA), Width: c.Cover.Width, Height: c.Cover.Height, Tint: c.Cover.Tint}
	}
	return out
}

func apiCardsOf(in []library.BookCard) []apiBookCard {
	out := make([]apiBookCard, 0, len(in))
	for _, c := range in {
		out = append(out, apiCardOf(c))
	}
	return out
}

func apiChaptersOf(in []library.Chapter) []apiChapter {
	out := make([]apiChapter, 0, len(in))
	for _, c := range in {
		out = append(out, apiChapter{Idx: c.Idx, StartMs: c.StartMs, EndMs: c.EndMs, Title: c.Title})
	}
	return out
}

func apiFilesOf(book library.Book, in []library.AudioFile) []apiFile {
	out := make([]apiFile, 0, len(in))
	for _, f := range in {
		out = append(out, apiFile{
			ID: f.ID, Idx: f.Idx, OffsetMs: f.OffsetMs, DurationMs: f.DurationMs, Size: f.Size,
			Mime: playerMime(f.Container, f.Codec), NeedsCompat: media.MayNeedCompat(f.Container, f.Codec),
			Playable: streamable(book, f), StreamURL: apiPrefix + "stream/" + f.ID, PeaksURL: apiPrefix + "peaks/" + f.ID,
		})
	}
	return out
}

func apiProgressOf(p progress.Progress) apiProgress {
	return apiProgress{
		BookID: p.BookID, PositionMs: p.PositionMs, DurationMs: p.DurationMs, Finished: p.Finished(),
		FinishedAt: p.FinishedAt, ClientUpdatedAt: p.ClientUpdatedAt, ServerUpdatedAt: p.ServerUpdatedAt,
		DeviceID: p.DeviceID, DeviceLabel: p.DeviceLabel,
	}
}

func apiShelfOf(sh library.ShelfSummary) apiShelf {
	return apiShelf{
		ID: sh.ID, Name: sh.Name, OwnerName: sh.OwnerName, Shared: sh.Shared, Books: sh.Books,
		CreatedAt: sh.CreatedAt, UpdatedAt: sh.UpdatedAt,
	}
}

func apiShelvesOf(in []library.ShelfSummary) []apiShelf {
	out := make([]apiShelf, 0, len(in))
	for _, sh := range in {
		out = append(out, apiShelfOf(sh))
	}
	return out
}
