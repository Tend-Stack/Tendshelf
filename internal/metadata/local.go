package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Enqueuer is the part of jobs.Runner this package uses to queue work.
type Enqueuer interface {
	Enqueue(ctx context.Context, kind string, payload any, o jobs.EnqueueOptions) (string, error)
	ActiveKeys(ctx context.Context, kind, field string) (map[string]bool, error)
}

// Service reads books' local details (folder names and file tags) and keeps their rows current.
type Service struct {
	repo *library.Repo
	jobs Enqueuer
	log  *slog.Logger
	now  func() time.Time
}

// New returns a Service. log may be nil.
func New(repo *library.Repo, jobs Enqueuer, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, jobs: jobs, log: log, now: time.Now}
}

func compact(b []byte) string {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return string(b)
	}
	return buf.String()
}

// sortTitle drops a leading English article, so "The Tide" sorts under T.
func sortTitle(s string) string {
	l := strings.ToLower(s)
	for _, a := range []string{"the ", "an ", "a "} {
		if strings.HasPrefix(l, a) && len(strings.TrimSpace(s[len(a):])) > 0 {
			return strings.TrimSpace(s[len(a):])
		}
	}
	return s
}

// firstTags returns the tags of the first live file that has a probe answer, in play order.
func firstTags(files []library.AudioFile) map[string]string {
	for _, f := range files {
		if f.MissingSince != 0 || len(f.ProbeJSON) == 0 {
			continue
		}
		var p media.Probe
		if json.Unmarshal(f.ProbeJSON, &p) == nil {
			return p.Tags
		}
	}
	return nil
}

// Local reads a book's folder names and file tags, stores them as "folder" and "tags" values, and
// brings the book's columns, people, series and genres in line with the strongest value of each
// field. It reads only database rows and writes only when something differs, in one transaction
// that starts with guard (a job's fence). A book that is gone, or whose files are all missing, is
// left alone.
func (s *Service) Local(ctx context.Context, bookID string, guard func(context.Context, *sql.Tx) error) (bool, error) {
	var changed bool
	err := s.repo.UpdateGuarded(ctx, guard, func(tx *library.Tx) error {
		var err error
		changed, err = s.local(ctx, tx, bookID)
		return err
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

func (s *Service) local(ctx context.Context, tx *library.Tx, bookID string) (bool, error) {
	b, err := tx.Book(bookID)
	if errors.Is(err, library.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	files, err := tx.AudioFiles(bookID)
	if err != nil {
		return false, err
	}
	live := 0
	for _, f := range files {
		if f.MissingSince == 0 {
			live++
		}
	}
	if b.MissingSince != 0 || live == 0 {
		return false, nil
	}
	folder, err := s.repo.Folder(ctx, b.FolderID)
	if err != nil {
		return false, err
	}
	single := len(files) == 1 && files[0].Path == b.RootPath

	fromFolder := FromPath(folder.Path, b.RootPath, single)
	fromFolder.fillProduction()
	fromTags := FromTags(firstTags(files), single)
	fromTags.fillProduction()
	desired := map[string]map[string]json.RawMessage{SourceFolder: fromFolder.Values(), SourceTags: fromTags.Values()}

	stored, err := tx.MetadataValues(bookID)
	if err != nil {
		return false, err
	}
	changed := false
	now := s.now().UnixMilli()
	var rows []library.MetadataValue // what the table holds after this call
	for _, r := range stored {
		want, ours := desired[r.Source]
		if !ours {
			rows = append(rows, r)
			continue
		}
		v, ok := want[r.Field]
		if !ok {
			if err := tx.DeleteMetadataValue(bookID, r.Field, r.Source); err != nil {
				return false, err
			}
			changed = true
			continue
		}
		if compact(r.Value) != compact(v) {
			continue // replaced below
		}
		rows = append(rows, r)
	}
	have := map[[2]string]bool{}
	for _, r := range rows {
		have[[2]string{r.Field, r.Source}] = true
	}
	for _, src := range []string{SourceFolder, SourceTags} {
		for field, v := range desired[src] {
			if have[[2]string{field, src}] {
				continue
			}
			mv := library.MetadataValue{Field: field, Source: src, Value: v, ObservedAt: now}
			if err := tx.PutMetadataValue(bookID, mv); err != nil {
				return false, err
			}
			rows = append(rows, mv)
			changed = true
		}
	}

	d, _ := Resolve(rows)
	nb := b
	if d.Title != "" {
		nb.Title = d.Title
	}
	nb.SortTitle = sortTitle(nb.Title)
	nb.Subtitle, nb.Description, nb.Publisher, nb.Language = d.Subtitle, d.Description, d.Publisher, d.Language
	nb.ISBN, nb.ASIN, nb.PublishedYear = d.ISBN, d.ASIN, d.Year
	nb.Abridged, nb.Dramatized, nb.Explicit = isTrue(d.Abridged), isTrue(d.Dramatized), isTrue(d.Explicit)
	if nb != b {
		if err := tx.UpdateBook(nb); err != nil {
			return false, err
		}
		changed = true
	}

	for _, r := range []struct {
		role  string
		names []string
	}{{library.RoleAuthor, d.Authors}, {library.RoleNarrator, d.Narrators}} {
		c, err := syncPeople(tx, bookID, r.role, r.names)
		if err != nil {
			return false, err
		}
		changed = changed || c
	}
	c, err := syncSeries(tx, bookID, d.Series)
	if err != nil {
		return false, err
	}
	changed = changed || c
	c, err = syncGenres(tx, bookID, d.Genres)
	if err != nil {
		return false, err
	}
	return changed || c, nil
}

func isTrue(b *bool) bool { return b != nil && *b }

func syncPeople(tx *library.Tx, bookID, role string, names []string) (bool, error) {
	var want []string
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			continue
		}
		id, err := tx.EnsurePerson(n, SortName(n))
		if err != nil {
			return false, err
		}
		if !slices.Contains(want, id) {
			want = append(want, id)
		}
	}
	cur, err := tx.BookPeople(bookID)
	if err != nil {
		return false, err
	}
	var have []string
	for _, p := range cur {
		if p.Role == role {
			have = append(have, p.PersonID)
		}
	}
	if slices.Equal(want, have) {
		return false, nil
	}
	return true, tx.SetBookPeople(bookID, role, want)
}

func syncSeries(tx *library.Tx, bookID string, refs []SeriesRef) (bool, error) {
	var want []library.BookSeriesLink
	for _, r := range refs {
		if strings.TrimSpace(r.Name) == "" {
			continue
		}
		id, err := tx.EnsureSeries(r.Name, sortTitle(r.Name))
		if err != nil {
			return false, err
		}
		if !slices.ContainsFunc(want, func(l library.BookSeriesLink) bool { return l.SeriesID == id }) {
			want = append(want, library.BookSeriesLink{SeriesID: id, Sequence: r.Sequence})
		}
	}
	cur, err := tx.BookSeries(bookID)
	if err != nil {
		return false, err
	}
	same := len(cur) == len(want)
	for i := 0; same && i < len(cur); i++ {
		same = cur[i].SeriesID == want[i].SeriesID && cur[i].Sequence == want[i].Sequence
	}
	if same {
		return false, nil
	}
	return true, tx.SetBookSeries(bookID, want)
}

func syncGenres(tx *library.Tx, bookID string, genres []string) (bool, error) {
	labels, err := tx.BookLabels(bookID)
	if err != nil {
		return false, err
	}
	lower := func(in []string) []string {
		var out []string
		for _, g := range in {
			if g = strings.ToLower(strings.TrimSpace(g)); g != "" && !slices.Contains(out, g) {
				out = append(out, g)
			}
		}
		slices.Sort(out)
		return out
	}
	if slices.Equal(lower(genres), lower(labels[library.LabelGenre])) {
		return false, nil
	}
	return true, tx.SetBookLabels(bookID, library.LabelGenre, genres)
}
