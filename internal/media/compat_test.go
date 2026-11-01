package media_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func TestMayNeedCompat(t *testing.T) {
	for _, c := range []struct {
		container, codec string
		want             bool
	}{
		{"mp4", "aac", false},
		{"aac", "aac", false},
		{"mp3", "mp3", false},
		{"flac", "flac", false},
		{"wav", "pcm_s16le", false},
		{"ogg", "opus", true},
		{"ogg", "vorbis", true},
		{"webm", "opus", true},
		{"mp4", "alac", true},
		{"mp4", "ac3", true},
		{"mp4", "eac3", true},
		{"asf", "wmav2", true},
		{"wav", "pcm_s24le", true},
		{"mp3", "mp2", true},
		{"amr", "amr_nb", true},
		{"", "", true},
	} {
		if got := media.MayNeedCompat(c.container, c.codec); got != c.want {
			t.Errorf("MayNeedCompat(%q, %q) = %v, want %v", c.container, c.codec, got, c.want)
		}
	}
	if media.CompatRecipe < 1 {
		t.Error("CompatRecipe must be positive")
	}
}

func TestCompatArgsFake(t *testing.T) {
	tools, runs := ffTools(t)
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "book.opus")
	mediatest.WriteFake(t, src, mediatest.FakeFile{Container: "ogg", Codec: "opus", DurationMs: 7000})
	dst := filepath.Join(dir, ".tmp-x.m4a")

	tail := " -movflags +faststart -f mp4 file:" + dst
	pre := common + " -i file:" + src + " -map 0:a:0 -vn -sn -dn -map_metadata -1 -map_chapters -1"
	cases := []struct{ codec, audio string }{
		{"opus", " -c:a aac -profile:a aac_low -b:a 128k"},
		{"aac", " -c:a copy"},
	}
	for i, c := range cases {
		if err := tools.Compat(ctx, src, dst, c.codec, 7000); err != nil {
			t.Fatalf("%s: %v", c.codec, err)
		}
		if want := pre + c.audio + tail; runs()[i] != want {
			t.Errorf("%s args =\n%s\nwant\n%s", c.codec, runs()[i], want)
		}
	}
	b, err := os.ReadFile(dst)
	if err != nil || !strings.HasPrefix(string(b), "TSFAKE1\n") {
		t.Errorf("fake output: %q, %v", b, err)
	}
	if err := tools.Compat(ctx, src, "relative.m4a", "opus", 7000); err == nil {
		t.Error("a relative output path should be refused")
	}
}
