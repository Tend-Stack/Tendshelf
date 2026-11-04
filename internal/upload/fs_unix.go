//go:build unix

package upload

import "syscall"

// sameDevice reports whether two existing paths are on the same file system, so a staged file can
// be hard-linked into the library folder instead of copied.
func sameDevice(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}
