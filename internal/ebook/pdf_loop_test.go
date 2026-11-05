package ebook

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
	"time"
)

// loopingPDF is a file whose page tree lists itself as its only child, with a page count that
// promises pages: walking it never ends.
func loopingPDF() []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 5 /Kids [2 0 R] >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var offs []int
	for i, o := range objs {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, x)
	return b.Bytes()
}

func TestPDFSelfReferencingPageTreeIsDamaged(t *testing.T) {
	before := runtime.NumGoroutine()
	lim := DefaultLimits()
	lim.ParseTimeout = 3 * time.Second
	start := time.Now()
	_, err := readBytes(t, "loop.pdf", loopingPDF(), lim)
	if err == nil {
		t.Fatal("a looping page tree was read")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v: the walk was not stopped by the page-tree check", time.Since(start))
	}
	wantRefusal(t, err, ReasonDamaged, "pdf_page_tree")
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines left over", n-before)
	}
}
