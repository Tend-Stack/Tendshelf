package media

import (
	"reflect"
	"strings"
	"testing"
)

func TestWriteTagsArgs(t *testing.T) {
	const pre = "-nostdin -hide_banner -loglevel error -protocol_whitelist file,pipe -y -i file:/in/a"
	chapters := []WriteChapter{{0, 1000, "One"}}
	cases := []struct {
		name string
		meta string
		w    TagWrite
		want string
	}{
		{"mp4 keep chapters", "", TagWrite{Format: "mp4", Tags: map[string]string{"title": "T", "album": "A"}},
			pre + " -map 0 -map_metadata 0 -map_chapters 0 -c copy -metadata album=A -metadata title=T -movflags +use_metadata_tags -f mp4 file:/out/b"},
		{"mp4 chapters", "/meta/c.ffmeta", TagWrite{Format: "mp4", Tags: map[string]string{"title": "T"}, Chapters: chapters, WriteChapters: true},
			pre + " -f ffmetadata -i file:/meta/c.ffmeta -map 0 -map_metadata 0 -map_chapters 1 -c copy -metadata title=T -movflags +use_metadata_tags -f mp4 file:/out/b"},
		{"mp3 keep chapters", "", TagWrite{Format: "mp3", Tags: map[string]string{"title": "T"}},
			pre + " -map 0 -map_metadata 0 -map_chapters 0 -c copy -metadata title=T -id3v2_version 4 -f mp3 file:/out/b"},
		{"mp3 chapters", "/meta/c.ffmeta", TagWrite{Format: "mp3", Tags: map[string]string{"title": "T"}, WriteChapters: true},
			pre + " -f ffmetadata -i file:/meta/c.ffmeta -map 0 -map_metadata 0 -map_chapters 1 -c copy -metadata title=T -id3v2_version 4 -f mp3 file:/out/b"},
		{"flac", "", TagWrite{Format: "flac", Tags: map[string]string{"title": "T"}},
			pre + " -map 0 -map_metadata 0 -map_chapters 0 -c copy -metadata title=T -f flac file:/out/b"},
		{"flac description is comment", "", TagWrite{Format: "flac", Tags: map[string]string{"description": "D"}},
			pre + " -map 0 -map_metadata 0 -map_chapters 0 -c copy -metadata comment=D -f flac file:/out/b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := writeTagsArgs("/in/a", "/out/b", c.meta, c.w)
			if s := strings.Join(got, " "); s != c.want {
				t.Errorf("args\n got %s\nwant %s", s, c.want)
			}
		})
	}
}

func TestWriteTagsArgsNoShell(t *testing.T) {
	// A value full of shell syntax stays one argument, byte for byte.
	v := "a; rm -rf / $(x) `y` \"q\" 'z' | > &"
	args := writeTagsArgs("/in/a b", "/out/c d", "", TagWrite{Format: "mp3", Tags: map[string]string{"title": v}})
	found := false
	for i, a := range args {
		if a == "-metadata" && args[i+1] == "title="+v {
			found = true
		}
	}
	if !found {
		t.Errorf("tag value was altered or split: %q", args)
	}
	if args[len(args)-1] != "file:/out/c d" {
		t.Errorf("output = %q", args[len(args)-1])
	}
}

func TestWriteTagsEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"a=b", `a\=b`},
		{"a;b#c", `a\;b\#c`},
		{`back\slash`, `back\\slash`},
		{"two\nlines", "two\\\nlines"},
		{`\=`, `\\\=`},
	}
	for _, c := range cases {
		if got := escapeFFMeta(c.in); got != c.want {
			t.Errorf("escape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := ffmetadataChapters([]WriteChapter{{0, 1500, "A=B"}, {1500, 3000, "C"}})
	want := ";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1500\ntitle=A\\=B\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1500\nEND=3000\ntitle=C\n"
	if got != want {
		t.Errorf("ffmetadata\n got %q\nwant %q", got, want)
	}
}

func TestWriteTagsFormat(t *testing.T) {
	cases := []struct {
		container, path, want string
		ok                    bool
	}{
		{"mp4", "/a/b.m4b", "mp4", true},
		{"mp4", "/a/b.M4A", "mp4", true},
		{"mov", "/a/b.mp4", "mp4", true},
		{"mp3", "/a/b.mp3", "mp3", true},
		{"flac", "/a/b.flac", "flac", true},
		{"ogg", "/a/b.ogg", "", false},
		{"mp3", "/a/b.m4b", "", false},
		{"mp4", "/a/b.aax", "", false},
		{"mp4", "/a/b.mp3", "", false},
	}
	for _, c := range cases {
		got, ok := WriteFormat(c.container, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("WriteFormat(%q, %q) = %q, %v", c.container, c.path, got, ok)
		}
	}
}

func TestWriteTagsBookTags(t *testing.T) {
	b := BookInfo{
		Title: "The Book", Subtitle: "Sub", Publisher: "Pub", Description: "D", Language: "en",
		ASIN: "B000", ISBN: "978", Series: "S", Sequence: "2", Year: 2020,
		Authors: []string{"A One", "A Two"}, Narrators: []string{"N One", " ", "N Two"}, Genres: []string{"Fantasy", "Epic"},
	}
	want := map[string]string{
		"album": "The Book", "title": "The Book", "artist": "A One, A Two", "album_artist": "A One, A Two",
		"composer": "N One, N Two", "date": "2020", "genre": "Fantasy; Epic", "publisher": "Pub",
		"description": "D", "language": "en", "subtitle": "Sub", "series": "S", "series-part": "2", "asin": "B000", "isbn": "978",
	}
	if got := BookTags(b, false); !reflect.DeepEqual(got, want) {
		t.Errorf("single file\n got %v\nwant %v", got, want)
	}
	delete(want, "title")
	if got := BookTags(b, true); !reflect.DeepEqual(got, want) {
		t.Errorf("multi file\n got %v\nwant %v", got, want)
	}
	if got := BookTags(BookInfo{Title: "Only"}, false); !reflect.DeepEqual(got, map[string]string{"album": "Only", "title": "Only"}) {
		t.Errorf("empty values were kept: %v", got)
	}
}
