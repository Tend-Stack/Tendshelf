package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// KOReader progress sync (S6, decision 0025 and docs/api.md). KOReader's own protocol, not /api/v1: the
// header pair x-auth-user and x-auth-key carries a user name and the MD5 of a per-device app key, and a
// document is named by the partial MD5 of its bytes. The routes sit in front of the outer mux (a server
// address typed with a trailing slash sends "//users/auth", which the mux would redirect, and a redirected
// PUT fails). Nothing here reads book text into a response: a place is stored as KOReader sent it.

const (
	kosyncPrefix    = "/kosync/"
	kosyncMaxBody   = 8 << 10
	kosyncDeviceMax = 64
	kosyncProgMax   = 512
)

var (
	kosyncDocRE = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	kosyncKeyRE = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	slashRuns   = regexp.MustCompile(`/{2,}`)
)

const (
	kosyncUnauthorized = `{"code":2001,"message":"Unauthorized"}`
	kosyncNoCreate     = `{"code":2002,"message":"Make a KOReader key in Tendshelf, then choose Login."}`
	kosyncInvalid      = `{"code":2003,"message":"Invalid fields"}`
)

// kosyncFront sends every path under /kosync/ to the sync routes and everything else to next.
func (s *server) kosyncFront(next http.Handler) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /users/create", func(w http.ResponseWriter, r *http.Request) {
		kosyncRaw(w, http.StatusPaymentRequired, kosyncNoCreate) // nobody is ever created here
	})
	guard := func(h http.HandlerFunc) http.Handler {
		return s.keyGuard(auth.KindKOReader, kosyncCreds, keyReplies{refuse: s.kosyncRefuse, say: kosyncSay}, h)
	}
	m.Handle("GET /users/auth", guard(func(w http.ResponseWriter, r *http.Request) {
		kosyncJSON(w, http.StatusOK, map[string]string{"authorized": "OK"})
	}))
	m.Handle("PUT /syncs/progress", guard(s.putKosync))
	m.Handle("GET /syncs/progress/{document}", guard(s.getKosync))
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		kosyncRaw(w, http.StatusNotFound, `{"code":2004,"message":"Not found"}`)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, kosyncPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = slashRuns.ReplaceAllString("/"+strings.TrimPrefix(r.URL.Path, kosyncPrefix), "/")
		r2.URL.RawPath = ""
		m.ServeHTTP(w, r2)
	})
}

// kosyncCreds reads x-auth-user and x-auth-key. The key is already the MD5 of what the person typed.
func kosyncCreds(kind auth.DeviceKind) keyCreds {
	return func(r *http.Request) (string, [32]byte, bool, bool) {
		name, key := r.Header.Get("x-auth-user"), r.Header.Get("x-auth-key")
		if name == "" && key == "" {
			return "", [32]byte{}, false, false
		}
		if !kosyncKeyRE.MatchString(key) {
			return "", [32]byte{}, true, false
		}
		return name, auth.KeyHashFromMD5(key), true, true
	}
}

func (s *server) kosyncRefuse(w http.ResponseWriter, ip string, counted bool) {
	if counted {
		s.keyLimiter.Fail(ip)
	}
	kosyncRaw(w, http.StatusUnauthorized, kosyncUnauthorized)
}

func kosyncSay(w http.ResponseWriter, status int, msg string) {
	kosyncJSON(w, status, map[string]any{"code": 2005, "message": msg})
}

func kosyncRaw(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func kosyncJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b, status = []byte(`{}`), http.StatusInternalServerError
	}
	kosyncRaw(w, status, string(b))
}

type kosyncBody struct {
	Document   string   `json:"document"`
	Progress   string   `json:"progress"`
	Percentage *float64 `json:"percentage"`
	Device     string   `json:"device"`
	DeviceID   string   `json:"device_id"`
}

func (s *server) putKosync(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	var b kosyncBody
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, kosyncMaxBody))
	if err != nil || json.Unmarshal(raw, &b) != nil || b.Percentage == nil ||
		!kosyncDocRE.MatchString(b.Document) || math.IsNaN(*b.Percentage) || *b.Percentage < 0 || *b.Percentage > 1 ||
		b.Progress == "" || len(b.Progress) > kosyncProgMax || len(b.Device) > kosyncDeviceMax || len(b.DeviceID) > kosyncDeviceMax ||
		!utf8.ValidString(b.Progress+b.Device+b.DeviceID) {
		kosyncRaw(w, http.StatusBadRequest, kosyncInvalid)
		return
	}
	doc := strings.ToLower(b.Document)
	now := s.now().UnixMilli()
	if err := s.progress.KosyncPut(r.Context(), u.ID, progress.KosyncRow{Document: doc, Progress: b.Progress,
		Percentage: *b.Percentage, Device: b.Device, DeviceID: b.DeviceID, UpdatedAt: now}); err != nil {
		slog.Error("kosync: store place", "err", err)
		kosyncSay(w, http.StatusInternalServerError, "Something went wrong on our side. Try again in a moment.")
		return
	}
	if err := s.kosyncToReading(r, u, doc, *b.Percentage, b.DeviceID, now); err != nil {
		slog.Warn("kosync: could not turn a place into a reading place", "err", err) // the KOReader row is kept
	}
	kosyncJSON(w, http.StatusOK, map[string]any{"document": doc, "timestamp": now / 1000})
}

// kosyncMatch finds the book a document digest names that the person may read: the oldest ready file with
// that digest on a book they have ReadText for.
func (s *server) kosyncMatch(r *http.Request, u users.User, doc string) (ebook.File, bool, error) {
	files, err := s.ebooks.Store().FilesByDigest(r.Context(), doc)
	if err != nil {
		return ebook.File{}, false, err
	}
	for _, f := range files {
		b, err := s.lib.Book(r.Context(), f.BookID)
		if err != nil {
			continue // the book is gone; the next file may be on another
		}
		if s.can.Can(r.Context(), u, share.ReadText, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) {
			return f, true, nil
		}
	}
	return ebook.File{}, false, nil
}

// kosyncToReading carries a KOReader place into the person's reading place, by percentage. It does nothing
// when no book the person may read has this digest.
func (s *server) kosyncToReading(r *http.Request, u users.User, doc string, pct float64, deviceID string, now int64) error {
	f, ok, err := s.kosyncMatch(r, u, doc)
	if err != nil || !ok {
		return err
	}
	ed, ok, err := s.ebooks.Store().NewestEdition(r.Context(), f.ID)
	if err != nil || !ok {
		return err
	}
	d, err := s.readerDocs.get(filepath.Join(s.dataDir, "text"), ed.DocSHA256)
	if err != nil {
		return err
	}
	a, ok := d.AnchorAtChar(int(math.Round(pct * float64(d.TotalUnits()))))
	if !ok {
		return errors.New("kosync: the document has no text")
	}
	ch, _, _, valid := ebook.ParseSID(a.SID)
	if !valid {
		return fmt.Errorf("kosync: bad sentence id %q", a.SID)
	}
	charPos, _, _ := d.CharPos(a)
	sum := sha256.Sum256([]byte(deviceID))
	_, _, err = s.progress.SaveReading(r.Context(), progress.ReadingSave{
		UserID: u.ID, BookID: f.BookID, EditionID: ed.ID, ChapterIdx: ch, SID: a.SID, Off: a.Off, CharPos: charPos,
		PercentBP: int(math.Round(pct * 10000)), ClientUpdatedAt: now,
		DeviceID: "ko-" + hex.EncodeToString(sum[:])[:16], DeviceLabel: "KOReader",
	})
	return err
}

func (s *server) getKosync(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	docIn := r.PathValue("document")
	if !kosyncDocRE.MatchString(docIn) {
		kosyncRaw(w, http.StatusBadRequest, kosyncInvalid)
		return
	}
	doc := strings.ToLower(docIn)
	row, have, err := s.progress.KosyncGet(r.Context(), u.ID, doc)
	if err != nil {
		slog.Error("kosync: read place", "err", err)
		kosyncSay(w, http.StatusInternalServerError, "Something went wrong on our side. Try again in a moment.")
		return
	}
	if out, ok := s.kosyncTendshelfPlace(r, u, doc, row, have); ok {
		kosyncJSON(w, http.StatusOK, out)
		return
	}
	if !have {
		kosyncRaw(w, http.StatusOK, `{}`)
		return
	}
	kosyncJSON(w, http.StatusOK, map[string]any{"document": doc, "progress": row.Progress, "percentage": row.Percentage,
		"device": row.Device, "device_id": row.DeviceID, "timestamp": row.UpdatedAt / 1000})
}

// kosyncTendshelfPlace answers the person's Tendshelf reading place for the document when it is newer than what
// KOReader last sent (or KOReader sent nothing) and was not itself written from KOReader. EPUB and PDF only:
// the place is a chapter start or a page, which KOReader opens at or before the real place.
func (s *server) kosyncTendshelfPlace(r *http.Request, u users.User, doc string, row progress.KosyncRow, have bool) (map[string]any, bool) {
	f, ok, err := s.kosyncMatch(r, u, doc)
	if err != nil || !ok || (f.Format != "epub" && f.Format != "pdf") {
		return nil, false
	}
	rp, ok, err := s.progress.Reading(r.Context(), u.ID, f.BookID)
	if err != nil || !ok || strings.HasPrefix(rp.DeviceID, "ko-") || (have && rp.ClientUpdatedAt <= row.UpdatedAt) {
		return nil, false
	}
	var pos string
	switch f.Format {
	case "epub":
		pos = fmt.Sprintf("/body/DocFragment[%d]/body", rp.ChapterIdx)
	case "pdf":
		ed, ok, err := s.ebooks.Store().NewestEdition(r.Context(), f.ID)
		if err != nil || !ok || ed.PageCount < 1 {
			return nil, false
		}
		pos = fmt.Sprint(max(1, int(math.Round(float64(rp.PercentBP)/10000*float64(ed.PageCount)))))
	}
	return map[string]any{"document": doc, "progress": pos, "percentage": float64(rp.PercentBP) / 10000,
		"device": "Tendshelf", "device_id": "tendshelf", "timestamp": rp.ClientUpdatedAt / 1000}, true
}
