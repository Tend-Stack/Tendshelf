package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func render(t *testing.T, name string, filled bool) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Icon(name, filled).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestIconSet(t *testing.T) {
	names := IconNames()
	if len(names) != 49 {
		t.Fatalf("%d icons, want 49", len(names))
	}
	for _, n := range names {
		out := render(t, n, false)
		if !strings.Contains(out, `class="ts-icon"`) || !strings.Contains(out, `aria-hidden="true"`) || !strings.Contains(out, `viewBox="0 0 24 24"`) {
			t.Errorf("%s: %s", n, out)
		}
		if !strings.Contains(render(t, n, true), "ts-icon ts-icon--fill") {
			t.Errorf("%s: filled variant lacks ts-icon--fill", n)
		}
	}
}

func TestIconUnknownRendersNothing(t *testing.T) {
	if out := render(t, "no-such-icon", false); out != "" {
		t.Fatalf("unknown icon rendered %q", out)
	}
}

func TestMainNavDestinations(t *testing.T) {
	want := []string{"/", "/libraries", "/search", "/me/downloads", "/me"}
	if len(MainNav) != len(want) {
		t.Fatalf("%d items", len(MainNav))
	}
	for i, it := range MainNav {
		if it.Href != want[i] {
			t.Errorf("item %d href %q, want %q", i, it.Href, want[i])
		}
		if _, ok := iconShapes[it.Icon]; !ok {
			t.Errorf("item %s uses unknown icon %q", it.Key, it.Icon)
		}
	}
}
