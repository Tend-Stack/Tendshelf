//go:build !unix

package upload

// sameDevice is false where file system identity is not available: files are then copied.
func sameDevice(a, b string) bool { return false }
