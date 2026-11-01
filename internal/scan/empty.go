package scan

import "context"

// ErrFolderUnavailable means a folder is gone, is not a directory, or the walk could not read it or
// one of its subfolders, so nothing can be said about what it holds.
var ErrFolderUnavailable = errFolderUnavailable

// FolderHasAudio reports whether root holds any audio or copy-protected file Tendshelf would list
// as a book part, using the same walk as a scan (hidden entries and symbolic links are skipped).
// It never opens a file. An unreadable root or subfolder is ErrFolderUnavailable, never "false":
// "emptied on purpose" needs a complete look.
func FolderHasAudio(ctx context.Context, root string) (bool, error) {
	files, st, err := walk(ctx, root)
	if err != nil {
		if err == errFolderUnavailable {
			return false, ErrFolderUnavailable
		}
		return false, err
	}
	if len(st.Unreadable) > 0 {
		return false, ErrFolderUnavailable
	}
	return len(files) > 0, nil
}
