package scan

import (
	"reflect"
	"testing"
)

func TestTrackNumbers(t *testing.T) {
	cases := []struct {
		name, file, dir, title string
		disc, track            int
		okDisc, okTrack        bool
	}{
		{"leading track", "01 - Part.mp3", "/L/B", "", 0, 1, false, true},
		{"disc and track in name", "Disc 2 Track 05.mp3", "/L/B", "", 2, 5, true, true},
		{"cd in name", "CD1 - 12.mp3", "/L/B", "", 1, 12, true, true},
		{"disc folder", "07.mp3", "/L/B/CD1", "", 1, 7, true, true},
		{"disc folder overrides name", "Disc 9 - 07.mp3", "/L/B/Disc 2", "", 2, 7, true, true},
		{"title removed", "Book 3 - 04.mp3", "/L/Book 3", "Book 3", 0, 4, false, true},
		{"title kept when absent", "Book 3 - 04.mp3", "/L/B", "", 0, 3, false, true},
		{"no numbers", "intro.mp3", "/L/B", "", 0, 0, false, false},
		{"four digit run", "Chapter 2024.mp3", "/L/B", "", 0, 2024, false, true},
		{"long run is cut at four", "123456.mp3", "/L/B", "", 0, 1234, false, true},
		{"discography is not a disc", "Discography 5.mp3", "/L/B", "", 0, 5, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, tr, okD, okT := trackNumbers(c.file, c.dir, c.title)
			if d != c.disc || tr != c.track || okD != c.okDisc || okT != c.okTrack {
				t.Fatalf("got (%d,%d,%v,%v), want (%d,%d,%v,%v)", d, tr, okD, okT, c.disc, c.track, c.okDisc, c.okTrack)
			}
		})
	}
}

func TestNaturalCmp(t *testing.T) {
	order := []string{"1", "2", "10", "a", "A1", "a2", "a10", "b"}
	for i := range order {
		for j := range order {
			got := naturalCmp(order[i], order[j])
			want := cmpInt(i, j)
			if (got < 0) != (want < 0) || (got > 0) != (want > 0) {
				t.Errorf("naturalCmp(%q, %q) = %d, want sign %d", order[i], order[j], got, want)
			}
		}
	}
	if naturalCmp("File", "file") != 0 {
		t.Error("case must not matter")
	}
	if naturalCmp("1", "01") >= 0 {
		t.Error("1 must sort before 01 (stable, deterministic)")
	}
}

func ids(in []orderInput) []string {
	var out []string
	for _, f := range in {
		out = append(out, f.ID)
	}
	return out
}

func TestSmartOrder(t *testing.T) {
	name := func(id string, d, tr int) orderInput {
		return orderInput{ID: id, Path: "/b/" + id, Key: id, NameDisc: d, NameTrack: tr, HasNameDisc: d > 0, HasNameTrack: tr > 0}
	}
	tags := func(o orderInput, d, tr int) orderInput {
		o.TagDisc, o.TagTrack, o.HasTagDisc, o.HasTagTrack = d, tr, d > 0, tr > 0
		return o
	}
	cases := []struct {
		name string
		in   []orderInput
		want []string
	}{
		{"sequential tag discs win", []orderInput{
			tags(name("a", 0, 1), 2, 1), tags(name("b", 0, 2), 1, 2), tags(name("c", 0, 1), 1, 1),
		}, []string{"c", "b", "a"}},
		{"tag discs with a gap are ignored", []orderInput{
			tags(name("a", 0, 2), 1, 2), tags(name("b", 0, 1), 3, 1),
		}, []string{"b", "a"}},
		{"name discs used when tags have none", []orderInput{
			name("x", 2, 1), name("y", 1, 5), name("z", 1, 6),
		}, []string{"y", "z", "x"}},
		{"names win with more distinct tracks", []orderInput{
			tags(name("a", 0, 3), 0, 1), tags(name("b", 0, 1), 0, 1), tags(name("c", 0, 2), 0, 1),
		}, []string{"b", "c", "a"}},
		{"tie goes to tags", []orderInput{
			tags(name("a", 0, 1), 0, 2), tags(name("b", 0, 2), 0, 1),
		}, []string{"b", "a"}},
		{"missing tracks last, then natural name", []orderInput{
			{ID: "n10", Path: "/b/n10", Key: "n10"}, {ID: "n2", Path: "/b/n2", Key: "n2"}, name("one", 0, 1),
		}, []string{"one", "n2", "n10"}},
		{"natural 2 before 10 on equal tracks", []orderInput{
			{ID: "10", Path: "/b/10", Key: "Part 10"}, {ID: "2", Path: "/b/2", Key: "Part 2"},
		}, []string{"2", "10"}},
		{"path breaks a full tie", []orderInput{
			{ID: "b", Path: "/b/2", Key: "x"}, {ID: "a", Path: "/b/1", Key: "x"},
		}, []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := append([]orderInput(nil), c.in...)
			got := ids(smartOrder(in))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("order = %v, want %v", got, c.want)
			}
			if !reflect.DeepEqual(in, c.in) {
				t.Fatal("input slice was reordered")
			}
		})
	}
}

func TestTagNumber(t *testing.T) {
	cases := []struct {
		tags map[string]string
		n    int
		ok   bool
	}{
		{map[string]string{"track": "7"}, 7, true},
		{map[string]string{"track": "07/24"}, 7, true},
		{map[string]string{"tracknumber": " 3 / 9 "}, 3, true},
		{map[string]string{"track": "x"}, 0, false},
		{map[string]string{"track": "", "tracknumber": "4"}, 4, true},
		{nil, 0, false},
	}
	for _, c := range cases {
		n, ok := tagNumber(c.tags, "track", "tracknumber")
		if n != c.n || ok != c.ok {
			t.Errorf("tagNumber(%v) = (%d,%v), want (%d,%v)", c.tags, n, ok, c.n, c.ok)
		}
	}
}

func TestProvisionalOrder(t *testing.T) {
	fs := foundAt("B/10.mp3", "B/2.mp3", "B/CD2/1.mp3", "B/CD1/9.mp3", "B/intro.mp3", "B/1.mp3")
	provisionalOrder("B", fs)
	var got []string
	for _, f := range fs {
		got = append(got, f.Path[len("/L/B/"):])
	}
	want := []string{"1.mp3", "2.mp3", "10.mp3", "intro.mp3", "CD1/9.mp3", "CD2/1.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
