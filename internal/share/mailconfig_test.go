package share

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

func validInput() MailInput {
	return MailInput{Host: "email-smtp.eu-west-1.amazonaws.com", Port: 587, TLS: TLSStartTLS, Username: "AKIA", FromName: "Home", FromAddress: "noreply@example.org"}
}

func saveMail(t *testing.T, f *fixture, in MailInput, pw string) (MailConfig, bool, error) {
	t.Helper()
	var cfg MailConfig
	var dropped bool
	err := f.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		cfg, dropped, err = SaveMailConfig(context.Background(), tx, tx, f.users["u_own"], in, pw, time.Now())
		return err
	})
	return cfg, dropped, err
}

func TestSaveMailConfigFieldErrors(t *testing.T) {
	long := strings.Repeat("a", 300)
	cases := []struct {
		name  string
		edit  func(*MailInput)
		pw    string
		field string
		code  string
	}{
		{"no host", func(i *MailInput) { i.Host = "" }, "", "host", "required"},
		{"host with scheme", func(i *MailInput) { i.Host = "https://mail.example.org" }, "", "host", "invalid"},
		{"host with port", func(i *MailInput) { i.Host = "mail.example.org:587" }, "", "host", "invalid"},
		{"host with space", func(i *MailInput) { i.Host = "mail example.org" }, "", "host", "invalid"},
		{"host too long", func(i *MailInput) { i.Host = long + ".org" }, "", "host", "invalid"},
		{"port zero", func(i *MailInput) { i.Port = 0 }, "", "port", "invalid"},
		{"port too big", func(i *MailInput) { i.Port = 70000 }, "", "port", "invalid"},
		{"unknown tls", func(i *MailInput) { i.TLS = "ssl" }, "", "tls", "invalid"},
		{"none to a remote host", func(i *MailInput) { i.TLS = TLSNone }, "", "tls", "insecure"},
		{"username with line break", func(i *MailInput) { i.Username = "a\nb" }, "", "username", "invalid"},
		{"username too long", func(i *MailInput) { i.Username = long }, "", "username", "invalid"},
		{"password with control", func(i *MailInput) {}, "a\x00b", "password", "invalid"},
		{"password too long", func(i *MailInput) {}, long, "password", "invalid"},
		{"from name with line break", func(i *MailInput) { i.FromName = "a\r\nBcc: x" }, "", "from_name", "invalid"},
		{"from name too long", func(i *MailInput) { i.FromName = strings.Repeat("n", 81) }, "", "from_name", "invalid"},
		{"no from address", func(i *MailInput) { i.FromAddress = "" }, "", "from_address", "required"},
		{"from address incomplete", func(i *MailInput) { i.FromAddress = "noreply@" }, "", "from_address", "invalid"},
		{"from address with a name", func(i *MailInput) { i.FromAddress = "Home <a@example.org>" }, "", "from_address", "invalid"},
		{"from address not ASCII", func(i *MailInput) { i.FromAddress = "ñ@example.org" }, "", "from_address", "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := openFixture(t)
			in := validInput()
			c.edit(&in)
			_, _, err := saveMail(t, f, in, c.pw)
			var fe users.FieldErrors
			if !errors.As(err, &fe) || fe[c.field] != c.code {
				t.Fatalf("err = %v, want %s=%s", err, c.field, c.code)
			}
			if got, _ := LoadMailConfig(context.Background(), f.st.Reader()); got.Host != "" {
				t.Error("a refused form changed the settings")
			}
		})
	}
}

func TestSaveMailConfigAcceptsLoopbackWithoutTLS(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		f := openFixture(t)
		in := validInput()
		in.Host, in.TLS, in.Port = host, TLSNone, 25
		if _, _, err := saveMail(t, f, in, ""); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
}

func TestSaveMailConfigPassword(t *testing.T) {
	f := openFixture(t)
	ctx := context.Background()
	if _, dropped, err := saveMail(t, f, validInput(), "pw-one"); err != nil || dropped {
		t.Fatalf("first save: %v dropped %v", err, dropped)
	}
	// Same server and user, no new password: kept.
	cfg, dropped, err := saveMail(t, f, validInput(), "")
	if err != nil || dropped || cfg.Password != "pw-one" {
		t.Fatalf("resave: %v dropped %v pw %q", err, dropped, cfg.Password)
	}
	// A changed host without a new password drops it.
	in := validInput()
	in.Host = "smtp.other.example"
	cfg, dropped, err = saveMail(t, f, in, "")
	if err != nil || !dropped || cfg.Password != "" {
		t.Fatalf("host change: %v dropped %v pw %q", err, dropped, cfg.Password)
	}
	if got, _ := LoadMailConfig(ctx, f.st.Reader()); got.Password != "" {
		t.Error("the stored password survived a host change")
	}
	// A new password with a new host is kept.
	if cfg, _, err = saveMail(t, f, in, "pw-two"); err != nil || cfg.Password != "pw-two" {
		t.Fatalf("new pw: %v %q", err, cfg.Password)
	}
	// A changed username drops it too.
	in.Username = "someone-else"
	if cfg, dropped, err = saveMail(t, f, in, ""); err != nil || !dropped || cfg.Password != "" {
		t.Fatalf("username change: %v dropped %v", err, dropped)
	}
	var detail string
	if err := f.st.Reader().QueryRow(`SELECT group_concat(detail_json) FROM audit_log WHERE action = 'smtp.update'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail, "pw-") || !strings.Contains(detail, `"password_dropped":true`) {
		t.Errorf("audit detail = %s", detail)
	}
}

func TestClearMailConfig(t *testing.T) {
	f := openFixture(t)
	if _, _, err := saveMail(t, f, validInput(), "pw"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		return ClearMailConfig(context.Background(), tx, f.users["u_own"], time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.st.Reader().QueryRow(`SELECT count(*) FROM settings WHERE key LIKE 'smtp.%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("smtp settings left = %d, %v", n, err)
	}
	cfg, _ := LoadMailConfig(context.Background(), f.st.Reader())
	if cfg.Ready() || cfg.Port != 587 || cfg.TLS != TLSStartTLS {
		t.Fatalf("defaults after clear = %+v", cfg)
	}
}

func TestMailConfigLogValueHidesPassword(t *testing.T) {
	c := MailConfig{Host: "h", Port: 1, TLS: "tls", Username: "u", Password: "topsecret"}
	if got := c.LogValue().String(); strings.Contains(got, "topsecret") {
		t.Fatal(got)
	}
}

func TestMailPresets(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range MailPresets {
		if seen[p.ID] || p.ID == "" || p.Name == "" || p.Port < 1 || p.Hint == "" {
			t.Errorf("bad preset %+v", p)
		}
		seen[p.ID] = true
		if p.TLS != TLSStartTLS && p.TLS != TLSImplicit {
			t.Errorf("%s: tls %q", p.ID, p.TLS)
		}
		if p.ID == "own" {
			continue
		}
		if p.Host == "" {
			t.Errorf("%s: no host", p.ID)
		}
		host := p.Filled("eu-west-1")
		if !validHost(host) {
			t.Errorf("%s: filled host %q is not valid", p.ID, host)
		}
	}
	for _, id := range []string{"ses", "gmail", "outlook", "fastmail", "icloud", "zoho", "mailgun", "sendgrid", "postmark", "brevo", "own"} {
		if !seen[id] {
			t.Errorf("preset %s is missing", id)
		}
	}
	ses, _ := MailPresetByID("ses")
	if ses.Filled("eu-west-1") != "email-smtp.eu-west-1.amazonaws.com" || ses.Filled("evil.example/") != "" {
		t.Errorf("ses host = %q / %q", ses.Filled("eu-west-1"), ses.Filled("evil.example/"))
	}
	if p, _ := MailPresetByID("sendgrid"); p.Username != "apikey" {
		t.Error("sendgrid username")
	}
}
