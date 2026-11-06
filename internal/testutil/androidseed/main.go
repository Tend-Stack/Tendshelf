// Command androidseed fills a fresh data folder for the Android emulator checks (android/tools/test-server.sh):
// an owner "tester", a member "member" and one library over the folder given. It writes the database
// directly, before the server starts, so no setup code is needed. Test accounts only; it refuses a data
// folder that already holds a database.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

func main() {
	data := flag.String("data", "", "empty data folder")
	books := flag.String("books", "", "folder of synthetic books")
	pass := flag.String("password", "", "password of both test accounts")
	flag.Parse()
	if *data == "" || *books == "" || *pass == "" {
		fmt.Fprintln(os.Stderr, "usage: androidseed -data DIR -books DIR -password PW")
		os.Exit(2)
	}
	if err := run(context.Background(), *data, *books, *pass); err != nil {
		fmt.Fprintln(os.Stderr, "androidseed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, data, books, pass string) error {
	if _, err := os.Stat(db.FilePath(data)); err == nil {
		return fmt.Errorf("%s already holds a database", data)
	}
	st, err := db.Open(ctx, data)
	if err != nil {
		return err
	}
	defer st.Close()
	ur := users.New(st, users.Options{})
	hash, err := ur.HashPassword(ctx, pass)
	if err != nil {
		return err
	}
	err = st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := ur.CreateOwner(ctx, tx, users.NewUser{Username: "tester", DisplayName: "Test Owner", Password: pass}, hash); err != nil {
			return err
		}
		m, err := ur.CreateInvited(ctx, tx, users.NewUser{DisplayName: "Test Member", Role: users.RoleMember}, hash)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET username = 'member' WHERE id = ?`, m.ID)
		return err
	})
	if err != nil {
		return err
	}
	lib := library.New(st)
	_, _, err = lib.CreateLibraryWithFolder(ctx, "Android tests", books)
	return err
}
