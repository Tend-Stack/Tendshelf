//go:build !tendshelf_probe

package main

// attachProbe is a no-op in every normal build. The probe lives in probe_on.go behind a build tag.
func attachProbe(*windowUI, *Session) func(message string) bool { return nil }
