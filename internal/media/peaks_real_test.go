package media_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestRealPeaks runs the waveform decode on real ffmpeg: `make test-media`.
func TestRealPeaks(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()

	// A 6 s sine book gives 12 bins, all audible, and it decodes much faster than it plays.
	book := filepath.Join(dir, "book.m4b")
	mediatest.GenM4B(t, tools, book, mediatest.Synth{Title: "Peaks"})
	start := time.Now()
	p, err := tools.Peaks(ctx, book, 6000)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(p.Values); n < 11 || n > 13 {
		t.Fatalf("%d bins, want 12 +-1", n)
	}
	for i, v := range p.Values {
		if v == 0 {
			t.Errorf("bin %d is silent", i)
		}
	}
	enc := p.Encode()
	if _, err := media.DecodePeaks(enc); err != nil {
		t.Errorf("encoded peaks do not decode: %v", err)
	}
	t.Logf("m4b: 6 s of audio, %d bins, %d bytes in the file, %s (%.0fx real time)",
		len(p.Values), len(enc), took.Round(time.Millisecond), 6/took.Seconds())

	// Each part of a split book has its own peaks.
	for _, part := range mediatest.GenMP3Parts(t, tools, dir, 3, mediatest.Synth{Title: "Part", Author: "Ana", Album: "Album"}) {
		pp, err := tools.Peaks(ctx, part, 2000)
		if err != nil || len(pp.Values) < 3 || len(pp.Values) > 5 {
			t.Errorf("%s: %d bins, %v", filepath.Base(part), len(pp.Values), err)
		}
	}

	// An encrypted file is never decoded: ffmpeg refuses it or the caller checked first. Either way
	// there are no peaks.
	enc2 := filepath.Join(dir, "enc.m4a")
	mediatest.GenEncryptedM4A(t, tools, enc2)
	if pk, err := tools.Peaks(ctx, enc2, 2000); err == nil {
		t.Errorf("an encrypted file gave %d bins", len(pk.Values))
	} else if errors.Is(err, media.ErrProtected) {
		t.Logf("encrypted file refused as protected")
	} else {
		t.Log("encrypted file: ffmpeg could not decode it")
	}
}
