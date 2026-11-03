//go:build !unix

package media

// FreeBytes is not available on this platform; the caller then refuses to write.
func FreeBytes(dir string) (uint64, error) { return 0, ErrNoSpaceInfo }
