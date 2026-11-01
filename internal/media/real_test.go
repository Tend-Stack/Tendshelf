package media_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// These tests run real ffmpeg and ffprobe: `make test-media` (TENDSHELF_TEST_FFMPEG=1).

func TestRealM4B(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "book.m4b")
	mediatest.GenM4B(t, tools, path, mediatest.Synth{
		Title: "The Test Book", Author: "Ana Autora", Narrator: "Nico Narrador", Album: "The Test Book",
		Genre: "Fiction", Year: "2020", Comment: "a=b; c#d",
		Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 2000, "One"), mediatest.Chapter(2000, 4000, "Two"), mediatest.Chapter(4000, 6000, "Three")},
		Cover:    true,
	})
	p, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Container != "mp4" || p.Tags["title"] != "The Test Book" || p.Tags["comment"] != "a=b; c#d" {
		t.Errorf("container %q tags %v", p.Container, p.Tags)
	}
	if len(p.Audio) != 1 || p.Audio[0].Codec != "aac" || len(p.Pictures) != 1 || p.Encrypted {
		t.Errorf("audio %+v pictures %+v encrypted %v", p.Audio, p.Pictures, p.Encrypted)
	}
	if p.DurationMs < 5950 || p.DurationMs > 6050 {
		t.Errorf("duration %d ms", p.DurationMs)
	}
	if len(p.Chapters) != 3 {
		t.Fatalf("chapters = %+v", p.Chapters)
	}
	for i, want := range []int64{0, 2000, 4000} {
		if d := p.Chapters[i].StartMs - want; d < -50 || d > 50 {
			t.Errorf("chapter %d starts at %d, want about %d", i, p.Chapters[i].StartMs, want)
		}
	}
	if p.Chapters[1].Title != "Two" {
		t.Errorf("chapter title %q", p.Chapters[1].Title)
	}
}

func TestRealMP3Parts(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	paths := mediatest.GenMP3Parts(t, tools, t.TempDir(), 3, mediatest.Synth{Title: "Part", Author: "Ana", Album: "Album"})
	p, err := tools.Probe(context.Background(), paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if p.Container != "mp3" || p.Tags["track"] != "1/3" || p.Tags["artist"] != "Ana" {
		t.Errorf("container %q tags %v", p.Container, p.Tags)
	}
	if p.DurationMs < 1900 || p.DurationMs > 2200 {
		t.Errorf("duration %d ms", p.DurationMs)
	}
}

func TestRealFLAC(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "a.flac")
	mediatest.GenFLAC(t, tools, path, mediatest.Synth{Title: "Lossless", Author: "Ana"})
	p, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Container != "flac" || p.Tags["title"] != "Lossless" || p.Audio[0].Codec != "flac" {
		t.Errorf("probe = %+v", p)
	}
}

func TestRealEncryptedIsRefused(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "enc.m4a")
	mediatest.GenEncryptedM4A(t, tools, path)
	p, err := tools.Probe(context.Background(), path)
	if !errors.Is(err, media.ErrProtected) {
		t.Fatalf("err = %v, want ErrProtected", err)
	}
	var me *media.Error
	if !errors.As(err, &me) || me.Code != media.CodeDRM {
		t.Fatalf("err = %v", err)
	}
	if !p.Encrypted || p.Protection != media.ProtectionPacketSide {
		t.Errorf("probe = %+v", p)
	}
}

func TestRealProtectedExtension(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "book.aax")
	mediatest.WriteProtected(t, path)
	p, err := tools.Probe(context.Background(), path)
	if !errors.Is(err, media.ErrProtected) || p.Protection != media.ProtectionExtension {
		t.Fatalf("probe = %+v, err = %v", p, err)
	}
}

func TestRealJunkAndMissing(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	dir := t.TempDir()
	mediatest.WriteFake(t, filepath.Join(dir, "fake.mp3"), mediatest.FakeFile{}) // a text file with an audio name
	_, err := tools.Probe(context.Background(), filepath.Join(dir, "fake.mp3"))
	var me *media.Error
	if !errors.As(err, &me) || me.Code != media.CodeNotAudio {
		t.Fatalf("err = %v", err)
	}
}
