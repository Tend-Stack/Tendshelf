package db

import (
	"context"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	var s string
	ok, err := GetSetting(ctx, st.Reader(), "web.k", &s)
	if err != nil || ok {
		t.Fatalf("absent key: ok=%v err=%v", ok, err)
	}
	if err := PutSetting(ctx, st.Writer(), "web.k", "one"); err != nil {
		t.Fatal(err)
	}
	if err := PutSetting(ctx, st.Writer(), "web.k", "two"); err != nil {
		t.Fatal(err)
	}
	ok, err = GetSetting(ctx, st.Reader(), "web.k", &s)
	if err != nil || !ok || s != "two" {
		t.Fatalf("got %q ok=%v err=%v, want two", s, ok, err)
	}
	var n struct{ A int }
	if err := PutSetting(ctx, st.Writer(), "web.n", map[string]int{"A": 7}); err != nil {
		t.Fatal(err)
	}
	if ok, err := GetSetting(ctx, st.Reader(), "web.n", &n); err != nil || !ok || n.A != 7 {
		t.Fatalf("struct: %+v ok=%v err=%v", n, ok, err)
	}
}

func TestAuditInsert(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	if err := Audit(ctx, st.Writer(), AuditEntry{At: 5, Action: "x.anon", Target: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := Audit(ctx, st.Writer(), AuditEntry{At: 6, Action: "x.detail", Detail: map[string]string{"ip": "1.2.3.4"}}); err != nil {
		t.Fatal(err)
	}
	var uidNull bool
	var detail string
	if err := st.Reader().QueryRowContext(ctx, `SELECT user_id IS NULL, detail_json FROM audit_log WHERE action = 'x.anon'`).Scan(&uidNull, &detail); err != nil {
		t.Fatal(err)
	}
	if !uidNull || detail != "{}" {
		t.Fatalf("null user %v detail %q", uidNull, detail)
	}
	if err := st.Reader().QueryRowContext(ctx, `SELECT detail_json FROM audit_log WHERE action = 'x.detail'`).Scan(&detail); err != nil || detail != `{"ip":"1.2.3.4"}` {
		t.Fatalf("detail %q err %v", detail, err)
	}
}

func TestDeleteSetting(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := PutSetting(ctx, st.Writer(), "web.d", "x"); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second delete finds nothing and is still fine
		if err := DeleteSetting(ctx, st.Writer(), "web.d"); err != nil {
			t.Fatal(err)
		}
	}
	var s string
	if ok, err := GetSetting(ctx, st.Reader(), "web.d", &s); err != nil || ok {
		t.Fatalf("after delete: ok=%v err=%v", ok, err)
	}
}
