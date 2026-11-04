//go:build !(linux || darwin || freebsd)

package media

// LowerPriority is a no-op where the platform has no per-process niceness call we use.
func LowerPriority(pid int) {}
