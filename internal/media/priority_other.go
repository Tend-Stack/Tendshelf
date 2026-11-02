//go:build !(linux || darwin || freebsd)

package media

// lowerPriority is a no-op where the platform has no per-process niceness call we use.
func lowerPriority(pid int) {}
