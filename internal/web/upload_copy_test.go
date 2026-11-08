package web

import "testing"

func TestBookRemoveText(t *testing.T) {
	for n, want := range map[int]string{
		1: "This deletes the file Tendshelf added for this book. Files changed since then stay.",
		2: "This deletes the 2 files Tendshelf added for this book. Files changed since then stay.",
		5: "This deletes the 5 files Tendshelf added for this book. Files changed since then stay.",
	} {
		if got := bookRemoveText(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}
