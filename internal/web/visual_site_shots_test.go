//go:build dev

package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html"
	"image/png"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestVisualSiteShots makes the screenshots of the public site from the real pages: a small family
// library of public-domain books (no real files, no Audible art), seeded here, shot in Chromium at
// the sizes the site uses. It runs only when TENDSHELF_SITE_SHOTS names an output folder
// (`make site-shots`), so `make visual` and CI skip it. Needs TENDSHELF_TEST_BROWSER.
//
// Everything on screen is repeatable: the clock stands on Saturday 2026-11-07 10:00 UTC, ids are
// seeded, covers are drawn by the browser from fixed colours, tokens in addresses are replaced by
// fixed ones and the address of the server is the fake https://books.example.org.
func TestVisualSiteShots(t *testing.T) {
	out := os.Getenv("TENDSHELF_SITE_SHOTS")
	if out == "" {
		t.Skip("TENDSHELF_SITE_SHOTS not set")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	b := chromium(t)
	w := siteWorld(t)
	base := w.serve(t)
	w.drawCovers(t, b, base)
	w.seedEbook(t)
	w.seedPeople(t)

	shot := func(name string) string { return filepath.Join(out, name) }

	// 1. Home on the desktop, with the player bar playing the book in the middle of its chapter.
	t.Run("home-desktop", func(t *testing.T) {
		w.theme(t, "dark")
		tab := siteTab(t, b, 1440, 900, false)
		tab.Navigate(base + "/")
		tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
		playerClickSel(t, tab, "main [data-ts-play]")
		tab.WaitFor(`window.tsPlayer.state().playing`, 10*time.Second)
		time.Sleep(1500 * time.Millisecond)
		siteSettle(tab)
		tab.Screenshot(shot("home-desktop.png"))
	})

	// 2, 4, 5, 6. The full player on a phone, its speed and sleep sheets and the chapter list.
	t.Run("player-phone", func(t *testing.T) {
		w.theme(t, "dark")
		tab := siteTab(t, b, 390, 844, true)
		tab.Navigate(base + "/libraries")
		tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
		fullpOpenPlayer(t, tab)
		siteSettle(tab)
		tab.Screenshot(shot("player-phone.png"))
		for _, sheet := range []struct{ open, sel, file string }{
			{"[data-ts-open-speed]", speedSheetSel, "player-speed-phone.png"},
			{"[data-ts-open-sleep]", sleepSheetSel, "sleep-phone.png"},
			{"[data-ts-open-chapters]", `#player-full [data-ts-chapters-sheet]`, "chapters-phone.png"},
		} {
			playerClickSel(t, tab, sheet.open)
			speedWaitSheet(tab, sheet.sel)
			time.Sleep(500 * time.Millisecond)
			siteSettle(tab)
			tab.Screenshot(shot(sheet.file))
			fullpKey(t, tab, "Escape", 27)
			speedWaitSheetClosed(tab, sheet.sel)
			time.Sleep(300 * time.Millisecond)
		}
	})

	t.Run("library-phone", func(t *testing.T) {
		w.theme(t, "dark")
		tab := siteTab(t, b, 390, 844, true)
		tab.Navigate(base + "/libraries")
		siteSettle(tab)
		tab.Screenshot(shot("library-phone.png"))
	})

	// 7. The reader in sepia.
	for _, rs := range []struct {
		file string
		w, h int
		mob  bool
	}{{"reader-sepia-desktop.png", 1280, 800, false}, {"reader-sepia-phone.png", 390, 844, true}} {
		rs := rs
		t.Run(rs.file, func(t *testing.T) {
			w.theme(t, "sepia")
			tab := siteTab(t, b, rs.w, rs.h, rs.mob)
			at := ""
			if rs.mob { // the phone's first screen is the heading, so open at the paragraph with the marks
				at = "?at=" + w.readAt
			}
			tab.Navigate(base + "/books/" + w.ti.ID + "/read" + at)
			tab.WaitFor(`document.querySelector("#reader") !== null`, 10*time.Second)
			siteSettle(tab)
			tab.Screenshot(shot(rs.file))
		})
	}

	// 8. People and phones.
	t.Run("invite-desktop", func(t *testing.T) {
		w.theme(t, "dark")
		tab := siteTab(t, b, 1280, 800, false)
		tab.Navigate(base + "/admin/users")
		siteSettle(tab)
		tab.Screenshot(shot("invite-desktop.png"))
	})
	t.Run("add-phone-desktop", func(t *testing.T) {
		w.theme(t, "dark")
		tab := siteTab(t, b, 1280, 800, false)
		tab.Navigate(base + "/vis/add-phone")
		tab.WaitFor(`document.querySelector(".ts-qr") !== null`, 5*time.Second)
		siteSettle(tab)
		tab.Screenshot(shot("add-phone-desktop.png"))
	})

	// 9. Listening statistics.
	t.Run("stats-phone", func(t *testing.T) {
		w.theme(t, "dark")
		tab := siteTab(t, b, 390, 844, true)
		tab.Navigate(base + "/me/listening?range=year")
		siteSettle(tab)
		tab.Screenshot(shot("stats-phone.png"))
	})

	// 10. Profile.
	t.Run("profile-desktop", func(t *testing.T) {
		w.theme(t, "dark")
		tab := siteTab(t, b, 1280, 800, false)
		tab.Navigate(base + "/me")
		tab.WaitFor(`document.getElementById("pf-stats") !== null`, 5*time.Second)
		siteSettle(tab)
		tab.Screenshot(shot("profile-desktop.png"))
	})
}

// ---- the world ----

var siteNow = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

// siteTitle is one book of the family library. hours and mins are the length of the recording.
// progress is the percent listened (0 for none); finished is the day it was finished (empty for
// not finished).
type siteTitle struct {
	title, author, narrator, series, seq, genre string
	hours, mins, year                           int
	bg, fg                                      string // the cover: one colour block and the colour of its type
	progress                                    int
	ago                                         time.Duration // when it was last played, before siteNow
	finished                                    string
}

var siteTitles = []siteTitle{
	{title: "Treasure Island", author: "Robert Louis Stevenson", narrator: "Hugh Calder", genre: "Adventure", hours: 8, mins: 24, year: 1883, bg: "#1f4a52", fg: "#f4ead5", progress: 52, ago: 20 * time.Minute},
	{title: "Pride and Prejudice", author: "Jane Austen", narrator: "Nora Vance", genre: "Romance", hours: 11, mins: 40, year: 1813, bg: "#8c3b4a", fg: "#f6e9d8"},
	{title: "Moby-Dick", author: "Herman Melville", narrator: "Samuel Adeyemi", genre: "Adventure", hours: 24, mins: 30, year: 1851, bg: "#1d3557", fg: "#e9eef5", progress: 6, ago: 9 * 24 * time.Hour},
	{title: "Dracula", author: "Bram Stoker", narrator: "Marcus Oyelaran", genre: "Gothic", hours: 15, mins: 30, year: 1897, bg: "#5a1a22", fg: "#f0e2d0", progress: 31, ago: 2 * 24 * time.Hour},
	{title: "Frankenstein", author: "Mary Shelley", narrator: "Eleanor Whitcombe", genre: "Gothic", hours: 8, mins: 20, year: 1818, bg: "#3b4a3a", fg: "#e8efd9", finished: "2026-09-18"},
	{title: "The Time Machine", author: "H. G. Wells", narrator: "Hugh Calder", genre: "Science fiction", hours: 3, mins: 30, year: 1895, bg: "#c9772b", fg: "#1c1208", finished: "2026-10-04"},
	{title: "Jane Eyre", author: "Charlotte Brontë", narrator: "Priya Raman", genre: "Romance", hours: 19, mins: 0, year: 1847, bg: "#4a3a5e", fg: "#f1e6d6", finished: "2026-06-21"},
	{title: "Great Expectations", author: "Charles Dickens", narrator: "Marcus Oyelaran", genre: "Classics", hours: 19, mins: 10, year: 1861, bg: "#6b5a3a", fg: "#f4ecd8", finished: "2026-03-14"},
	{title: "Little Women", author: "Louisa May Alcott", narrator: "Nora Vance", genre: "Classics", hours: 21, mins: 0, year: 1868, bg: "#d98e8e", fg: "#3a1a1a"},
	{title: "The Adventures of Sherlock Holmes", author: "Arthur Conan Doyle", narrator: "Hugh Calder", series: "Sherlock Holmes", seq: "3", genre: "Mystery", hours: 11, mins: 0, year: 1892, bg: "#2c2f36", fg: "#e8c15a"},
	{title: "A Tale of Two Cities", author: "Charles Dickens", narrator: "Samuel Adeyemi", genre: "Classics", hours: 14, mins: 0, year: 1859, bg: "#7a2e2e", fg: "#f4e8d4"},
	{title: "The Odyssey", author: "Homer", narrator: "Eleanor Whitcombe", genre: "Epic", hours: 12, mins: 0, year: 0, bg: "#2b6a8f", fg: "#f4f1e6", finished: "2026-01-30"},
	{title: "Emma", author: "Jane Austen", narrator: "Priya Raman", genre: "Romance", hours: 16, mins: 0, year: 1815, bg: "#e7c9a0", fg: "#3a2a1c", progress: 74, ago: 3 * 24 * time.Hour},
	{title: "Persuasion", author: "Jane Austen", narrator: "Nora Vance", genre: "Romance", hours: 7, mins: 30, year: 1817, bg: "#7f9a8a", fg: "#1d2a24", finished: "2026-08-02"},
	{title: "Walden", author: "Henry David Thoreau", narrator: "Samuel Adeyemi", genre: "Nature", hours: 11, mins: 0, year: 1854, bg: "#3f6b4a", fg: "#f1ecd8", progress: 12, ago: 5 * 24 * time.Hour},
	{title: "The War of the Worlds", author: "H. G. Wells", narrator: "Marcus Oyelaran", genre: "Science fiction", hours: 6, mins: 0, year: 1898, bg: "#a8321f", fg: "#fbe9d0"},
	{title: "Wuthering Heights", author: "Emily Brontë", narrator: "Priya Raman", genre: "Gothic", hours: 12, mins: 0, year: 1847, bg: "#4d5a6a", fg: "#eef1f5"},
	{title: "Middlemarch", author: "George Eliot", narrator: "Eleanor Whitcombe", genre: "Classics", hours: 35, mins: 0, year: 1871, bg: "#b58a3d", fg: "#231a08"},
	{title: "A Study in Scarlet", author: "Arthur Conan Doyle", narrator: "Hugh Calder", series: "Sherlock Holmes", seq: "1", genre: "Mystery", hours: 4, mins: 0, year: 1887, bg: "#8b1e2d", fg: "#f4e8d4", finished: "2026-02-08"},
	{title: "The Sign of the Four", author: "Arthur Conan Doyle", narrator: "Hugh Calder", series: "Sherlock Holmes", seq: "2", genre: "Mystery", hours: 4, mins: 0, year: 1890, bg: "#3a3a52", fg: "#ece6d6"},
	{title: "The Hound of the Baskervilles", author: "Arthur Conan Doyle", narrator: "Hugh Calder", series: "Sherlock Holmes", seq: "5", genre: "Mystery", hours: 6, mins: 0, year: 1902, bg: "#2f3b2f", fg: "#d9e2c9"},
	{title: "Sense and Sensibility", author: "Jane Austen", narrator: "Nora Vance", genre: "Romance", hours: 11, mins: 0, year: 1811, bg: "#b8c4d6", fg: "#1d2a3a"},
	{title: "The Picture of Dorian Gray", author: "Oscar Wilde", narrator: "Marcus Oyelaran", genre: "Gothic", hours: 7, mins: 0, year: 1890, bg: "#6a2c5c", fg: "#f4e6d8"},
	{title: "Alice's Adventures in Wonderland", author: "Lewis Carroll", narrator: "Priya Raman", genre: "Fantasy", hours: 2, mins: 40, year: 1865, bg: "#4f8fb8", fg: "#0f2536", finished: "2026-05-11"},
}

// Treasure Island's 34 chapters, with the book's own titles.
var siteChapters = []string{
	"The Old Sea-dog at the Admiral Benbow", "Black Dog Appears and Disappears", "The Black Spot", "The Sea-chest",
	"The Last of the Blind Man", "The Captain's Papers", "I Go to Bristol", "At the Sign of the Spy-glass",
	"Powder and Arms", "The Voyage", "What I Heard in the Apple Barrel", "Council of War",
	"How My Shore Adventure Began", "The First Blow", "The Man of the Island",
	"How the Ship Was Abandoned", "The Jolly-boat's Last Trip", "End of the First Day's Fighting",
	"The Garrison in the Stockade", "Silver's Embassy", "The Attack", "How My Sea Adventure Began",
	"The Ebb-tide Runs", "The Cruise of the Coracle", "I Strike the Jolly Roger", "Israel Hands",
	"“Pieces of Eight”", "In the Enemy's Camp", "The Black Spot Again", "On Parole",
	"The Treasure-hunt: Flint's Pointer", "The Treasure-hunt: The Voice Among the Trees",
	"The Fall of a Chieftain", "And Last",
}

type siteWorldT struct {
	a       *testApp
	ownerID string
	folder  library.Folder
	books   map[string]library.Book
	ti      library.Book
	files   []library.AudioFile
	chs     []library.Chapter
	covers  map[string]string // title -> original PNG path under the data folder
	ebooks  *ebook.Service
	peaks   *mediajobs.Peaks
	invite  share.Invite
	readAt  string // the first sentence of the paragraph with the highlights
}

func siteWorld(t *testing.T) *siteWorldT {
	t.Helper()
	px, err := auth.ParseProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, func(o *Options) {
		o.TrustedProxies = px
		o.Version = "2026.5.0" // the footer of Profile shows it
		o.Now = func() time.Time { return siteNow }
	})
	a.owner(t)
	w := &siteWorldT{a: a, books: map[string]library.Book{}, covers: map[string]string{}}
	w.ownerID = sweepUserID(t, a, a.owner(t))
	w.exec(t, `UPDATE users SET display_name = 'Ana' WHERE id = ?`, w.ownerID)
	w.exec(t, `INSERT OR REPLACE INTO settings(key, value_json) VALUES ('server.name', '"The family shelf"')`)
	w.peaks = peaksAttach(t, a)

	lib, f := librarytest.NewLibrary(t, a.lib, "Audiobooks", visualTempDir(t))
	_ = lib
	w.folder = f
	for i, s := range siteTitles {
		spec := librarytest.BookSpec{
			Title: s.title, Authors: []string{s.author}, Narrators: []string{s.narrator}, Series: s.series, Sequence: s.seq,
			Genres: []string{s.genre}, DurationMs: int64(s.hours*60+s.mins) * 60_000, PublishedYear: s.year,
			AddedAt: siteNow.UnixMilli() - int64(i)*86_400_000, Files: 1,
		}
		if s.title == "Treasure Island" {
			spec.Files = 8
		}
		w.books[s.title] = librarytest.AddBook(t, a.lib, f, spec)
	}
	w.ti = w.books["Treasure Island"]
	// Playable files for the rest (the Resume button needs a playable container); Treasure Island
	// gets real, silent WAV files below.
	w.exec(t, `UPDATE audio_files SET container='mp3', codec='mp3', duration_ms=(SELECT duration_ms FROM books b WHERE b.id = audio_files.book_id)`)
	w.seedTreasureIsland(t)
	w.seedProgress(t)
	w.seedListening(t)
	return w
}

func (w *siteWorldT) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := w.a.st.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// theme saves the owner's theme and time zone, which beat the ?theme= cookie.
func (w *siteWorldT) theme(t *testing.T, name string) {
	t.Helper()
	s, err := w.a.srv.users.LoadSettings(context.Background(), w.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	s.Theme, s.TimeZone = name, "UTC"
	if err := w.a.srv.users.SaveSettings(context.Background(), w.ownerID, s); err != nil {
		t.Fatal(err)
	}
}

// sparseWAV writes a silent 8 kHz mono WAV file of ms milliseconds without spending the disk on it:
// the header says how long it is and the file is cut to that length (the holes read as zeros).
func sparseWAV(path string, ms int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	const rate, header = 8000, 44
	dataLen := ms * rate / 1000 * 2
	h := make([]byte, header)
	le := binary.LittleEndian
	copy(h[0:], "RIFF")
	le.PutUint32(h[4:], uint32(header-8+dataLen))
	copy(h[8:], "WAVEfmt ")
	le.PutUint32(h[16:], 16)
	le.PutUint16(h[20:], 1)
	le.PutUint16(h[22:], 1)
	le.PutUint32(h[24:], rate)
	le.PutUint32(h[28:], rate*2)
	le.PutUint16(h[32:], 2)
	le.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	le.PutUint32(h[40:], uint32(dataLen))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(h); err != nil {
		return err
	}
	return f.Truncate(int64(header + dataLen))
}

// seedTreasureIsland gives the book that plays eight silent files, its 34 chapters and a voice-like
// waveform for every file.
func (w *siteWorldT) seedTreasureIsland(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	files, err := w.a.lib.AudioFiles(ctx, w.ti.ID)
	if err != nil || len(files) != 8 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	total := w.ti.DurationMs
	per := total / int64(len(files))
	rng := rand.New(rand.NewSource(21))
	for i, af := range files {
		ms := per
		if i == len(files)-1 {
			ms = total - per*int64(len(files)-1)
		}
		if err := sparseWAV(af.Path, int(ms)); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(af.Path)
		if err != nil {
			t.Fatal(err)
		}
		w.exec(t, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=?, offset_ms=?, size=?, mtime_ns=? WHERE id=?`,
			ms, int64(i)*per, fi.Size(), fi.ModTime().UnixNano(), af.ID)
		// Speech looks like phrases with a breath between them: a loudness that swells and falls.
		bins := int(ms / media.PeaksBinMs)
		vals := make([]byte, bins)
		level := 0.6
		for k := range vals {
			if k%(6+rng.Intn(5)) == 0 {
				level = 0.35 + 0.6*rng.Float64()
			}
			v := level * (0.55 + 0.45*rng.Float64())
			if rng.Intn(23) == 0 {
				v *= 0.15 // a pause
			}
			vals[k] = byte(math.Min(255, 20+v*235))
		}
		dir := mediajobs.PeaksDir(w.a.dir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		pk := media.Peaks{BinMs: media.PeaksBinMs, Values: vals}.Encode()
		if err := os.WriteFile(filepath.Join(dir, af.QuickHash+"-v1.peaks"), pk, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	w.files, _ = w.a.lib.AudioFiles(ctx, w.ti.ID)
	// Chapter lengths between four and nineteen minutes, scaled to fill the book exactly.
	weights := make([]float64, len(siteChapters))
	sum := 0.0
	for i := range weights {
		weights[i] = 4 + 15*rng.Float64()
		sum += weights[i]
	}
	var chs []library.Chapter
	at := 0.0
	for i, name := range siteChapters {
		start := int64(at / sum * float64(total))
		at += weights[i]
		end := int64(at / sum * float64(total))
		if i == len(siteChapters)-1 {
			end = total
		}
		chs = append(chs, library.Chapter{StartMs: start, EndMs: end, Title: name, Source: library.ChapterFile})
	}
	w.chs = chs
	if err := w.a.lib.Update(ctx, func(tx *library.Tx) error { return tx.ReplaceChapters(w.ti.ID, chs) }); err != nil {
		t.Fatal(err)
	}
}

// seedProgress puts Ana part-way through six books and past the end of eight.
func (w *siteWorldT) seedProgress(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, s := range siteTitles {
		b := w.books[s.title]
		switch {
		case s.progress > 0:
			at := siteNow.Add(-s.ago).UnixMilli()
			pos := b.DurationMs * int64(s.progress) / 100
			if b.ID == w.ti.ID { // a little under halfway through the chapter the percentage lands in
				for _, c := range w.chs {
					if pos >= c.StartMs && pos < c.EndMs {
						pos = c.StartMs + (c.EndMs-c.StartMs)*38/100
						break
					}
				}
			}
			if _, err := w.a.srv.progress.Save(ctx, progress.Update{UserID: w.ownerID, BookID: b.ID, PositionMs: pos,
				DurationMs: b.DurationMs, ClientUpdatedAt: at, DeviceID: "siteshots0001", DeviceLabel: "Pixel 8"}); err != nil {
				t.Fatal(err)
			}
		case s.finished != "":
			day, err := time.Parse("2006-01-02", s.finished)
			if err != nil {
				t.Fatal(err)
			}
			at := day.Add(12 * time.Hour).UnixMilli()
			w.exec(t, `INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, ?, ?, ?, ?, ?, 'siteshots0001')`, w.ownerID, b.ID, b.DurationMs, b.DurationMs, at, at, at)
		}
	}
}

// seedListening spreads listening sessions over the last two years, mostly the books Ana is in.
func (w *siteWorldT) seedListening(t *testing.T) {
	t.Helper()
	rng := rand.New(rand.NewSource(11))
	var ids []string
	for _, s := range siteTitles {
		if s.progress > 0 || s.finished != "" {
			ids = append(ids, w.books[s.title].ID)
		}
	}
	tx, err := w.a.st.Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	for day := time.Date(2024, 11, 10, 0, 0, 0, 0, time.UTC); day.Before(end); day = day.AddDate(0, 0, 1) {
		if rng.Intn(100) < 14 {
			continue
		}
		for k := 0; k < 1+rng.Intn(3); k++ {
			hour := []int{6, 7, 8, 12, 13, 17, 19, 20, 21, 22}[rng.Intn(10)]
			start := day.Add(time.Duration(hour)*time.Hour + time.Duration(rng.Intn(50))*time.Minute)
			ms := int64(12+rng.Intn(55)) * 60_000
			n++
			if _, err := tx.Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'siteshots0001', ?, ?, 0, ?, ?, ?)`, fmt.Sprintf("ses_site%d", n), w.ownerID, ids[rng.Intn(len(ids))], start.UnixMilli(), start.UnixMilli()+ms, ms, ms,
				[]int{100, 110, 125, 150}[rng.Intn(4)]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// ---- covers ----

// serve starts the server in front of the app: it answers /__cover itself (the page the browser
// draws a cover from), signs every request in as Ana, and for the pages that show an address
// answers as the fake https://books.example.org behind a proxy.
func (w *siteWorldT) serve(t *testing.T) string {
	t.Helper()
	owner := w.a.owner(t)
	var qrToken = strings.Repeat("Qr7-", 11)[:43]
	var inviteToken = strings.Repeat("Kp3_", 11)[:43]
	tokenRe := regexp.MustCompile(`/(?:pair|invite)/([A-Za-z0-9_-]{43})`)
	srv := httptest.NewServer(http.HandlerFunc(func(w2 http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__cover" {
			q := r.URL.Query()
			w2.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w2, coverPage(q.Get("t"), q.Get("a"), q.Get("bg"), q.Get("fg")))
			return
		}
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
		address := r.URL.Query().Get("address") == "1" || strings.HasPrefix(r.URL.Path, "/vis/")
		if address {
			r.Host = "books.example.org"
			r.RemoteAddr = "10.1.1.1:5000"
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		if r.URL.Path == "/vis/add-phone" || r.URL.Path == "/vis/invite-link" {
			r = w.postForPage(t, r)
		}
		rec := httptest.NewRecorder()
		w.a.h.ServeHTTP(rec, r)
		body := rec.Body.Bytes()
		if address && strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			s := string(body)
			for _, m := range tokenRe.FindAllStringSubmatch(s, -1) {
				fixed := qrToken
				if strings.Contains(m[0], "/invite/") {
					fixed = inviteToken
				}
				s = strings.ReplaceAll(s, m[1], fixed)
			}
			// Draw the QR again for the fixed address.
			for _, link := range []string{"https://books.example.org/pair/" + qrToken, "https://books.example.org/invite/" + inviteToken} {
				if strings.Contains(s, link) {
					if v, ok := qrView(link, ""); ok {
						s = regexp.MustCompile(`(<path class="ts-qr__ink" d=")[^"]*(")`).ReplaceAllString(s, "${1}"+v.Path+"${2}")
					}
				}
			}
			body = []byte(s)
		}
		for k, vs := range rec.Header() {
			if k != "Content-Length" {
				w2.Header()[k] = vs
			}
		}
		w2.WriteHeader(rec.Code)
		_, _ = w2.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// postForPage turns a plain navigation into the POST that makes the page (the code page and the
// invite link panel are answers to a POST, not pages one can open).
func (w *siteWorldT) postForPage(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	owner := w.a.owner(t)
	tok := w.a.tokenFor(t, owner)
	path := "/me/devices/add"
	if r.URL.Path == "/vis/invite-link" {
		path = "/admin/invites/" + r.URL.Query().Get("id") + "/link"
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(url.Values{"_csrf": {tok}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
	req.Host, req.RemoteAddr = r.Host, r.RemoteAddr
	if c, err := r.Cookie(themeCookie); err == nil {
		req.AddCookie(c)
	}
	return req
}

// coverPage is one cover, 640 by 640: a colour block with the title in Fraunces and the author in
// Fira Sans, set inside a thin rule. The browser draws it; the screenshot is the cover.
func coverPage(title, author, bg, fg string) string {
	size := 74
	switch n := len([]rune(title)); {
	case n > 26:
		size = 50
	case n > 16:
		size = 60
	}
	return fmt.Sprintf(`<!doctype html><meta charset="utf-8"><style>
@font-face{font-family:Fraunces;font-weight:100 900;src:url(/static/fonts/Fraunces-Variable.woff2) format("woff2")}
@font-face{font-family:"Fira Sans";font-weight:500;src:url(/static/fonts/FiraSans-Medium.woff2) format("woff2")}
html,body{margin:0;width:640px;height:640px;background:%[3]s;color:%[4]s;overflow:hidden}
.c{box-sizing:border-box;width:640px;height:640px;padding:44px;display:flex;flex-direction:column;align-items:center;justify-content:space-between;text-align:center}
.f{box-sizing:border-box;position:absolute;inset:26px;border:2px solid %[4]s;opacity:.55}
h1{margin:0;font:600 %[5]dpx/1.08 Fraunces,serif;text-wrap:balance;font-variation-settings:"opsz" 144,"SOFT" 50}
.t{flex:1;display:flex;align-items:center;justify-content:center;padding:36px 14px 0}
.r{width:64px;height:2px;background:%[4]s;opacity:.7;margin:0 auto 22px}
p{margin:0 0 20px;font:500 24px/1.2 "Fira Sans",sans-serif;letter-spacing:.2em;text-transform:uppercase}
</style><div class="f"></div><div class="c"><div class="t"><h1>%[1]s</h1></div><div><div class="r"></div><p>%[2]s</p></div></div>`,
		html.EscapeString(title), html.EscapeString(author), bg, fg, size)
}

// drawCovers has the browser draw every cover, then stores each as the book's cover with the tint
// and accent colours the cover job would have found.
func (w *siteWorldT) drawCovers(t *testing.T, b *cdp.Browser, base string) {
	t.Helper()
	tab := b.NewTab(640, 640, false, false)
	dir := t.TempDir()
	for i, s := range siteTitles {
		q := url.Values{"t": {s.title}, "a": {strings.ToUpper(s.author)}, "bg": {s.bg}, "fg": {s.fg}}
		tab.Navigate(base + "/__cover?" + q.Encode())
		path := filepath.Join(dir, fmt.Sprintf("c%d.png", i))
		tab.Screenshot(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		tint, _ := media.Tint(img)
		var accent json.RawMessage
		if acc, ok := media.Accents(tint); ok {
			accent, _ = json.Marshal(acc)
		}
		sha := fmt.Sprintf("%064x", 0x5173000+i)
		rel := filepath.Join("covers", sha+".png")
		for _, p := range []string{filepath.Join(w.a.dir, rel), mediajobs.ThumbPath(w.a.dir, sha, 160), mediajobs.ThumbPath(w.a.dir, sha, 320), mediajobs.ThumbPath(w.a.dir, sha, 640)} {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		book := w.books[s.title]
		cover := library.Cover{BookID: book.ID, Source: library.CoverFolder, Mime: "image/png", Width: 640, Height: 640, SHA256: sha, Path: rel, Tint: tint, Accent: accent}
		if err := w.a.lib.Update(context.Background(), func(tx *library.Tx) error {
			if err := tx.PutCover(&cover); err != nil {
				return err
			}
			return tx.SetBookCover(book.ID, cover.ID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	tab.Close()
}

// ---- the ebook ----

// siteEdition is the ebook of Treasure Island: the opening of chapter 1 as Stevenson wrote it (public
// domain), with one highlight, one highlight with a note and one bookmark of Ana's.
func (w *siteWorldT) seedEbook(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	svc := ebook.New(w.a.st, w.a.lib, w.a.runner, w.a.dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Register(w.a.runner); err != nil {
		t.Fatal(err)
	}
	w.a.srv.ebooks = svc
	w.ebooks = svc
	paras := []string{
		"Squire Trelawney, Dr. Livesey, and the rest of these gentlemen having asked me to write down the whole particulars about Treasure Island, from the beginning to the end, keeping nothing back but the bearings of the island, and that only because there is still treasure not yet lifted, I take up my pen in the year of grace 17— and go back to the time when my father kept the Admiral Benbow inn and the brown old seaman with the sabre cut first took up his lodging under our roof.",
		"I remember him as if it were yesterday, as he came plodding to the inn door, his sea-chest following behind him in a hand-barrow—a tall, strong, heavy, nut-brown man, his tarry pigtail falling over the shoulders of his soiled blue coat, his hands ragged and scarred, with black, broken nails, and the sabre cut across one cheek, a dirty, livid white. I remember him looking round the cove and whistling to himself as he did so, and then breaking out in that old sea-song that he sang so often afterwards:",
		"“Fifteen men on the dead man’s chest— Yo-ho-ho, and a bottle of rum!”",
		"in the high, old tottering voice that seemed to have been tuned and broken at the capstan bars. Then he rapped on the door with a bit of stick like a handspike that he carried, and when my father appeared, called roughly for a glass of rum.",
		"He was a very silent man by custom. All day he hung round the cove, or upon the cliffs, with a brass telescope; all evening he sat in a corner of the parlour next the fire, and drank rum and water very strong. Mostly he would not speak when spoken to, only look up sudden and fierce and blow through his nose like a fog-horn; and we and the people who came about our house soon learned to let him be.",
		"Day after day, when he came back from his stroll, he would ask if any seafaring men had gone by along the road.",
	}
	// The reader shows the chapter's title itself, so the body is the paragraphs alone.
	var body strings.Builder
	for _, p := range paras {
		body.WriteString("<p>" + html.EscapeString(p) + "</p>\n")
	}
	epub := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Treasure Island", Authors: []string{"Robert Louis Stevenson"}, Chapters: []ebooktest.EPUBChapter{
		{Title: "The Old Sea-dog at the Admiral Benbow", EpubType: "chapter", Raw: body.String()},
	}})
	if err := w.a.runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	f, err := svc.AddUpload(ctx, w.ti, w.ownerID, "treasure-island.epub", bytes.NewReader(epub))
	if err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(30 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		cur, ok, err := svc.Store().File(ctx, f.ID)
		if err != nil || !ok {
			t.Fatalf("ebook file: %v %v", ok, err)
		}
		if cur.State == "ready" {
			f = cur
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the ebook never became ready (%s)", cur.State)
		}
	}
	if err := w.a.runner.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	ed, ok, err := svc.Store().CurrentEdition(ctx, f.ID)
	if err != nil || !ok {
		t.Fatalf("edition: %v %v", ok, err)
	}
	doc, err := ebook.ReadDoc(w.a.dir+"/text", ed.DocSHA256)
	if err != nil {
		t.Fatal(err)
	}
	var ch ebook.Chapter
	for _, c := range doc.Chapters {
		for _, blk := range c.Blocks {
			if strings.Contains(blk.Text, "nut-brown man") {
				ch = c
			}
		}
	}
	if len(ch.Blocks) == 0 {
		t.Fatal("the ebook has no chapter with the passage")
	}
	owner := w.a.owner(t)
	mark := func(from, to, colour, note string) {
		t.Helper()
		var blk ebook.Block
		for _, cand := range ch.Blocks {
			if strings.Contains(cand.Text, from) {
				blk = cand
				break
			}
		}
		if blk.ID == "" {
			t.Fatalf("%q is not in the chapter", from)
		}
		off := func(sub string) int {
			i := strings.Index(blk.Text, sub)
			if i < 0 {
				t.Fatalf("%q is not in its block", sub)
			}
			return len(utf16.Encode([]rune(blk.Text[:i])))
		}
		start, end := off(from), off(to)+len(utf16.Encode([]rune(to)))
		if w.readAt == "" {
			w.readAt = ebook.SentenceID(blk.ID, 0)
		}
		sid := func(o int) string {
			for k, s := range blk.Sentences {
				if o >= s[0] && o <= s[1] {
					return ebook.SentenceID(blk.ID, k)
				}
			}
			return ebook.SentenceID(blk.ID, 0)
		}
		form := url.Values{"_csrf": {w.a.tokenFor(t, owner)}, "edition": {ed.ID}, "start_sid": {sid(start)}, "start_off": {fmt.Sprint(start)},
			"end_sid": {sid(end)}, "end_off": {fmt.Sprint(end)}, "colour": {colour}, "note": {note}}
		rec := w.a.do(http.MethodPost, "/hx/books/"+w.ti.ID+"/highlights", form, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, owner)
		if rec.Code != http.StatusOK {
			t.Fatalf("highlight: %d %s", rec.Code, rec.Body.String())
		}
	}
	mark("a tall, strong, heavy, nut-brown man", "a tall, strong, heavy, nut-brown man", "yellow", "")
	mark("the sabre cut across one cheek", "a dirty, livid white", "pink", "Stevenson sketches him in one sentence and he is unforgettable.")
	mark("he hung round the cove", "with a brass telescope", "blue", "")
}

// ---- people ----

func (w *siteWorldT) seedPeople(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	person := func(name string, role users.Role) string {
		id := sweepUserID(t, w.a, w.a.as(t, role))
		w.exec(t, `UPDATE users SET display_name = ?, username = ? WHERE id = ?`, name, strings.ToLower(strings.NewReplacer("á", "a").Replace(name)), id)
		return id
	}
	daniel := person("Daniel", users.RoleAdmin)
	maya := person("Maya", users.RoleMember)
	lena := person("Lena", users.RoleMember)
	rosa := person("Rosa", users.RoleGuest)
	tomas := person("Tomás", users.RoleGuest)

	shelf := func(name string, titles ...string) library.Shelf {
		sh, err := w.a.lib.CreateShelf(ctx, w.ownerID, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, title := range titles {
			if err := w.a.lib.AddToShelf(ctx, w.ownerID, sh.ID, w.books[title].ID); err != nil {
				t.Fatal(err)
			}
		}
		return sh
	}
	shelf("Rainy Sundays", "Jane Eyre", "Wuthering Heights", "Emma", "Persuasion")
	shelf("Ghost stories and gothic", "Dracula", "Frankenstein", "The Picture of Dorian Gray")
	forRosa := shelf("For Rosa", "Alice's Adventures in Wonderland", "Little Women", "Treasure Island")

	lib := w.folder.LibraryID
	w.exec(t, `INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gs1', ?, 'all', 1, 1), ('gs2', ?, 'all', 1, 1)`, maya, lena)
	w.exec(t, `INSERT INTO grants(id, user_id, scope, shelf_id, can_download, created_at, updated_at) VALUES ('gs3', ?, 'shelf', ?, 1, 1, 1)`, rosa, forRosa.ID)
	w.exec(t, `INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at) VALUES ('gs4', ?, 'library', ?, 1, 1)`, tomas, lib)
	day := int64(24 * 3_600_000)
	nowMs := siteNow.UnixMilli()
	n := 0
	session := func(user, title string, hours, endedAt int64) {
		n++
		w.exec(t, `INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, 1, ?, 100)`, fmt.Sprintf("ses_people%d", n), user, w.books[title].ID, endedAt-hours*3_600_000, endedAt, hours*3_600_000)
	}
	session(daniel, "Moby-Dick", 14, nowMs-3*3_600_000)
	session(maya, "Dracula", 9, nowMs-26*3_600_000)
	session(rosa, "Alice's Adventures in Wonderland", 3, nowMs-40*60_000)
	session(lena, "Emma", 6, nowMs-3*day)
	session(tomas, "Walden", 2, nowMs-9*day)

	create := func(spec share.InviteSpec) (share.Invite, string) {
		u, err := w.a.srv.users.ByID(ctx, w.ownerID)
		if err != nil {
			t.Fatal(err)
		}
		var inv share.Invite
		var tok string
		if err := w.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
			var err error
			inv, tok, err = w.a.srv.invites.Create(ctx, tx, u, spec)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return inv, tok
	}
	w.invite, _ = create(share.InviteSpec{Name: "Mateo", Note: "Start with Treasure Island, it is the one we are listening to.", Role: users.RoleMember,
		ExpiresAt: siteNow.AddDate(0, 0, 14).UnixMilli(), Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: lib, Download: true}}})
	create(share.InviteSpec{Name: "Ines", Role: users.RoleMember, AddsBooks: true, ExpiresAt: siteNow.AddDate(0, 0, 7).UnixMilli(),
		Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: lib}}})
}

// siteTab opens a page of w by h CSS pixels at twice the pixel density.
func siteTab(t *testing.T, b *cdp.Browser, wd, ht int, mobile bool) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(wd, ht, mobile, false)
	if err := tab.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": wd, "height": ht, "deviceScaleFactor": 2, "mobile": mobile}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("Emulation.setTimezoneOverride", map[string]any{"timezoneId": "UTC"}, nil); err != nil {
		t.Fatal(err)
	}
	return tab
}

// siteSettle waits for fonts and images and lets the page rest, and clears focus so no ring shows.
func siteSettle(tab *cdp.Tab) {
	_ = tab.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 1, "y": 1}, nil)
	// A lazy image that is off screen never decodes, so each wait is capped.
	tab.Eval(`Promise.race([document.fonts.ready.then(() => Promise.all([...document.images].map(i => i.decode().catch(() => 0)))), new Promise(r => setTimeout(r, 3000))]).then(() => { if (document.activeElement && document.activeElement !== document.body) document.activeElement.blur(); return true; })`, nil)
	time.Sleep(400 * time.Millisecond)
}
