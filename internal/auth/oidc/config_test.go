package oidc_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

func validInput() oidc.ConfigInput {
	return oidc.ConfigInput{Enabled: true, Name: "Authelia", Issuer: "https://auth.example.com", ClientID: "tendshelf",
		ServerAddress: "https://Music.Example.com:443", MatchEmail: true}
}

func save(t *testing.T, st *db.Store, in oidc.ConfigInput, secret string) (oidc.Config, error) {
	t.Helper()
	var cfg oidc.Config
	err := st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		cfg, err = oidc.SaveConfig(context.Background(), tx, tx, users.User{}, in, secret, time.Now())
		return err
	})
	return cfg, err
}

func openStore(t *testing.T) *db.Store {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSaveConfigFieldErrors(t *testing.T) {
	st := openStore(t)
	for name, tc := range map[string]struct {
		mut   func(*oidc.ConfigInput)
		field string
		code  string
	}{
		"name empty":       {func(in *oidc.ConfigInput) { in.Name = " " }, "name", "required"},
		"name long":        {func(in *oidc.ConfigInput) { in.Name = strings.Repeat("a", 41) }, "name", "too_long"},
		"issuer empty":     {func(in *oidc.ConfigInput) { in.Issuer = "" }, "issuer", "required"},
		"issuer http":      {func(in *oidc.ConfigInput) { in.Issuer = "http://auth.example.com" }, "issuer", "invalid"},
		"issuer userinfo":  {func(in *oidc.ConfigInput) { in.Issuer = "https://a@auth.example.com" }, "issuer", "invalid"},
		"issuer query":     {func(in *oidc.ConfigInput) { in.Issuer = "https://auth.example.com/?a=b" }, "issuer", "invalid"},
		"client id empty":  {func(in *oidc.ConfigInput) { in.ClientID = "" }, "client_id", "required"},
		"client id long":   {func(in *oidc.ConfigInput) { in.ClientID = strings.Repeat("a", 256) }, "client_id", "too_long"},
		"address empty":    {func(in *oidc.ConfigInput) { in.ServerAddress = "" }, "server_address", "required"},
		"address has path": {func(in *oidc.ConfigInput) { in.ServerAddress = "https://x.example.com/app" }, "server_address", "invalid"},
		"role admin":       {func(in *oidc.ConfigInput) { in.NewRole = users.RoleAdmin }, "new_role", "invalid"},
		"role contributor": {func(in *oidc.ConfigInput) { in.NewRole = "contributor" }, "new_role", "invalid"},
	} {
		in := validInput()
		tc.mut(&in)
		_, err := save(t, st, in, "")
		var fe users.FieldErrors
		if !errors.As(err, &fe) || fe[tc.field] != tc.code {
			t.Errorf("%s: err = %v, want %s=%s", name, err, tc.field, tc.code)
		}
	}
	if got, _ := oidc.LoadConfig(context.Background(), st.Reader()); got.Issuer != "" {
		t.Error("a refused save wrote settings")
	}
}

func TestSaveConfigRoundTripAndSecret(t *testing.T) {
	st := openStore(t)
	cfg, err := save(t, st, validInput(), "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerAddress != "https://music.example.com" || cfg.NewRole != users.RoleMember || cfg.ClientSecret != "s3cret" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.RedirectURL() != "https://music.example.com/login/oidc/callback" {
		t.Errorf("redirect = %s", cfg.RedirectURL())
	}
	// A blank secret keeps the saved one.
	in := validInput()
	in.CreateAccounts, in.NewRole = true, users.RoleGuest
	cfg, err = save(t, st, in, "")
	if err != nil || cfg.ClientSecret != "s3cret" {
		t.Fatalf("kept secret = %q, %v", cfg.ClientSecret, err)
	}
	got, err := oidc.LoadConfig(context.Background(), st.Reader())
	if err != nil || got != cfg || got.NewRole != users.RoleGuest || !got.CreateAccounts {
		t.Fatalf("loaded %+v, %v", got, err)
	}
	// A new client id without a new secret drops the secret, so it never goes to another client.
	in.ClientID = "other"
	if cfg, err = save(t, st, in, ""); err != nil || cfg.ClientSecret != "" {
		t.Fatalf("secret after client id change = %q, %v", cfg.ClientSecret, err)
	}
	// Neither the audit rows nor a log line carry the secret.
	if _, err := save(t, st, in, "another-secret"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE detail_json LIKE '%secret%' AND (detail_json LIKE '%s3cret%' OR detail_json LIKE '%another-secret%')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit rows with the secret = %d, %v", n, err)
	}
	if err := st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'oidc.update'`).Scan(&n); err != nil || n != 4 {
		t.Errorf("oidc.update rows = %d, %v", n, err)
	}
	if v := slog.AnyValue(cfg).Resolve().String(); strings.Contains(v, "another-secret") || !strings.Contains(v, "secret_set") {
		t.Errorf("log value = %s", v)
	}
}

func TestSaveConfigIssuerChangeDropsIdentities(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if _, err := save(t, st, validInput(), ""); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'ana', 'member', 1)`,
		`INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES ('https://auth.example.com', 's1', 'us_1', 'account', 1)`,
	} {
		if _, err := st.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	in := validInput()
	in.Enabled = false
	if _, err := save(t, st, in, ""); err != nil { // same issuer: identities stay
		t.Fatal(err)
	}
	var n int
	_ = st.Reader().QueryRow(`SELECT count(*) FROM oidc_identities`).Scan(&n)
	if n != 1 {
		t.Fatalf("identities after a save with the same issuer = %d", n)
	}
	in.Issuer = "https://auth.example.com/"
	if _, err := save(t, st, in, ""); err != nil {
		t.Fatal(err)
	}
	_ = st.Reader().QueryRow(`SELECT count(*) FROM oidc_identities`).Scan(&n)
	if n != 0 {
		t.Fatalf("identities after an issuer change = %d", n)
	}
	var detail string
	if err := st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'oidc.update' ORDER BY at DESC, rowid DESC LIMIT 1`).Scan(&detail); err != nil || !strings.Contains(detail, `"identities_removed":1`) {
		t.Errorf("audit detail = %s, %v", detail, err)
	}
}

func TestClearConfigKeepsTheRest(t *testing.T) {
	st := openStore(t)
	if _, err := save(t, st, validInput(), "s3cret"); err != nil {
		t.Fatal(err)
	}
	err := st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		return oidc.ClearConfig(context.Background(), tx, users.User{}, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := oidc.LoadConfig(context.Background(), st.Reader())
	if err != nil || cfg.Enabled || cfg.Issuer == "" || cfg.ClientSecret != "s3cret" || cfg.Ready() {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'oidc.off'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("oidc.off rows = %d, %v", n, err)
	}
}
