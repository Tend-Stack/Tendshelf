package metadata

// Lookup sites an admin added (decision 0046): the table, its validation, and the hand-over to the
// online client. The authorization value is a secret: it is stored as typed, read only to hand to the
// client, and never appears in a view, an audit row, an error or a log.

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
)

// MaxCustomProviders is how many lookup sites an admin may add.
const MaxCustomProviders = 10

// ErrProviderNotFound is returned for a site that is not (or no longer) in the list.
var ErrProviderNotFound = errors.New("metadata: lookup site not found")

// CustomProviderView is what a page may show about a site. The header is never part of it.
type CustomProviderView struct {
	ID, Name, Host string
	HasAuth        bool
}

// CustomProviderInput is what an admin typed.
type CustomProviderInput struct{ Name, Address, Auth string }

// ProviderFieldError says which field is wrong and why. Field is name, address or auth; Code is one
// of name, name_taken, address, address_parts, port, blocked, insecure, auth, too_many.
type ProviderFieldError struct{ Field, Code string }

func (e *ProviderFieldError) Error() string {
	return "metadata: lookup site " + e.Field + ": " + e.Code
}

func fieldErr(field, code string) error { return &ProviderFieldError{Field: field, Code: code} }

// List returns the added sites, oldest first.
func (s *Service) List(ctx context.Context) ([]CustomProviderView, error) {
	rows, err := s.repo.Store().Reader().QueryContext(ctx, `SELECT id, name, base_url, auth_value <> '' FROM custom_providers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomProviderView
	for rows.Next() {
		var v CustomProviderView
		var base string
		if err := rows.Scan(&v.ID, &v.Name, &base, &v.HasAuth); err != nil {
			return nil, err
		}
		v.Host = hostOf(base)
		out = append(out, v)
	}
	return out, rows.Err()
}

func hostOf(base string) string {
	if u, err := url.Parse(base); err == nil {
		return u.Host
	}
	return ""
}

// checkProviderInput validates what an admin typed and returns the trimmed name, the address to
// store (scheme and host in lower case, no trailing slash) and the header value.
func checkProviderInput(in CustomProviderInput) (name, base, auth string, err error) {
	name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
		return "", "", "", fieldErr("name", "name")
	}
	raw := strings.TrimSpace(in.Address)
	u, perr := url.Parse(raw)
	if perr != nil || u.Opaque != "" || u.Host == "" || u.Hostname() == "" {
		return "", "", "", fieldErr("address", "address")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", "", fieldErr("address", "address")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return "", "", "", fieldErr("address", "address_parts")
	}
	if !podnet.PortOK(u.Port()) {
		return "", "", "", fieldErr("address", "port")
	}
	if a, aerr := netip.ParseAddr(u.Hostname()); aerr == nil {
		switch podnet.ClassifyAddr(a) {
		case podnet.AddrRefused:
			return "", "", "", fieldErr("address", "blocked")
		case podnet.AddrPublic:
			if scheme == "http" {
				return "", "", "", fieldErr("address", "insecure")
			}
		}
	}
	base = scheme + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/")
	if len(base) > 500 {
		return "", "", "", fieldErr("address", "address")
	}
	auth = in.Auth
	if len(auth) > 1000 {
		return "", "", "", fieldErr("auth", "auth")
	}
	for i := 0; i < len(auth); i++ {
		if auth[i] < 0x20 || auth[i] > 0x7E {
			return "", "", "", fieldErr("auth", "auth")
		}
	}
	return name, base, auth, nil
}

// AddCustomProvider validates and stores a site, records who added it, and returns its id.
func (s *Service) AddCustomProvider(ctx context.Context, in CustomProviderInput, userID string) (string, error) {
	name, base, auth, err := checkProviderInput(in)
	if err != nil {
		return "", err
	}
	id := db.NewID("cp")
	err = s.repo.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_providers`).Scan(&n); err != nil {
			return err
		}
		if n >= MaxCustomProviders {
			return fieldErr("", "too_many")
		}
		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_providers WHERE name = ? COLLATE NOCASE)`, name).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return fieldErr("name", "name_taken")
		}
		var by any
		if userID != "" {
			by = userID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO custom_providers(id, name, base_url, auth_value, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, name, base, auth, by, s.now().UnixMilli()); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: "custom_provider.add",
			Target: "server", Detail: map[string]any{"name": name, "host": hostOf(base), "auth": auth != ""}})
	})
	if err != nil {
		return "", err
	}
	s.reloadCustom(ctx)
	return id, nil
}

// RemoveCustomProvider deletes a site. A saved "Also search" choice that names it simply stops matching.
func (s *Service) RemoveCustomProvider(ctx context.Context, id, userID string) error {
	err := s.repo.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		var name, base string
		err := tx.QueryRowContext(ctx, `SELECT name, base_url FROM custom_providers WHERE id = ?`, id).Scan(&name, &base)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProviderNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM custom_providers WHERE id = ?`, id); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: "custom_provider.remove",
			Target: "server", Detail: map[string]any{"name": name, "host": hostOf(base)}})
	})
	if err != nil {
		return err
	}
	s.reloadCustom(ctx)
	return nil
}

// providerName returns a site's name, or ErrProviderNotFound.
func (s *Service) providerName(ctx context.Context, id string) (string, error) {
	var name string
	err := s.repo.Store().Reader().QueryRowContext(ctx, `SELECT name FROM custom_providers WHERE id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrProviderNotFound
	}
	return name, err
}

// reloadCustom hands the table to the online client. It runs at start (SetOnline) and after each
// change; a failure is logged and leaves the client's list as it was.
func (s *Service) reloadCustom(ctx context.Context) {
	if s.online == nil {
		return
	}
	rows, err := s.repo.Store().Reader().QueryContext(ctx, `SELECT id, name, base_url, auth_value FROM custom_providers ORDER BY created_at, id`)
	if err != nil {
		s.log.Warn("load lookup sites", "err", err)
		return
	}
	defer rows.Close()
	var list []online.CustomProvider
	for rows.Next() {
		var p online.CustomProvider
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.Auth); err != nil {
			s.log.Warn("read lookup site", "err", err)
			return
		}
		list = append(list, p)
	}
	if rows.Err() != nil {
		s.log.Warn("load lookup sites", "err", rows.Err())
		return
	}
	s.online.SetCustom(list)
}

// LookupPlaces lists the optional places a person may tick: Open Library, Apple Books, then the added
// sites. Without an online client it lists the two fixed ones.
func (s *Service) LookupPlaces() []online.Place {
	if s == nil || s.online == nil {
		return (&online.Client{}).Places()
	}
	return s.online.Places()
}
