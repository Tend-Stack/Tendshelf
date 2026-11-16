package web

import "testing"

func TestBookRemoveText(t *testing.T) {
	for n, want := range map[int]string{
		1: "This moves the file Tendshelf added for this book into a hidden holding folder. It is kept for 30 days, and the owner or an admin can put it back. Files changed since then stay.",
		2: "This moves the 2 files Tendshelf added for this book into a hidden holding folder. They are kept for 30 days, and the owner or an admin can put them back. Files changed since then stay.",
		5: "This moves the 5 files Tendshelf added for this book into a hidden holding folder. They are kept for 30 days, and the owner or an admin can put them back. Files changed since then stay.",
	} {
		if got := bookRemoveText(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}
