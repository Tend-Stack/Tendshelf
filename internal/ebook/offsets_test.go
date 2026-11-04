package ebook

import (
	"strings"
	"testing"
)

// TestEbookHTMLOffsets: a block's src range is its element and each segment sits at its byte offset.
func TestEbookHTMLOffsets(t *testing.T) {
	data := []byte("<?xml version=\"1.0\"?>\n<html><head><title>x</title></head><body><h1>Dep</h1>\n<p>The <em>cart</em> left.</p></body></html>")
	d, err := walkHTML(data, "p", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"<h1>Dep</h1>", "<p>The <em>cart</em> left.</p>"}
	if len(d.Blocks) != len(want) {
		t.Fatalf("%d blocks", len(d.Blocks))
	}
	for i, b := range d.Blocks {
		if got := string(data[b.Src.Start:b.Src.End]); got != want[i] {
			t.Errorf("block %d range = %q, want %q", i, got, want[i])
		}
	}
	for _, s := range d.Blocks[1].Segs {
		if !strings.HasPrefix(string(data[s.ByteOff:]), s.Text) {
			t.Errorf("segment %q does not sit at byte %d", s.Text, s.ByteOff)
		}
	}
}
