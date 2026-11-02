package share

import (
	"context"
	"log/slog"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The email relay's settings. The relay is optional: without it people send an invite from their own
// email app. These seven keys are the whole configuration; the password is the only secret and never
// leaves the settings table (not rendered, logged, audited, or put in a job).
const (
	settingSMTPHost     = "smtp.host"
	settingSMTPPort     = "smtp.port"
	settingSMTPTLS      = "smtp.tls"
	settingSMTPUsername = "smtp.username"
	settingSMTPPassword = "smtp.password"
	settingSMTPFromName = "smtp.from_name"
	settingSMTPFromAddr = "smtp.from_address"
)

var mailSettingKeys = []string{settingSMTPHost, settingSMTPPort, settingSMTPTLS, settingSMTPUsername,
	settingSMTPPassword, settingSMTPFromName, settingSMTPFromAddr}

// The three ways to secure the connection.
const (
	TLSStartTLS = "starttls" // plain connect, then upgrade; refuse a server that cannot
	TLSImplicit = "tls"      // encrypted from the first byte
	TLSNone     = "none"     // only for a server on this computer
)

// MailConfig is the relay's settings.
type MailConfig struct {
	Host                  string
	Port                  int
	TLS                   string
	Username, Password    string
	FromName, FromAddress string
}

// LogValue keeps the password out of any log line that prints a config.
func (c MailConfig) LogValue() slog.Value {
	return slog.GroupValue(slog.String("host", c.Host), slog.Int("port", c.Port), slog.String("tls", c.TLS),
		slog.Bool("username_set", c.Username != ""), slog.Bool("password_set", c.Password != ""))
}

// Ready reports whether the relay can send: a server and a sender address.
func (c MailConfig) Ready() bool { return c.Host != "" && c.FromAddress != "" }

// From is the sender as an address with an optional display name.
func (c MailConfig) From() mail.Address {
	return mail.Address{Name: c.FromName, Address: c.FromAddress}
}

// IsLoopback reports whether host is localhost or a loopback IP.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoadMailConfig reads the settings; a missing key takes its default (port 587, STARTTLS).
func LoadMailConfig(ctx context.Context, q db.RowQueryer) (MailConfig, error) {
	c := MailConfig{Port: 587, TLS: TLSStartTLS}
	for _, f := range []struct {
		key string
		dst any
	}{
		{settingSMTPHost, &c.Host}, {settingSMTPPort, &c.Port}, {settingSMTPTLS, &c.TLS}, {settingSMTPUsername, &c.Username},
		{settingSMTPPassword, &c.Password}, {settingSMTPFromName, &c.FromName}, {settingSMTPFromAddr, &c.FromAddress},
	} {
		if _, err := db.GetSetting(ctx, q, f.key, f.dst); err != nil {
			return MailConfig{}, err
		}
	}
	return c, nil
}

var hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, l := range strings.Split(h, ".") {
		if !hostLabel.MatchString(l) {
			return false
		}
	}
	return true
}

func hasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

func validSender(a string) bool {
	if a == "" || len(a) > 254 {
		return false
	}
	p, err := mail.ParseAddress(a)
	if err != nil || p.Address != a {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i] >= utf8.RuneSelf || a[i] <= ' ' {
			return false
		}
	}
	return true
}

// ValidMailAddress reports whether a is a plain ASCII address that round-trips ParseAddress.
func ValidMailAddress(a string) bool { return validSender(a) }

// MailInput is what the settings form sends. Password is empty when the person left it alone.
type MailInput struct {
	Host               string
	Port               int
	TLS                string
	Username, FromName string
	FromAddress        string
}

// SaveMailConfig validates in and saves it inside tx. A host or username change without a newly typed
// password drops the stored password, so a saved secret never goes to a new server; passwordDropped
// says so. Problems come back as users.FieldErrors with the codes required, invalid and insecure.
func SaveMailConfig(ctx context.Context, tx db.Execer, q db.RowQueryer, by users.User, in MailInput, newPassword string, now time.Time) (cfg MailConfig, passwordDropped bool, err error) {
	old, err := LoadMailConfig(ctx, q)
	if err != nil {
		return MailConfig{}, false, err
	}
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.FromName = strings.TrimSpace(in.FromName)
	in.FromAddress = strings.TrimSpace(in.FromAddress)
	errs := users.FieldErrors{}
	switch {
	case in.Host == "":
		errs["host"] = "required"
	case !validHost(in.Host):
		errs["host"] = "invalid"
	}
	if in.Port < 1 || in.Port > 65535 {
		errs["port"] = "invalid"
	}
	switch in.TLS {
	case TLSStartTLS, TLSImplicit:
	case TLSNone:
		if in.Host != "" && !IsLoopback(in.Host) {
			errs["tls"] = "insecure"
		}
	default:
		errs["tls"] = "invalid"
	}
	if utf8.RuneCountInString(in.Username) > 256 || hasControl(in.Username) {
		errs["username"] = "invalid"
	}
	if utf8.RuneCountInString(newPassword) > 256 || hasControl(newPassword) {
		errs["password"] = "invalid"
	}
	if utf8.RuneCountInString(in.FromName) > 80 || hasControl(in.FromName) {
		errs["from_name"] = "invalid"
	}
	switch {
	case in.FromAddress == "":
		errs["from_address"] = "required"
	case !validSender(in.FromAddress):
		errs["from_address"] = "invalid"
	}
	if len(errs) > 0 {
		return MailConfig{}, false, errs
	}
	cfg = MailConfig{Host: in.Host, Port: in.Port, TLS: in.TLS, Username: in.Username, Password: newPassword,
		FromName: in.FromName, FromAddress: in.FromAddress}
	changed := newPassword != ""
	if newPassword == "" {
		if !strings.EqualFold(old.Host, in.Host) || old.Username != in.Username {
			passwordDropped = old.Password != ""
		} else {
			cfg.Password = old.Password
		}
	}
	for _, f := range []struct {
		key string
		v   any
	}{
		{settingSMTPHost, cfg.Host}, {settingSMTPPort, cfg.Port}, {settingSMTPTLS, cfg.TLS}, {settingSMTPUsername, cfg.Username},
		{settingSMTPPassword, cfg.Password}, {settingSMTPFromName, cfg.FromName}, {settingSMTPFromAddr, cfg.FromAddress},
	} {
		if err := db.PutSetting(ctx, tx, f.key, f.v); err != nil {
			return MailConfig{}, false, err
		}
	}
	err = db.Audit(ctx, tx, db.AuditEntry{At: now.UnixMilli(), UserID: by.ID, Action: "smtp.update", Target: "server",
		Detail: map[string]any{"host": cfg.Host, "port": cfg.Port, "tls": cfg.TLS, "username_set": cfg.Username != "",
			"password_changed": changed, "password_dropped": passwordDropped}})
	return cfg, passwordDropped, err
}

// ClearMailConfig turns the relay off by deleting its settings.
func ClearMailConfig(ctx context.Context, tx db.Execer, by users.User, now time.Time) error {
	for _, k := range mailSettingKeys {
		if err := db.DeleteSetting(ctx, tx, k); err != nil {
			return err
		}
	}
	return db.Audit(ctx, tx, db.AuditEntry{At: now.UnixMilli(), UserID: by.ID, Action: "smtp.off", Target: "server"})
}
