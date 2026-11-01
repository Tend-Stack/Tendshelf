package scan

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// wantDetails is what the details of one fixture book must come to.
type wantDetails struct {
	title     string
	authors   []string
	narrators []string
	series    string
	sequence  string
}

// expectDetails derives the expected details of the book at rootRel from the fixture's layout.
func expectDetails(rootRel string) wantDetails {
	p := strings.Split(rootRel, "/")
	var i int
	switch {
	case len(p) == 1 && strings.HasPrefix(p[0], "S") && strings.HasSuffix(p[0], ".m4b"):
		return wantDetails{title: strings.TrimSuffix(p[0], ".m4b")}
	case strings.HasPrefix(p[0], "Auth") && len(p) == 3: // Auth01/Series/Book 2
		fmt.Sscanf(p[2], "Book %d", &i)
		return wantDetails{title: p[2], authors: []string{p[0]}, series: "Series", sequence: fmt.Sprint(i)}
	case strings.HasPrefix(p[0], "Au") && len(p) == 2: // Au01/Book01: the file's tags win
		fmt.Sscanf(p[0], "Au%d", &i)
		return wantDetails{title: fmt.Sprintf("Tagged %02d", i), authors: []string{fmt.Sprintf("Tag Author %02d", i)},
			narrators: []string{"Narr One", "Narr Two"}}
	case len(p) == 1: // Split00
		return wantDetails{title: p[0]}
	default: // Num/N01, Disc/D01, Mix/M01, Prot/P0, Cont00/Sub, Cont00/Loose A.m4b, Two00/Alpha.m4b
		return wantDetails{title: strings.TrimSuffix(p[1], ".m4b"), authors: []string{p[0]}}
	}
}

func TestDetailsFixture200(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	buildFixture(t, e.root)
	e.scan()
	e.probeAll()
	svc := metadata.New(e.repo, e.enq, nil)

	books, err := e.repo.BooksInFolder(bg, e.folder.ID)
	if err != nil || len(books) != 200 {
		t.Fatalf("%d books, %v", len(books), err)
	}
	for _, b := range books {
		if _, err := svc.Local(bg, b.ID, nil); err != nil {
			t.Fatalf("%s: %v", b.RootPath, err)
		}
	}

	refused := 0
	for _, b := range books {
		rel := strings.TrimPrefix(strings.TrimPrefix(b.RootPath, e.root), "/")
		want := expectDetails(rel)
		got, err := e.repo.Book(bg, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		people, _ := e.repo.BookPeople(bg, b.ID)
		var authors, narrators []string
		for _, p := range people {
			if p.Role == library.RoleAuthor {
				authors = append(authors, p.Name)
			} else {
				narrators = append(narrators, p.Name)
			}
		}
		series, _ := e.repo.BookSeries(bg, b.ID)
		var seriesName, seq string
		if len(series) > 0 {
			seriesName, seq = series[0].Name, series[0].Sequence
		}
		if got.Title != want.title || !reflect.DeepEqual(authors, want.authors) || !reflect.DeepEqual(narrators, want.narrators) ||
			seriesName != want.series || seq != want.sequence || len(series) > 1 {
			t.Errorf("%s: title %q authors %q narrators %q series %q #%q; want %+v", rel, got.Title, authors, narrators, seriesName, seq, want)
		}
		if got.RefusedReason != "" {
			refused++
		}
	}
	if refused != 5 {
		t.Errorf("%d refused books got details, want 5", refused)
	}

	// A second pass over unchanged rows changes nothing.
	before := e.changes()
	for _, b := range books {
		if changed, err := svc.Local(bg, b.ID, nil); err != nil || changed {
			t.Fatalf("%s: second Local changed=%v err=%v", b.RootPath, changed, err)
		}
	}
	if after := e.changes(); after != before {
		t.Fatalf("second pass made %d row changes", after-before)
	}
}
