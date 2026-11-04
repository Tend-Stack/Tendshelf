// Port of the grouping rules in Audiobookshelf server/utils/scandir.js (groupFileItemsIntoLibraryItemDirs)
// and TendVox Extractor extractor_details.py (book_layout, is_parts_set); see NOTICES.md.

package scan

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Group is one book found on disk. A Single book is one file (Root is the file's path); any other
// book is a folder (Root is the folder's path) and its Files are every audio file the folder owns,
// including those in disc subfolders.
type Group struct {
	Root, Title string
	Single      bool
	Files       []Found
}

var (
	discDirRe  = regexp.MustCompile(`(?i)^(cd|dis[ck])\s*\d{1,3}$`)
	partNameRe = regexp.MustCompile(`(?i)(\b(part|pt|disc|disk|cd)\b\.?\s*\d{1,3})|(\b\d{1,3}\s*of\s*\d{1,3}\b)`)
)

// IsDiscDir reports whether dir is named like a disc folder (CD1, Disc 2).
func IsDiscDir(dir string) bool { return discDirRe.MatchString(filepath.Base(dir)) }

func stem(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }

// GroupFiles turns the files found under one library folder (root) into books. The rules, in order:
//
//   - A file directly in root is a book by itself.
//   - A folder named like CD1, Disc 2 or Disk 3 (not at root) belongs to its parent folder, unless the
//     parent is a container; then it is an ordinary folder.
//   - A folder with any subfolder (other than its own disc folders) that holds audio is a container:
//     each file directly in it is a book by itself.
//   - A folder of two or more .m4b/.m4a files whose names all carry a part marker ("Part 2",
//     "3 of 12") is one book; without markers each file is a book by itself.
//   - Any other folder with audio is one book.
//
// The result is sorted by Root; Files of a folder book are in provisional play order.
func GroupFiles(root string, files []Found) []Group {
	root = filepath.Clean(root)
	holders := map[string]bool{} // directories with audio files directly in them
	for _, f := range files {
		holders[filepath.Dir(f.Path)] = true
	}
	// container[p] is true when some folder below p holds audio and is not a disc folder directly under p.
	container := map[string]bool{}
	for q := range holders {
		if q == root {
			continue
		}
		for p := filepath.Dir(q); ; p = filepath.Dir(p) {
			if !(IsDiscDir(q) && p == filepath.Dir(q)) {
				container[p] = true
			}
			if p == root || len(p) <= len(root) {
				break
			}
		}
	}
	// target is the folder whose book owns files that sit in dir.
	target := func(dir string) string {
		for dir != root && IsDiscDir(dir) {
			p := filepath.Dir(dir)
			if p == root || container[p] {
				break
			}
			dir = p
		}
		return dir
	}

	byDir := map[string][]Found{}
	var out []Group
	for _, f := range files {
		dir := filepath.Dir(f.Path)
		if dir == root {
			out = append(out, single(f))
			continue
		}
		t := target(dir)
		byDir[t] = append(byDir[t], f)
	}
	for dir, fs := range byDir {
		if container[dir] {
			for _, f := range fs {
				out = append(out, single(f))
			}
			continue
		}
		if len(fs) >= 2 && allM4(fs) && !allMarked(fs) {
			for _, f := range fs {
				out = append(out, single(f))
			}
			continue
		}
		g := Group{Root: dir, Title: filepath.Base(dir), Files: fs}
		provisionalOrder(g.Title, g.Files)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out
}

func single(f Found) Group {
	return Group{Root: f.Path, Title: stem(f.Name), Single: true, Files: []Found{f}}
}

func allM4(fs []Found) bool {
	for _, f := range fs {
		switch strings.ToLower(filepath.Ext(f.Name)) {
		case ".m4b", ".m4a":
		default:
			return false
		}
	}
	return true
}

func allMarked(fs []Found) bool {
	for _, f := range fs {
		if !partNameRe.MatchString(stem(f.Name)) {
			return false
		}
	}
	return true
}
