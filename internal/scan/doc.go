// Package scan turns the files of a library's folders into books.
//
// ScanLibrary walks each folder (never following links, never writing), groups the audio files
// into books (Author/Series/Book folders, single files, numbered MP3 folders, multi-disc folders),
// and diffs the result against the database: new, changed, moved and missing files and books. A
// moved file keeps its ID, found by its quick hash; a renamed book folder keeps its book. Nothing is
// ever deleted because it vanished; a folder that cannot be read, or that suddenly shows no files,
// changes nothing. ProbeFile reads one file with ffprobe, records the answer, and when it settles
// a book's last unread file, finalises the book (play order, offsets, duration, chapters, refusal)
// in the same transaction. Copy-protected files are listed as refused and never opened.
//
// Both run as jobs (Register): scan.library and media.probe. Every write is one guarded
// transaction that begins with the job's fence check. Metadata, covers and periodic scans are
// later slices; see docs/architecture.md and docs/roadmap.md.
package scan
