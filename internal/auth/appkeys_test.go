package auth

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/token"
)

// akeyIssue stores a new key of the given kind for the env's owner.
func (e *env) akeyIssue(t *testing.T, kind DeviceKind) (string, Device) {
	t.Helper()
	var secret string
	var dev Device
	err := e.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		secret, dev, err = e.dev.IssueKey(context.Background(), tx, e.owner.ID, "", kind)
		return err
	})
	if err != nil {
		t.Fatalf("issue key: %v", err)
	}
	return secret, dev
}

func TestAppKeyHashPerKind(t *testing.T) {
	k := "abcdefghijklmnopqrstuvwx"
	if KeyHash(KindOPDS, k) != sha256.Sum256([]byte(k)) {
		t.Error("opds hash is not the SHA-256 of the key")
	}
	m := md5.Sum([]byte(k))
	if KeyHash(KindKOReader, k) != sha256.Sum256([]byte(hex.EncodeToString(m[:]))) {
		t.Error("koreader hash is not the SHA-256 of the hex MD5")
	}
	if KeyHash(KindOPDS, k) == KeyHash(KindKOReader, k) {
		t.Error("the kinds hash alike")
	}
}

func TestAppKeyIssueStoresOnlyHash(t *testing.T) {
	e := newEnv(t).withOwner(t)
	secret, dev := e.akeyIssue(t, KindOPDS)
	if !token.IsTypable(secret) || dev.Name != "Book app" || dev.Kind != KindOPDS {
		t.Fatalf("key %q dev %+v", secret, dev)
	}
	h := KeyHash(KindOPDS, secret)
	if e.count(t, `SELECT COUNT(*) FROM devices WHERE token_hash = ?`, h[:]) != 1 {
		t.Error("the hash is not stored")
	}
	if e.count(t, `SELECT COUNT(*) FROM devices WHERE CAST(token_hash AS TEXT) = ? OR name = ?`, secret, secret) != 0 {
		t.Error("the key itself is stored")
	}
	err := e.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, _, err := e.dev.IssueKey(context.Background(), tx, e.owner.ID, "x", KindWeb)
		return err
	})
	if err == nil {
		t.Error("a web kind issued a key")
	}
}

func TestAppKeyKindsDoNotCross(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	secret, dev := e.akeyIssue(t, KindOPDS)
	got, u, err := e.dev.LookupKey(ctx, KindOPDS, KeyHash(KindOPDS, secret))
	if err != nil || got.ID != dev.ID || u.ID != e.owner.ID {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	if _, _, err := e.dev.LookupKey(ctx, KindKOReader, KeyHash(KindOPDS, secret)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("an opds key opened the koreader surface: %v", err)
	}
	if _, _, err := e.dev.LookupKey(ctx, KindWeb, KeyHash(KindOPDS, secret)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("web kind lookup: %v", err)
	}
	// The key as a bearer token never works: wrong shape, and Lookup excludes the kinds anyway.
	if _, _, err := e.dev.Lookup(ctx, secret); !errors.Is(err, ErrNoDevice) {
		t.Errorf("key as bearer: %v", err)
	}
	ko, _ := e.akeyIssue(t, KindKOReader)
	if _, _, err := e.dev.LookupKey(ctx, KindOPDS, KeyHash(KindKOReader, ko)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("koreader key opened opds: %v", err)
	}
}

func TestAppKeyLookupRefuses(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	revoked, rdev := e.akeyIssue(t, KindOPDS)
	if _, err := e.dev.Revoke(ctx, e.st.Writer(), e.owner.ID, rdev.ID, e.clk.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.dev.LookupKey(ctx, KindOPDS, KeyHash(KindOPDS, revoked)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("revoked: %v", err)
	}

	idle, idev := e.akeyIssue(t, KindOPDS)
	e.clk.Add(DeviceIdleTTL + time.Millisecond)
	if _, _, err := e.dev.LookupKey(ctx, KindOPDS, KeyHash(KindOPDS, idle)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("idle: %v", err)
	}
	if _, rev, hl := e.deviceRow(t, idev.ID); !rev.Valid || hl != 0 {
		t.Errorf("idle row not revoked: %v %d", rev, hl)
	}

	live, _ := e.akeyIssue(t, KindKOReader)
	if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, e.owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.dev.LookupKey(ctx, KindKOReader, KeyHash(KindKOReader, live)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("disabled: %v", err)
	}
	if _, _, err := e.dev.LookupKey(ctx, KindOPDS, [32]byte{}); !errors.Is(err, ErrNoDevice) {
		t.Errorf("unknown: %v", err)
	}
}

func TestAppKeyListsKeepKeysApart(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	e.issue(t, "Phone", KindMobile)
	e.akeyIssue(t, KindOPDS)
	e.akeyIssue(t, KindKOReader)
	apps, err := e.dev.ListForUser(ctx, e.owner.ID)
	if err != nil || len(apps) != 1 || apps[0].Kind != KindMobile {
		t.Errorf("ListForUser = %+v %v", apps, err)
	}
	all, _ := e.dev.ListAll(ctx)
	if len(all) != 1 {
		t.Errorf("ListAll = %d, want 1", len(all))
	}
	keys, err := e.dev.OwnKeys(ctx, e.owner.ID)
	if err != nil || len(keys) != 2 {
		t.Errorf("OwnKeys = %+v %v", keys, err)
	}
	if _, ok := ParseDeviceKind("opds"); ok {
		t.Error("ParseDeviceKind accepts opds")
	}
	if _, ok := ParseDeviceKind("koreader"); ok {
		t.Error("ParseDeviceKind accepts koreader")
	}
}
