package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const resetUsage = "usage: tendshelf reset-password [-data DIR] <username>"

// runResetPassword is `tendshelf reset-password [-data DIR] <username>`: it gives the account a new
// generated password, signs it out everywhere, and prints the password once on stdout. Nothing
// else is printed or logged about it. It works while the server runs: the database is in WAL mode
// and the write waits for the server's own.
func runResetPassword(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	dataFlag := fs.String("data", envOr("TENDSHELF_DATA", ""), "data folder; default is per user")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), resetUsage) }
	var username string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		if username != "" {
			return errors.New(resetUsage)
		}
		username, args = args[0], args[1:] // flags may also follow the name
	}
	if strings.TrimSpace(username) == "" {
		return errors.New(resetUsage)
	}

	dataDir := *dataFlag
	if dataDir == "" {
		d, err := defaultDataDir()
		if err != nil {
			return err
		}
		dataDir = d
	}
	// Never create an empty database by mistake: the account has to be somewhere.
	if _, err := os.Stat(db.FilePath(dataDir)); err != nil {
		return fmt.Errorf("there is no Tendshelf database at %s. Check the -data folder", db.FilePath(dataDir))
	}
	st, err := db.Open(ctx, dataDir)
	if err != nil {
		return describeOpenError(err, dataDir)
	}
	defer st.Close()

	ur := users.New(st, users.Options{})
	svc := auth.NewService(st, ur, auth.NewSessions(st, nil), auth.ServiceOptions{})
	pw, err := svc.ResetPassword(ctx, username)
	if errors.Is(err, users.ErrNotFound) {
		return fmt.Errorf("no account named %q", strings.ToLower(strings.TrimSpace(username)))
	}
	if err != nil {
		return fmt.Errorf("reset the password: %w", err)
	}
	fmt.Fprintf(stdout, "New password for %s: %s\n", strings.ToLower(strings.TrimSpace(username)), pw)
	fmt.Fprintln(stdout, "Sign in with it, then change it on your Profile.")
	return nil
}
