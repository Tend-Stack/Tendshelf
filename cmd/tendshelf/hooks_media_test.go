package main

import (
	"flag"
	"strings"
	"testing"
)

func TestCompatCacheFlag(t *testing.T) {
	parse := func(args ...string) *mediaFlags {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		mf := addMediaFlags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return mf
	}
	t.Setenv("TENDSHELF_COMPAT_CACHE_GIB", "")
	if got := *parse().compatGiB; got != 5 {
		t.Errorf("default = %d, want 5", got)
	}
	t.Setenv("TENDSHELF_COMPAT_CACHE_GIB", "12")
	if got := *parse().compatGiB; got != 12 {
		t.Errorf("from environment = %d, want 12", got)
	}
	if got := *parse("-compat-cache-gib", "2").compatGiB; got != 2 {
		t.Errorf("flag = %d, want 2", got)
	}
	t.Setenv("TENDSHELF_COMPAT_CACHE_GIB", "lots")
	if got := *parse().compatGiB; got != 5 {
		t.Errorf("bad environment value = %d, want the default 5", got)
	}

	_, err := registerMediaJobs(deps{}, parse("-compat-cache-gib", "0"))
	if err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Errorf("a cap below 1 GiB: err = %v", err)
	}
}
