package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func TestMain(m *testing.M) {
	mediatest.RunFakeIfRequested()
	os.Exit(m.Run())
}

// foundAt builds Found values for slash paths below /L.
func foundAt(paths ...string) []Found {
	var out []Found
	for _, p := range paths {
		full := filepath.FromSlash("/L/" + p)
		rel := filepath.ToSlash(filepath.Dir(p))
		if rel == "." {
			rel = ""
		}
		out = append(out, Found{Path: full, RelDir: rel, Name: filepath.Base(p), Kind: media.Classify(p)})
	}
	return out
}

// shape is a book as the table writes it: root, then the file paths below /L, in order.
type shape struct {
	Root   string
	Single bool
	Files  []string
}

func shapes(gs []Group) []shape {
	var out []shape
	for _, g := range gs {
		s := shape{Root: strings.TrimPrefix(g.Root, "/L/"), Single: g.Single}
		for _, f := range g.Files {
			s.Files = append(s.Files, strings.TrimPrefix(f.Path, "/L/"))
		}
		out = append(out, s)
	}
	return out
}

func TestGroupFiles(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  []shape
	}{
		{"single at root", []string{"A.m4b"},
			[]shape{{"A.m4b", true, []string{"A.m4b"}}}},
		{"author and book", []string{"Au/Book/x.m4b"},
			[]shape{{"Au/Book", false, []string{"Au/Book/x.m4b"}}}},
		{"author series book", []string{"Au/Ser/Book 2/x.m4b"},
			[]shape{{"Au/Ser/Book 2", false, []string{"Au/Ser/Book 2/x.m4b"}}}},
		{"numbered mp3 folder", numbered("Au/B/", 12),
			[]shape{{"Au/B", false, numbered("Au/B/", 12)}}},
		{"disc folders and loose intro",
			[]string{"Au/B/CD1/a.mp3", "Au/B/CD 2/a.mp3", "Au/B/Disc 3/a.mp3", "Au/B/intro.mp3"},
			[]shape{{"Au/B", false, []string{"Au/B/intro.mp3", "Au/B/CD1/a.mp3", "Au/B/CD 2/a.mp3", "Au/B/Disc 3/a.mp3"}}}},
		{"disc folder at the library root is ordinary", []string{"CD1/a.mp3"},
			[]shape{{"CD1", false, []string{"CD1/a.mp3"}}}},
		{"container: loose files are singles",
			[]string{"Au/L1.m4b", "Au/L2.m4b", "Au/B/x.m4b"},
			[]shape{{"Au/B", false, []string{"Au/B/x.m4b"}}, {"Au/L1.m4b", true, []string{"Au/L1.m4b"}}, {"Au/L2.m4b", true, []string{"Au/L2.m4b"}}}},
		{"two distinct m4b are two books", []string{"Au/T A.m4b", "Au/T B.m4b"},
			[]shape{{"Au/T A.m4b", true, []string{"Au/T A.m4b"}}, {"Au/T B.m4b", true, []string{"Au/T B.m4b"}}}},
		{"split-part m4b is one book", []string{"Au/B/B - Part 1.m4b", "Au/B/B - Part 2.m4b"},
			[]shape{{"Au/B", false, []string{"Au/B/B - Part 1.m4b", "Au/B/B - Part 2.m4b"}}}},
		{"x of y markers", []string{"Au/B/B 1 of 2.m4b", "Au/B/B 2 of 2.m4b"},
			[]shape{{"Au/B", false, []string{"Au/B/B 1 of 2.m4b", "Au/B/B 2 of 2.m4b"}}}},
		{"one marked and one not: singles", []string{"Au/B/B - Part 1.m4b", "Au/B/Other.m4b"},
			[]shape{{"Au/B/B - Part 1.m4b", true, []string{"Au/B/B - Part 1.m4b"}}, {"Au/B/Other.m4b", true, []string{"Au/B/Other.m4b"}}}},
		{"mixed formats without numbers", []string{"Au/B/a.mp3", "Au/B/b.flac"},
			[]shape{{"Au/B", false, []string{"Au/B/a.mp3", "Au/B/b.flac"}}}},
		{"protected file", []string{"Au/B/x.aax"},
			[]shape{{"Au/B", false, []string{"Au/B/x.aax"}}}},
		{"disc folder beside a real subfolder: container, disc is ordinary",
			[]string{"Au/B/CD1/a.mp3", "Au/B/Sub/b.mp3", "Au/B/c.mp3"},
			[]shape{{"Au/B/CD1", false, []string{"Au/B/CD1/a.mp3"}}, {"Au/B/Sub", false, []string{"Au/B/Sub/b.mp3"}}, {"Au/B/c.mp3", true, []string{"Au/B/c.mp3"}}}},
		{"discs only under a series folder", []string{"Au/Ser/B1/CD1/a.mp3", "Au/Ser/B1/CD2/a.mp3", "Au/Ser/B2/x.mp3"},
			[]shape{{"Au/Ser/B1", false, []string{"Au/Ser/B1/CD1/a.mp3", "Au/Ser/B1/CD2/a.mp3"}}, {"Au/Ser/B2", false, []string{"Au/Ser/B2/x.mp3"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shapes(GroupFiles("/L", foundAt(c.files...)))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("books =\n%+v\nwant\n%+v", got, c.want)
			}
		})
	}
}

func TestGroupTitles(t *testing.T) {
	gs := GroupFiles("/L", foundAt("A Tale.m4b", "Au/My Book/01.mp3"))
	if len(gs) != 2 || gs[0].Title != "A Tale" || gs[1].Title != "My Book" {
		t.Fatalf("titles = %+v", gs)
	}
}

func numbered(prefix string, n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, prefix+twoDigits(i)+".mp3")
	}
	return out
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
