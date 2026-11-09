// Package oidc is Tendshelf's OpenID Connect client for one configured sign-in provider such as
// Authelia, Authentik or Keycloak (decision 0037). It is the standard library only: discovery, the
// token exchange, JWKS and a compact-JWS check over crypto/rsa, crypto/ecdsa and crypto/ed25519.
// Its http.Client is the second narrow exception to "only internal/metadata/online makes one".
package oidc

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The provider's settings. One provider; the client secret is the only secret and, like the SMTP
// password, never leaves the settings table (not rendered, logged, audited or put in a job).
const (
	settingEnabled        = "oidc.enabled"
	settingName           = "oidc.name"
	settingIssuer         = "oidc.issuer"
	settingClientID       = "oidc.client_id"
	settingClientSecret   = "oidc.client_secret"
	settingServerAddress  = "oidc.server_address"
	settingMatchEmail     = "oidc.match_email"
	settingCreateAccounts = "oidc.create_accounts"
	settingNewRole        = "oidc.new_role"
)

// CallbackPath is where the provider sends the browser back to, under the server address.
const CallbackPath = "/login/oidc/callback"

// Config is the provider's settings.
type Config struct {
	Enabled        bool
	Name           string
	Issuer         string
	ClientID       string
	ClientSecret   string
	ServerAddress  string
	MatchEmail     bool
	CreateAccounts bool
	NewRole        users.Role // member or guest
}

// LogValue keeps the client secret out of any log line that prints a config.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("enabled", c.Enabled), slog.String("issuer", c.Issuer),
		slog.String("client_id", c.ClientID), slog.Bool("secret_set", c.ClientSecret != ""))
}

// RedirectURL is the redirect address the provider must allow.
func (c Config) RedirectURL() string { return c.ServerAddress + CallbackPath }

// Ready reports whether the provider is switched on and complete.
func (c Config) Ready() bool {
	return c.Enabled && c.Issuer != "" && c.ClientID != "" && c.ServerAddress != ""
}

// ConfigInput is what the settings form sends. The client secret travels separately.
type ConfigInput struct {
	Enabled        bool
	Name           string
	Issuer         string
	ClientID       string
	ServerAddress  string
	MatchEmail     bool
	CreateAccounts bool
	NewRole        users.Role
}

// LoadConfig reads the settings; a missing key takes its default (off, new people start as member).
func LoadConfig(ctx context.Context, q db.RowQueryer) (Config, error) {
	c := Config{NewRole: users.RoleMember}
	var role string
	for _, f := range []struct {
		key string
		dst any
	}{
		{settingEnabled, &c.Enabled}, {settingName, &c.Name}, {settingIssuer, &c.Issuer}, {settingClientID, &c.ClientID},
		{settingClientSecret, &c.ClientSecret}, {settingServerAddress, &c.ServerAddress}, {settingMatchEmail, &c.MatchEmail},
		{settingCreateAccounts, &c.CreateAccounts}, {settingNewRole, &role},
	} {
		if _, err := db.GetSetting(ctx, q, f.key, f.dst); err != nil {
			return Config{}, err
		}
	}
	if role == string(users.RoleGuest) {
		c.NewRole = users.RoleGuest
	}
	return c, nil
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// secureScheme reports whether u is https, or http to a loopback host (tests and same-computer setups).
func secureScheme(u *url.URL) bool {
	return u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname()))
}

// originOf is scheme://host[:port], lower-cased, with the scheme's default port dropped.
func originOf(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return strings.ToLower(u.Scheme) + "://" + host
}

// parseAbsolute parses raw as an absolute URL with a secure scheme, a host and no userinfo, query or fragment.
func parseAbsolute(raw string) (*url.URL, bool) {
	if raw == "" || len(raw) > 512 || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, false
	}
	if !secureScheme(u) {
		return nil, false
	}
	return u, true
}

// ParseIssuer checks an issuer address: absolute, https (or http on loopback), no query, fragment or
// userinfo. It is kept byte-exact, so a trailing slash matters.
func ParseIssuer(raw string) bool {
	_, ok := parseAbsolute(raw)
	return ok
}

// ParseServerAddress reduces raw to scheme://host[:port], lower-cased with the default port dropped.
// A path other than "" or "/" is refused.
func ParseServerAddress(raw string) (string, bool) {
	u, ok := parseAbsolute(raw)
	if !ok || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	return originOf(u), true
}

// SaveConfig validates in and saves it inside tx. A blank newSecret keeps the saved secret, except
// that an issuer or client id change without a new secret drops it, so a saved secret never goes to
// a different provider. Changing the issuer deletes every identity of the old one. Problems come back
// as users.FieldErrors with the codes required, invalid and too_long.
func SaveConfig(ctx context.Context, tx db.Execer, q db.RowQueryer, by users.User, in ConfigInput, newSecret string, now time.Time) (Config, error) {
	old, err := LoadConfig(ctx, q)
	if err != nil {
		return Config{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Issuer = strings.TrimSpace(in.Issuer)
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.ServerAddress = strings.TrimSpace(in.ServerAddress)
	errs := users.FieldErrors{}
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		errs["name"] = "required"
	case n > 40:
		errs["name"] = "too_long"
	case strings.IndexFunc(in.Name, unicode.IsControl) >= 0:
		errs["name"] = "invalid"
	}
	switch {
	case in.Issuer == "":
		errs["issuer"] = "required"
	case !ParseIssuer(in.Issuer):
		errs["issuer"] = "invalid"
	}
	switch n := utf8.RuneCountInString(in.ClientID); {
	case n == 0:
		errs["client_id"] = "required"
	case n > 255:
		errs["client_id"] = "too_long"
	case strings.IndexFunc(in.ClientID, unicode.IsControl) >= 0:
		errs["client_id"] = "invalid"
	}
	origin, ok := ParseServerAddress(in.ServerAddress)
	switch {
	case in.ServerAddress == "":
		errs["server_address"] = "required"
	case !ok:
		errs["server_address"] = "invalid"
	}
	if n := utf8.RuneCountInString(newSecret); n > 512 {
		errs["client_secret"] = "too_long"
	} else if strings.IndexFunc(newSecret, unicode.IsControl) >= 0 {
		errs["client_secret"] = "invalid"
	}
	switch in.NewRole {
	case "":
		in.NewRole = users.RoleMember
	case users.RoleMember, users.RoleGuest:
	default:
		errs["new_role"] = "invalid"
	}
	if len(errs) > 0 {
		return Config{}, errs
	}
	cfg := Config{Enabled: in.Enabled, Name: in.Name, Issuer: in.Issuer, ClientID: in.ClientID, ClientSecret: newSecret,
		ServerAddress: origin, MatchEmail: in.MatchEmail, CreateAccounts: in.CreateAccounts, NewRole: in.NewRole}
	secretDropped := false
	if newSecret == "" {
		if old.Issuer != in.Issuer || old.ClientID != in.ClientID {
			secretDropped = old.ClientSecret != ""
		} else {
			cfg.ClientSecret = old.ClientSecret
		}
	}
	removed := int64(0)
	if old.Issuer != "" && old.Issuer != cfg.Issuer {
		res, err := tx.ExecContext(ctx, `DELETE FROM oidc_identities WHERE issuer = ?`, old.Issuer)
		if err != nil {
			return Config{}, err
		}
		removed, _ = res.RowsAffected()
		if _, err := tx.ExecContext(ctx, `DELETE FROM oidc_logins WHERE issuer = ?`, old.Issuer); err != nil {
			return Config{}, err
		}
	}
	var changed []string
	for _, f := range []struct {
		key     string
		v       any
		differs bool
	}{
		{settingEnabled, cfg.Enabled, old.Enabled != cfg.Enabled},
		{settingName, cfg.Name, old.Name != cfg.Name},
		{settingIssuer, cfg.Issuer, old.Issuer != cfg.Issuer},
		{settingClientID, cfg.ClientID, old.ClientID != cfg.ClientID},
		{settingClientSecret, cfg.ClientSecret, old.ClientSecret != cfg.ClientSecret},
		{settingServerAddress, cfg.ServerAddress, old.ServerAddress != cfg.ServerAddress},
		{settingMatchEmail, cfg.MatchEmail, old.MatchEmail != cfg.MatchEmail},
		{settingCreateAccounts, cfg.CreateAccounts, old.CreateAccounts != cfg.CreateAccounts},
		{settingNewRole, string(cfg.NewRole), old.NewRole != cfg.NewRole},
	} {
		if err := db.PutSetting(ctx, tx, f.key, f.v); err != nil {
			return Config{}, err
		}
		if f.differs {
			changed = append(changed, f.key)
		}
	}
	err = db.Audit(ctx, tx, db.AuditEntry{At: now.UnixMilli(), UserID: by.ID, Action: "oidc.update", Target: "server",
		Detail: map[string]any{"changed": changed, "secret_set": cfg.ClientSecret != "", "secret_dropped": secretDropped,
			"identities_removed": removed}})
	return cfg, err
}

// ClearConfig turns sign-in with the provider off. It keeps the rest of the settings, so turning it
// on again does not mean typing them again.
func ClearConfig(ctx context.Context, tx db.Execer, by users.User, now time.Time) error {
	if err := db.PutSetting(ctx, tx, settingEnabled, false); err != nil {
		return err
	}
	return db.Audit(ctx, tx, db.AuditEntry{At: now.UnixMilli(), UserID: by.ID, Action: "oidc.off", Target: "server"})
}

// Identity is a provider account linked to a Tendshelf account.
type Identity struct {
	Issuer, Subject, Email string
	LinkedVia              string // account, email or new
	CreatedAt              int64  // Unix milliseconds UTC
	LastUsedAt             int64  // 0 when never used
}
