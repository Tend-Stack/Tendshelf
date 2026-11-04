package textmatch

import "testing"

func TestTextmatchTitleKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Dune", "dune"},
		{"Dune: The Desert Planet", "dune"},
		{"The Hunger Games (Hunger Games, #1)", "the hunger games"},
		{"Dune [Unabridged]", "dune"},
		{"Dune (Unabridged) [Audio]", "dune"},
		{"The Silent Patient: A Novel", "the silent patient"},
		{"The Silent Patient a Novel", "the silent patient"},
		{"Dune Unabridged", "dune"},
		{"Café Crème", "cafe creme"},
		{"(Untitled)", "untitled"},
		{"[Draft]", "draft"},
		{": Odd", "odd"},
		{"", ""},
		{"   ", ""},
	} {
		if got := TitleKey(c.in); got != c.want {
			t.Errorf("TitleKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTextmatchSurname(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Frank Herbert", "herbert"},
		{"Herbert, Frank", "herbert"},
		{"Martin Luther King Jr.", "king"},
		{"Ursula K. Le Guin", "guin"},
		{"Brontë", "bronte"},
		{"Doe, Jane, PhD", "phd"},
		{"Cher", "cher"},
		{"", ""},
	} {
		if got := Surname(c.in); got != c.want {
			t.Errorf("Surname(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTextmatchISBN13(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"0306406152", "9780306406157"},
		{"0-306-40615-2", "9780306406157"},
		{"urn:isbn:0306406152", "9780306406157"},
		{"9780306406157", "9780306406157"},
		{"978-0-306-40615-7", "9780306406157"},
		{"080442957X", "9780804429573"},
		{"0306406153", ""},
		{"9780306406158", ""},
		{"12345", ""},
		{"", ""},
	} {
		if got := ISBN13(c.in); got != c.want {
			t.Errorf("ISBN13(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
