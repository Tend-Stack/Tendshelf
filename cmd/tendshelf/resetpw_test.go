package main

import (
	"bytes"
	"context"
	"database/sql"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var resetCheap = users.HashParams{MemoryKiB: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func TestResetPasswordCommand(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := db.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	ur := users.New(st, users.Options{Hash: resetCheap})
	ss := auth.NewSessions(st, nil)
	svc := auth.NewService(st, ur, ss, auth.ServiceOptions{SetupCode: "ABCDE-FGHJK"})
	ip := netip.MustParseAddr("203.0.113.7")
	const oldPW = "correct horse battery"
	l, err := svc.Setup(ctx, "ABCDE-FGHJK", users.NewUser{Username: "ana", DisplayName: "Ana", Password: oldPW}, ip, "ua")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "ana", oldPW, ip, "ua", nil); err != nil {
		t.Fatal(err)
	}
	st.Close() // the command opens the file itself

	var out bytes.Buffer
	if err := runResetPassword(ctx, []string{"-data", dir, "ANA"}, &out); err != nil {
		t.Fatalf("reset: %v", err)
	}
	re := regexp.MustCompile(`^New password for ana: ([0-9A-Z]{4}(?:-[0-9A-Z]{4}){3})\nSign in with it, then change it on your Profile\.\n$`)
	m := re.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("output %q", out.String())
	}
	newPW := m[1]

	st, err = db.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ur = users.New(st, users.Options{Hash: resetCheap})
	svc = auth.NewService(st, ur, auth.NewSessions(st, nil), auth.ServiceOptions{})
	if _, err := svc.Login(ctx, "ana", oldPW, ip, "ua", nil); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err := svc.Login(ctx, "ana", newPW, ip, "ua", nil); err != nil {
		t.Fatalf("the new password is refused: %v", err)
	}
	// Every session from before is gone (only the one just made remains).
	var n int
	if err := st.Reader().QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, l.User.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("sessions = %d, %v", n, err)
	}
	// The audit row exists and the password is nowhere in the log.
	var all string
	if err := st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&all); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if !strings.Contains(all, "auth.password_reset") || strings.Contains(all, newPW) {
		t.Fatalf("audit log: %q", all)
	}

	// An unknown account, no name, two names and a missing database are plain errors.
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"unknown":    {[]string{"-data", dir, "nobody"}, `no account named "nobody"`},
		"no args":    {[]string{"-data", dir}, "usage: tendshelf reset-password"},
		"two names":  {[]string{"-data", dir, "a", "b"}, "usage: tendshelf reset-password"},
		"no db":      {[]string{"-data", t.TempDir(), "ana"}, "no Tendshelf database"},
		"name first": {[]string{"nobody", "-data", dir}, `no account named "nobody"`},
	} {
		var o bytes.Buffer
		err := runResetPassword(ctx, tc.args, &o)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
		if o.Len() != 0 {
			t.Errorf("%s: printed %q", name, o.String())
		}
	}
	// `run` dispatches the subcommand.
	if err := run(ctx, []string{"reset-password"}); err == nil || !strings.Contains(err.Error(), "usage: tendshelf reset-password") {
		t.Errorf("run reset-password without a name = %v", err)
	}
}
