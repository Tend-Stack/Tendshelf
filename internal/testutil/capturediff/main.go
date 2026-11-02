// Command capturediff compares two capture folders and fails on any difference.
//
//	go run ./internal/testutil/capturediff A B
//
// Every .png under A and B is matched by its relative path. The exit status is 1 when a file is
// missing on either side or the bytes differ; the names are printed. Last line: "N of M identical".
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: capturediff <folder-a> <folder-b>")
		os.Exit(2)
	}
	ident, total, bad, err := compare(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, line := range bad {
		fmt.Println(line)
	}
	fmt.Printf("%d of %d identical\n", ident, total)
	if len(bad) > 0 || total == 0 {
		os.Exit(1)
	}
}

// list returns the relative paths of every PNG under dir.
func list(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".png") {
			rel, _ := filepath.Rel(dir, p)
			out[rel] = true
		}
		return nil
	})
	return out, err
}

// compare returns the identical count, the total of distinct names, and one line per problem.
func compare(a, b string) (int, int, []string, error) {
	la, err := list(a)
	if err != nil {
		return 0, 0, nil, err
	}
	lb, err := list(b)
	if err != nil {
		return 0, 0, nil, err
	}
	names := map[string]bool{}
	for n := range la {
		names[n] = true
	}
	for n := range lb {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	ident := 0
	var bad []string
	for _, n := range sorted {
		switch {
		case !la[n]:
			bad = append(bad, "only in B: "+n)
		case !lb[n]:
			bad = append(bad, "only in A: "+n)
		default:
			x, _ := os.ReadFile(filepath.Join(a, n))
			y, _ := os.ReadFile(filepath.Join(b, n))
			if bytes.Equal(x, y) {
				ident++
			} else {
				bad = append(bad, "differs: "+n)
			}
		}
	}
	return ident, len(sorted), bad, nil
}
