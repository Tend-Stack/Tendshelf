package runtime

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// rtImports lists the import paths of one Go file.
func rtImports(t *testing.T, path string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range f.Imports {
		p, err := strconv.Unquote(s.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}
