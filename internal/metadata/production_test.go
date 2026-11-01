package metadata

import "testing"

func TestFindProduction(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		abridged  *bool
		dramatize *bool
	}{
		{"unabridged", "The Tide (Unabridged)", boolp(false), nil},
		{"abridged", "An abridged edition", boolp(true), nil},
		{"full cast", "performed by a full-cast ensemble", nil, boolp(true)},
		{"graphic audio", "A GraphicAudio production", nil, boolp(true)},
		{"radio play", "a radio play", nil, boolp(true)},
		{"dramatised", "Dramatised for audio", nil, boolp(true)},
		{"both", "Unabridged full cast recording", boolp(false), boolp(true)},
		{"none", "Just a book", nil, nil},
		{"word inside another", "The Cabridgedness of Things", nil, nil},
	}
	for _, c := range cases {
		a, d := FindProduction(c.text)
		if (a == nil) != (c.abridged == nil) || (a != nil && *a != *c.abridged) {
			t.Errorf("%s: abridged = %v", c.name, a)
		}
		if (d == nil) != (c.dramatize == nil) || (d != nil && *d != *c.dramatize) {
			t.Errorf("%s: dramatized = %v", c.name, d)
		}
	}
}

func TestFillProductionKeepsSetValues(t *testing.T) {
	d := Details{Title: "A (Unabridged) full cast", Abridged: boolp(true)}
	d.fillProduction("extra")
	if !flagIs(d.Abridged, true) || !flagIs(d.Dramatized, true) {
		t.Errorf("abridged %v dramatized %v", d.Abridged, d.Dramatized)
	}
}
