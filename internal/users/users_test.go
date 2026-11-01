package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

var cheap = HashParams{MemoryKiB: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func newRepo(t *testing.T, o Options) *Repo {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if o.Hash == (HashParams{}) {
		o.Hash = cheap
	}
	return New(st, o)
}

func goodUser(name string) NewUser {
	return NewUser{Username: name, DisplayName: "Ana", Email: "ana@example.org", Password: "correct horse battery"}
}

func createOwner(t *testing.T, r *Repo, n NewUser) (User, error) {
	t.Helper()
	hash, err := r.HashPassword(context.Background(), n.Password)
	if err != nil {
		t.Fatal(err)
	}
	var u User
	err = r.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var e error
		u, e = r.CreateOwner(context.Background(), tx, n, hash)
		return e
	})
	return u, err
}

func TestDefaultHashIsOWASP(t *testing.T) {
	if DefaultHash != (HashParams{MemoryKiB: 19456, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}) {
		t.Fatalf("DefaultHash = %+v", DefaultHash)
	}
	r := newRepo(t, Options{Hash: DefaultHash})
	h, err := r.HashPassword(context.Background(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("prefix: %s", h)
	}
	if ok, err := r.VerifyPassword(context.Background(), h, "correct horse battery"); err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	// Zero options mean the default.
	if New(r.st, Options{}).opts.Hash != DefaultHash {
		t.Fatal("zero options do not select DefaultHash")
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	h1, _ := r.HashPassword(ctx, "pässword with ünicode")
	h2, _ := r.HashPassword(ctx, "pässword with ünicode")
	if h1 == h2 {
		t.Fatal("two hashes of one password are identical: salt is not random")
	}
	for _, h := range []string{h1, h2} {
		ok, err := r.VerifyPassword(ctx, h, "pässword with ünicode")
		if err != nil || !ok {
			t.Fatalf("verify %s: %v %v", h, ok, err)
		}
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	r := newRepo(t, Options{})
	h, _ := r.HashPassword(context.Background(), "correct horse battery")
	for _, pw := range []string{"", "correct horse batterY", "correct horse battery ", "x"} {
		ok, err := r.VerifyPassword(context.Background(), h, pw)
		if err != nil || ok {
			t.Fatalf("pw %q: ok=%v err=%v", pw, ok, err)
		}
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	r := newRepo(t, Options{})
	good, _ := r.HashPassword(context.Background(), "x")
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":         "",
		"plain":         "hunter2",
		"bcrypt":        "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
		"argon2i":       strings.Replace(good, "argon2id", "argon2i", 1),
		"old version":   strings.Replace(good, "v=19", "v=16", 1),
		"too few":       "$argon2id$v=19$m=64,t=1,p=1$" + parts[4],
		"bad params":    "$argon2id$v=19$m=x,t=1,p=1$" + parts[4] + "$" + parts[5],
		"zero time":     "$argon2id$v=19$m=64,t=0,p=1$" + parts[4] + "$" + parts[5],
		"huge memory":   "$argon2id$v=19$m=4294967295,t=1,p=1$" + parts[4] + "$" + parts[5],
		"bad salt b64":  "$argon2id$v=19$m=64,t=1,p=1$!!!$" + parts[5],
		"short key":     "$argon2id$v=19$m=64,t=1,p=1$" + parts[4] + "$AAAA",
		"bad key b64":   "$argon2id$v=19$m=64,t=1,p=1$" + parts[4] + "$!!!",
		"leading space": " " + good,
	}
	for name, h := range cases {
		ok, err := r.VerifyPassword(context.Background(), h, "x")
		if ok || !errors.Is(err, ErrBadHash) {
			t.Errorf("%s: ok=%v err=%v, want ErrBadHash", name, ok, err)
		}
	}
}

func TestVerifyUsesEncodedParams(t *testing.T) {
	a := newRepo(t, Options{Hash: cheap})
	h, _ := a.HashPassword(context.Background(), "correct horse battery")
	other := HashParams{MemoryKiB: 128, Time: 2, Threads: 2, SaltLen: 16, KeyLen: 32}
	b := New(a.st, Options{Hash: other})
	if ok, err := b.VerifyPassword(context.Background(), h, "correct horse battery"); err != nil || !ok {
		t.Fatalf("a repo with other params must verify an older hash: %v %v", ok, err)
	}
	h2, _ := b.HashPassword(context.Background(), "x")
	if !strings.Contains(h2, "m=128,t=2,p=2") {
		t.Fatalf("new hashes use current params: %s", h2)
	}
}

func TestValidateNewUser(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	ok := goodUser("ana")
	cases := []struct {
		name  string
		mod   func(*NewUser)
		field string
		code  string // "" means the user is valid
	}{
		{"valid", func(*NewUser) {}, "", ""},
		{"valid without email", func(n *NewUser) { n.Email = "" }, "", ""},
		{"name required", func(n *NewUser) { n.DisplayName = "  " }, "display_name", "required"},
		{"name too long", func(n *NewUser) { n.DisplayName = strings.Repeat("é", 81) }, "display_name", "too_long"},
		{"name 80 ok", func(n *NewUser) { n.DisplayName = strings.Repeat("é", 80) }, "", ""},
		{"name control", func(n *NewUser) { n.DisplayName = "A\x00na" }, "display_name", "invalid"},
		{"name newline", func(n *NewUser) { n.DisplayName = "A\nna" }, "display_name", "invalid"},
		{"username short", func(n *NewUser) { n.Username = "an" }, "username", "invalid"},
		{"username 3 ok", func(n *NewUser) { n.Username = "ana" }, "", ""},
		{"username long", func(n *NewUser) { n.Username = long(33) }, "username", "invalid"},
		{"username 32 ok", func(n *NewUser) { n.Username = long(32) }, "", ""},
		{"username starts dot", func(n *NewUser) { n.Username = ".ana" }, "username", "invalid"},
		{"username space", func(n *NewUser) { n.Username = "a na" }, "username", "invalid"},
		{"username unicode", func(n *NewUser) { n.Username = "anä" }, "username", "invalid"},
		{"username upper after Normalize", func(n *NewUser) { n.Username = " ANA.k " }, "", ""},
		{"email no domain", func(n *NewUser) { n.Email = "ana@" }, "email", "invalid"},
		{"email display form", func(n *NewUser) { n.Email = "Ana <ana@example.org>" }, "email", "invalid"},
		{"email too long", func(n *NewUser) { n.Email = long(250) + "@e.org" }, "email", "invalid"},
		{"password short", func(n *NewUser) { n.Password = "123456789" }, "password", "too_short"},
		{"password 10 ok", func(n *NewUser) { n.Password = "1234567890" }, "", ""},
		{"password 256 ok", func(n *NewUser) { n.Password = strings.Repeat("é", 256) }, "", ""},
		{"password long", func(n *NewUser) { n.Password = strings.Repeat("é", 257) }, "password", "too_long"},
		{"password is username", func(n *NewUser) { n.Username = "anaanaanaa"; n.Password = "AnaAnaAnaA" }, "password", "same_as_username"},
	}
	for _, c := range cases {
		n := ok
		c.mod(&n)
		errs := Validate(Normalize(n))
		if c.code == "" {
			if errs != nil {
				t.Errorf("%s: unexpected %v", c.name, errs)
			}
			continue
		}
		if errs[c.field] != c.code {
			t.Errorf("%s: %v, want %s=%s", c.name, errs, c.field, c.code)
		}
	}
	if n := Normalize(NewUser{Username: " ANA ", Password: "  keep  "}); n.Username != "ana" || n.Password != "  keep  " {
		t.Fatalf("Normalize: %+v", n)
	}
	var fe error = FieldErrors{"b": "x", "a": "y"}
	if !strings.Contains(fe.Error(), "a: y, b: x") {
		t.Fatalf("Error(): %s", fe)
	}
}

func TestCreateOwnerOnce(t *testing.T) {
	r := newRepo(t, Options{Now: func() time.Time { return time.UnixMilli(1234) }})
	if ok, _ := r.OwnerExists(context.Background()); ok {
		t.Fatal("owner on an empty database")
	}
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleOwner || !strings.HasPrefix(u.ID, "usr_") || u.CreatedAt != 1234 || u.Email != "ana@example.org" {
		t.Fatalf("user: %+v", u)
	}
	if ok, _ := r.OwnerExists(context.Background()); !ok {
		t.Fatal("owner missing after create")
	}
	if _, err := createOwner(t, r, goodUser("ben")); !errors.Is(err, ErrOwnerExists) {
		t.Fatalf("second owner: %v", err)
	}
	got, hash, err := r.ByUsername(context.Background(), "ana")
	if err != nil || got != u || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("ByUsername: %+v %q %v", got, hash, err)
	}
	if byID, err := r.ByID(context.Background(), u.ID); err != nil || byID != u {
		t.Fatalf("ByID: %+v %v", byID, err)
	}
	if _, err := r.ByID(context.Background(), "usr_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	if err := r.TouchLastSeen(context.Background(), r.st.Writer(), u.ID, 99); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.ByID(context.Background(), u.ID); again.LastSeenAt != 99 {
		t.Fatalf("last seen %d", again.LastSeenAt)
	}
}

func TestCreateOwnerConcurrent(t *testing.T) {
	r := newRepo(t, Options{})
	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = func() error {
				hash, err := r.HashPassword(context.Background(), "correct horse battery")
				if err != nil {
					return err
				}
				return r.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
					nu := goodUser("owner" + string(rune('a'+i)) + "xx")
					_, err := r.CreateOwner(context.Background(), tx, nu, hash)
					return err
				})
			}()
		}(i)
	}
	wg.Wait()
	var okN, existsN int
	for _, err := range errs {
		switch {
		case err == nil:
			okN++
		case errors.Is(err, ErrOwnerExists):
			existsN++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if okN != 1 || existsN != n-1 {
		t.Fatalf("ok=%d exists=%d", okN, existsN)
	}
	var rows int
	_ = r.st.Reader().QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'owner'`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("owner rows = %d", rows)
	}
}

// The unique index alone must also hold when the pre-check is bypassed.
func TestOwnerIndexMapsToErrOwnerExists(t *testing.T) {
	r := newRepo(t, Options{})
	if _, err := createOwner(t, r, goodUser("ana")); err != nil {
		t.Fatal(err)
	}
	_, err := r.st.Writer().Exec(`INSERT INTO users (id, username, display_name, role, disabled, created_at) VALUES ('u2','ben','Ben','owner',0,1)`)
	if !db.IsUniqueViolation(err) {
		t.Fatalf("raw insert of a second owner: %v", err)
	}
}

func TestUsernameTaken(t *testing.T) {
	r := newRepo(t, Options{})
	// A non-owner already holds the name; the owner create must map the
	// username conflict to a field error, not to ErrOwnerExists.
	if _, err := r.st.Writer().Exec(`INSERT INTO users (id, username, display_name, role, disabled, created_at) VALUES ('u1','Ana','Ana','member',0,1)`); err != nil {
		t.Fatal(err)
	}
	_, err := createOwner(t, r, goodUser("ana"))
	var fe FieldErrors
	if !errors.As(err, &fe) || fe["username"] != "taken" {
		t.Fatalf("err = %v", err)
	}
}

func TestByUsernameCaseInsensitive(t *testing.T) {
	r := newRepo(t, Options{})
	u, err := createOwner(t, r, goodUser("ana.k"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ana.k", "ANA.K", "Ana.K"} {
		got, _, err := r.ByUsername(context.Background(), name)
		if err != nil || got.ID != u.ID {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
	}
	if _, _, err := r.ByUsername(context.Background(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestHashSemaphoreHonoursContext(t *testing.T) {
	r := newRepo(t, Options{MaxHashers: 1})
	r.sem <- struct{}{} // another hash holds the only slot
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.HashPassword(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HashPassword while full: %v", err)
	}
	good := "$argon2id$v=19$m=64,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := r.VerifyPassword(ctx, good, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("VerifyPassword while full: %v", err)
	}
	<-r.sem
	if _, err := r.HashPassword(context.Background(), "x"); err != nil {
		t.Fatalf("after release: %v", err)
	}
	// Never more than MaxHashers at once.
	r2 := newRepo(t, Options{MaxHashers: 2})
	if cap(r2.sem) != 2 {
		t.Fatalf("cap = %d", cap(r2.sem))
	}
	r2.VerifyDummy(context.Background(), "x") // must not deadlock or panic
}
