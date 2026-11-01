package metadata

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func TestPrecedencePinned(t *testing.T) {
	want := []string{"user", "opf", "txt", "nfo", "tags", "folder", "online"}
	if !reflect.DeepEqual(Precedence, want) {
		t.Fatalf("Precedence = %v, want %v", Precedence, want)
	}
}

func row(field, source, value string) library.MetadataValue {
	return library.MetadataValue{Field: field, Source: source, Value: json.RawMessage(value)}
}

func TestResolve(t *testing.T) {
	t.Run("tags beat folder, folder fills a gap", func(t *testing.T) {
		d, from := Resolve([]library.MetadataValue{
			row("title", "folder", `"From Folder"`), row("title", "tags", `"From Tags"`),
			row("authors", "folder", `["Ann"]`), row("narrators", "tags", `["Ben"]`),
		})
		if d.Title != "From Tags" || !reflect.DeepEqual(d.Authors, []string{"Ann"}) || !reflect.DeepEqual(d.Narrators, []string{"Ben"}) {
			t.Fatalf("details = %+v", d)
		}
		if from["title"] != "tags" || from["authors"] != "folder" || from["narrators"] != "tags" {
			t.Fatalf("sources = %v", from)
		}
	})
	t.Run("online only fills gaps", func(t *testing.T) {
		d, from := Resolve([]library.MetadataValue{
			row("title", "online", `"Online"`), row("title", "folder", `"Folder"`),
			row("publisher", "online", `"Pub"`),
		})
		if d.Title != "Folder" || d.Publisher != "Pub" || from["publisher"] != "online" {
			t.Fatalf("details = %+v, %v", d, from)
		}
	})
	t.Run("an empty user value wins", func(t *testing.T) {
		d, from := Resolve([]library.MetadataValue{
			row("subtitle", "tags", `"Tag Sub"`), row("subtitle", "user", `""`),
			row("description", "user", `"Mine"`), row("description", "tags", `"Theirs"`),
		})
		if d.Subtitle != "" || from["subtitle"] != "user" || d.Description != "Mine" {
			t.Fatalf("details = %+v, %v", d, from)
		}
	})
	t.Run("an unknown source ranks last", func(t *testing.T) {
		d, from := Resolve([]library.MetadataValue{row("title", "mystery", `"Mystery"`), row("title", "online", `"Online"`)})
		if d.Title != "Online" || from["title"] != "online" {
			t.Fatalf("details = %+v, %v", d, from)
		}
		d, _ = Resolve([]library.MetadataValue{row("title", "mystery", `"Mystery"`)})
		if d.Title != "Mystery" {
			t.Fatalf("lone unknown source = %+v", d)
		}
	})
	t.Run("types", func(t *testing.T) {
		d, _ := Resolve([]library.MetadataValue{
			row("published_year", "tags", `2020`), row("abridged", "folder", `false`), row("explicit", "tags", `true`),
			row("series", "folder", `[{"name":"S","sequence":"2"}]`), row("genres", "tags", `["A","B"]`),
			row("title", "user", `7`), row("title", "tags", `"Fallback"`), row("nonsense", "tags", `"x"`),
		})
		if d.Year != 2020 || !flagIs(d.Abridged, false) || !flagIs(d.Explicit, true) || d.Title != "Fallback" ||
			!reflect.DeepEqual(d.Series, []SeriesRef{{"S", "2"}}) || !reflect.DeepEqual(d.Genres, []string{"A", "B"}) {
			t.Fatalf("details = %+v", d)
		}
	})
}

func TestDetailsValues(t *testing.T) {
	if got := (Details{}).Values(); len(got) != 0 {
		t.Fatalf("empty details gave %v", got)
	}
	d := Details{Title: "T", Year: 2020, Authors: []string{"A"}, Series: []SeriesRef{{"S", "1"}}, Abridged: boolp(false), Genres: []string{}}
	got := d.Values()
	want := map[string]string{"title": `"T"`, "published_year": `2020`, "authors": `["A"]`, "series": `[{"name":"S","sequence":"1"}]`, "abridged": `false`}
	if len(got) != len(want) {
		t.Fatalf("values = %v", got)
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
}
