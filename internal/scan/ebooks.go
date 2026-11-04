package scan

import (
	"context"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

// EbookFound is one ebook file the walk saw. Path is absolute; RelDir is the directory below the
// library folder in slash form ("" for a file directly in it).
type EbookFound struct {
	Path, RelDir, Name string
	Size, MtimeNs      int64
}

// EbookFolder is the ebook files found under one library folder. Path is the folder's own path.
type EbookFolder struct {
	FolderID, Path string
	Files          []EbookFound
}

// EbookScan is what a scan tells the ebook feature. Scope is set for a scan of one directory (only
// rows under it can vanish). Skip lists paths and folders whose absence proves nothing. Folders holds
// only folders that were walked: a folder that was unavailable or suddenly empty is not passed.
type EbookScan struct {
	LibraryID, Scope string
	Skip             []string
	Folders          []EbookFolder
}

// EbookSink receives the ebooks of a scan after the scan's write transaction has committed and before
// probes are queued. The scanner never imports the ebook package: ebooks are handed over here and
// never reach grouping or probing. Sinks log their own errors.
type EbookSink func(context.Context, EbookScan)

// OnEbooks adds an ebook sink. Call it only before Register.
func (s *Scanner) OnEbooks(f EbookSink) { s.sinks = append(s.sinks, f) }

// splitEbooks moves the ebook entries out of a walk result so grouping and probing never see them.
func splitEbooks(all []Found) (files []Found, ebooks []EbookFound) {
	files = make([]Found, 0, len(all))
	for _, f := range all {
		if f.Kind == media.KindEbook {
			ebooks = append(ebooks, EbookFound{Path: f.Path, RelDir: f.RelDir, Name: f.Name, Size: f.Size, MtimeNs: f.MtimeNs})
			continue
		}
		files = append(files, f)
	}
	return files, ebooks
}
