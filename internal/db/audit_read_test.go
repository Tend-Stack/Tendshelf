package db

import (
	"context"
	"testing"
)

func TestListAudit(t *testing.T) {
	ctx := context.Background()
	st := mustOpen(t, t.TempDir())
	t.Cleanup(func() { _ = st.Close() })
	for i, e := range []AuditEntry{
		{At: 1, UserID: "a", Action: "auth.login"},
		{At: 2, UserID: "b", Action: "invite.create", Target: "invite:x", Detail: map[string]any{"role": "member"}},
		{At: 3, UserID: "a", Action: "authXlogin"},
		{At: 4, Action: "auth.login_failed"},
	} {
		if err := Audit(ctx, st.Writer(), e); err != nil {
			t.Fatal(i, err)
		}
	}
	got := func(f AuditFilter) (acts string, more bool) {
		rows, more, err := ListAudit(ctx, st.Reader(), f)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			acts += r.Action + ","
		}
		return acts, more
	}
	if a, more := got(AuditFilter{Limit: 10}); a != "auth.login_failed,authXlogin,invite.create,auth.login," || more {
		t.Errorf("all = %q %v", a, more)
	}
	if a, _ := got(AuditFilter{UserID: "a", Limit: 10}); a != "authXlogin,auth.login," {
		t.Errorf("user a = %q", a)
	}
	if a, _ := got(AuditFilter{Prefixes: []string{"auth."}, Limit: 10}); a != "auth.login_failed,auth.login," {
		t.Errorf("prefix = %q (a dot must not be a wildcard)", a)
	}
	if a, more := got(AuditFilter{Limit: 2, Offset: 1}); a != "authXlogin,invite.create," || !more {
		t.Errorf("page = %q %v", a, more)
	}
	rows, _, _ := ListAudit(ctx, st.Reader(), AuditFilter{UserID: "b", Limit: 5})
	if len(rows) != 1 || rows[0].Detail["role"] != "member" {
		t.Errorf("detail = %+v", rows)
	}
}
