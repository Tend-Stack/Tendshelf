package progress

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/dict"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// Word is a word a person saved while reading (G1): the word as it was selected, the sentence around it, and where
// it was, so the list can jump back. Edition and SID are empty when the place is no longer known.
type Word struct {
	ID, UserID, BookID, EditionID string
	Word, Folded, Quote, SID      string
	Off                           int
	CreatedAt                     int64
}

// MaxWordsPerPerson is how many words one person may keep. It is a variable so tests can lower it.
var MaxWordsPerPerson = 5000

const (
	maxWordRunes  = 64
	maxQuoteWords = 400 // runes of the sentence kept with a word
)

// collapse trims text and joins runs of white space; control characters become spaces.
func collapse(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}

// AddWord saves a word for w.UserID on w.BookID. A word the person already saved on the book (compared folded, so
// "Café" and "cafe" are one) is left as it was and created is false. More than MaxWordsPerPerson is ErrLimit; an
// unknown person or book is ErrNotFound. A place that does not belong to the book is dropped: the word keeps its quote.
func (s *Service) AddWord(ctx context.Context, w Word) (out Word, created bool, err error) {
	w.Word = collapse(w.Word)
	if !utf8.ValidString(w.Word) || utf8.RuneCountInString(w.Word) < 1 || utf8.RuneCountInString(w.Word) > maxWordRunes {
		return Word{}, false, &InvalidError{"word"}
	}
	if w.UserID == "" || w.BookID == "" {
		return Word{}, false, &InvalidError{"book_id"}
	}
	if w.Folded = dict.Fold(w.Word); w.Folded == "" {
		return Word{}, false, &InvalidError{"word"}
	}
	w.Quote = collapse(w.Quote)
	if rs := []rune(w.Quote); len(rs) > maxQuoteWords {
		w.Quote = strings.TrimSpace(string(rs[:maxQuoteWords]))
	}
	if _, _, _, ok := ebook.ParseSID(w.SID); !ok || len(w.SID) < 6 || len(w.SID) > 40 || w.Off < 0 {
		w.SID, w.Off, w.EditionID = "", 0, ""
	}
	w.ID, w.CreatedAt = db.NewID("wd"), s.nowMs()
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		created = false
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM word_list WHERE user_id = ?`, w.UserID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxWordsPerPerson {
			var have int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM word_list WHERE user_id = ? AND book_id = ? AND folded = ?`,
				w.UserID, w.BookID, w.Folded).Scan(&have); err != nil {
				return err
			}
			if have == 0 {
				return ErrLimit
			}
		}
		var edition, sid any
		if w.EditionID != "" {
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM editions e JOIN ebook_files f ON f.id = e.ebook_file_id WHERE e.id = ? AND f.book_id = ?`,
				w.EditionID, w.BookID).Scan(&one)
			if err == nil {
				edition = w.EditionID
			} else if err != sql.ErrNoRows {
				return err
			}
		}
		if edition == nil {
			w.EditionID, w.SID, w.Off = "", "", 0
		}
		if w.SID != "" {
			sid = w.SID
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO word_list(id, user_id, book_id, edition_id, word, folded, quote, sid, off, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id, book_id, folded) DO NOTHING`,
			w.ID, w.UserID, w.BookID, edition, w.Word, w.Folded, w.Quote, sid, w.Off, w.CreatedAt)
		if err != nil {
			return err
		}
		k, _ := res.RowsAffected()
		created = k > 0
		return nil
	})
	if db.IsForeignKeyViolation(err) {
		return Word{}, false, ErrNotFound
	}
	if err != nil {
		return Word{}, false, err
	}
	return w, created, nil
}

// DeleteWord removes one of the person's words. Another person's word is ErrNotFound, like an absent one.
func (s *Service) DeleteWord(ctx context.Context, userID, id string) error {
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM word_list WHERE id = ? AND user_id = ?`, id, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// CountWords is how many words the person keeps, on every book (including books they can no longer see).
func (s *Service) CountWords(ctx context.Context, userID string) (n int, err error) {
	err = s.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM word_list WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}
