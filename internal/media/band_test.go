package media

import "testing"

func TestBandLevels(t *testing.T) {
	for _, tint := range []string{"#1f4a52", "#000000", "#ffffff", "#f0ddaa", "#e2703a"} {
		lv := BandLevels(tint)
		if lv == nil {
			t.Fatalf("%s: nil levels", tint)
		}
		for _, th := range Themes {
			level, ok := lv[th.Name]
			if !ok {
				// Absent: even the weakest step must fail.
				last := BandSteps[len(BandSteps)-1]
				if bandPasses(th, mixHex(th.Ground, tint, BandMax[th.Name]*last)) {
					t.Errorf("%s %s: no level but the weakest passes", tint, th.Name)
				}
				continue
			}
			if level < 1 || level > len(BandSteps) {
				t.Fatalf("%s %s: level %d out of range", tint, th.Name, level)
			}
			for i, k := range BandSteps {
				band := mixHex(th.Ground, tint, BandMax[th.Name]*k)
				passes := Contrast(th.Ink, band) >= minBandText && Contrast(th.InkMuted, band) >= minBandText && sideOfInk(th, band) == sideOfInk(th, th.Ground)
				switch {
				case i+1 == level && !passes:
					t.Errorf("%s %s: chosen level %d fails (%s)", tint, th.Name, level, band)
				case i+1 < level && passes:
					t.Errorf("%s %s: stronger level %d passes but %d was chosen", tint, th.Name, i+1, level)
				}
			}
		}
	}
}

func TestBandLevelsInvalid(t *testing.T) {
	for _, tint := range []string{"", "teal", "#fff", "#12345g"} {
		if BandLevels(tint) != nil {
			t.Errorf("%q: want nil", tint)
		}
	}
}

func TestBandLevelsBoardTeal(t *testing.T) {
	lv := BandLevels("#1f4a52")
	if _, ok := lv["dark"]; !ok {
		t.Errorf("dark has no band for the board teal: %v", lv)
	}
}

func TestMixHex(t *testing.T) {
	for _, c := range []struct {
		g, t string
		a    float64
		want string
	}{
		{"#000000", "#ffffff", 0, "#000000"},
		{"#000000", "#ffffff", 1, "#ffffff"},
		{"#000000", "#ffffff", 0.5, "#808080"},
	} {
		if got := mixHex(c.g, c.t, c.a); got != c.want {
			t.Errorf("mixHex(%s,%s,%v) = %s, want %s", c.g, c.t, c.a, got, c.want)
		}
	}
}
