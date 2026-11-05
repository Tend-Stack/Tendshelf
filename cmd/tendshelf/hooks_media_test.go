package main

import (
	"flag"
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
}

func TestWaveformsFlag(t *testing.T) {
	parse := func(args ...string) bool {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		mf := addMediaFlags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return *mf.waveforms
	}
	t.Setenv("TENDSHELF_WAVEFORMS", "")
	if !parse() {
		t.Error("waveforms should be on by default")
	}
	t.Setenv("TENDSHELF_WAVEFORMS", "false")
	if parse() {
		t.Error("TENDSHELF_WAVEFORMS=false should turn waveforms off")
	}
	if !parse("-waveforms") || parse("-waveforms=false") {
		t.Error("the flag should override the environment")
	}
	t.Setenv("TENDSHELF_WAVEFORMS", "maybe")
	if !parse() {
		t.Error("a bad environment value should leave the default (on)")
	}
}
