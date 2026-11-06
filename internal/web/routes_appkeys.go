package web

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// App keys (decision 0025): a typed key for one surface, kept in the devices table. They are made on
// "Your devices" and turned off with the existing device sign-out. The key is shown once, in the answer
// to the POST; only its SHA-256 is stored and it never reaches a URL, a log or the audit detail.

func (s *server) registerAppKeys(mux *http.ServeMux) {
	mux.HandleFunc("POST /me/app-keys", s.postAppKey)
}

// appKeysView is the App keys section for the signed-in person's own keys.
func (s *server) appKeysView(r *http.Request) (pages.AppKeysView, error) {
	u := currentUser(r)
	keys, err := s.devices.OwnKeys(r.Context(), u.ID)
	if err != nil {
		return pages.AppKeysView{}, err
	}
	v := pages.AppKeysView{
		CSRF: s.csrf.TokenFrom(r.Context()), Heading: akeyHeading, Lead: akeyLead, None: akeyNone,
		NameLabel: akeyNameLabel, NameHint: akeyNameHint, KindLabel: akeyKindLabel,
		KindOPDS: akeyKindOPDS, KindKO: akeyKindKO, Make: akeyMake, TurnOff: akeyTurnOff,
	}
	now := s.now()
	for _, k := range keys {
		meta := auth.KindLabel(k.Kind) + " · " + msgNotUsedYet
		if k.LastSeenAt > 0 {
			meta = auth.KindLabel(k.Kind) + " · " + agoText(now, time.UnixMilli(k.LastSeenAt))
		}
		v.Keys = append(v.Keys, pages.AppKeyLine{Title: k.Name, Meta: meta, Action: "/me/devices/" + k.ID + "/signout", Aria: akeyTurnOff + " " + k.Name})
	}
	return v, nil
}

// postAppKey makes a key for the signed-in person and answers 200 with the key, never cached.
func (s *server) postAppKey(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	kind := auth.DeviceKind(r.PostFormValue("kind"))
	if !auth.IsKeyKind(kind) {
		s.keyError(w, r, http.StatusBadRequest, akeyBadKind)
		return
	}
	var secret string
	var dev auth.Device
	err := s.lib.Store().WriteTx(r.Context(), func(tx *sql.Tx) error {
		var err error
		secret, dev, err = s.devices.IssueKey(r.Context(), tx, u.ID, r.PostFormValue("name"), kind)
		if err != nil {
			return err
		}
		return db.Audit(r.Context(), tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: u.ID, Action: "key.create", Target: u.ID,
			Detail: map[string]string{"device": dev.ID, "kind": string(kind)}}) // never the key
	})
	switch {
	case errors.Is(err, auth.ErrTooManyDevices):
		s.keyError(w, r, http.StatusConflict, akeyLimit)
		return
	case err != nil:
		s.serverError(w, r, "make app key", err)
		return
	}
	path, note := "/opds", akeyOPDSNote
	if kind == auth.KindKOReader {
		path, note = "/kosync", akeyKONote
	}
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	v := pages.AppKeyShownView{
		Title: akeyShownTitle, Lead: akeyShownLead, Note: note,
		AddressLabel: akeyAddress, Address: scheme + r.Host + path,
		UserLabel: akeyUser, User: u.Username, KeyLabel: akeyKey, Key: secret, Back: akeyBack,
	}
	w.Header().Set("Cache-Control", "no-store")
	s.prefsPage(w, r, http.StatusOK, akeyShownTitle, "profile", "devices", false, pages.AppKeyShown(v), nil, append(pages.MeStyles, pages.AppKeyStyles...)...)
}

// keyError answers a refused key request: a toast for htmx, plain words otherwise.
func (s *server) keyError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(msg))
		return
	}
	http.Error(w, msg, status)
}
