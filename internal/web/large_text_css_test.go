package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Larger text never uses the desktop shell (P3-11): every desktop block of the shell and the player is
// wrapped in a :where() guard that turns off when the root carries data-text-size="large", and every
// guarded 768 px (tablet) block has a twin from 960 px for large text. :where() adds no specificity,
// so standard text renders as it did before the guards.
const (
	textsizeStdGuard   = `:where(html:not([data-text-size="large"])) {`
	textsizeLargeGuard = `:where(html[data-text-size="large"]) {`
)

// textsizeBlocks returns each top-level @media block of css whose header contains cond, with its header.
func textsizeBlocks(css, cond string) (headers, bodies []string) {
	lines := strings.Split(css, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "@media") || !strings.Contains(lines[i], cond) {
			continue
		}
		j := i + 1
		for j < len(lines) && lines[j] != "}" {
			j++
		}
		headers = append(headers, lines[i])
		bodies = append(bodies, strings.Join(lines[i+1:j], "\n"))
		i = j
	}
	return headers, bodies
}

func TestLargeTextShellGuard(t *testing.T) {
	for _, name := range []string{"bundle.css", "player.css"} {
		raw, err := os.ReadFile(filepath.Join("static", "css", name))
		if err != nil {
			t.Fatal(err)
		}
		css := string(raw)
		for _, cond := range []string{"min-width: 1280px", "min-width: 768px"} {
			headers, bodies := textsizeBlocks(css, cond)
			if len(headers) == 0 {
				t.Errorf("%s: no %q blocks found", name, cond)
			}
			for i, body := range bodies {
				if !strings.HasPrefix(strings.TrimSpace(body), textsizeStdGuard) {
					t.Errorf("%s: %s block %d does not open with the standard-text guard", name, cond, i)
				}
			}
		}
		// Each guarded 768 block has a 960 twin for large text with the same body.
		_, tablet := textsizeBlocks(css, "(min-width: 768px) {")
		_, twins := textsizeBlocks(css, "(min-width: 960px) {")
		if len(tablet) != len(twins) {
			t.Errorf("%s: %d tablet blocks but %d large-text twins", name, len(tablet), len(twins))
			continue
		}
		for i := range tablet {
			if !strings.HasPrefix(strings.TrimSpace(twins[i]), textsizeLargeGuard) {
				t.Errorf("%s: twin %d lacks the large-text guard", name, i)
				continue
			}
			std := strings.TrimPrefix(strings.TrimSpace(tablet[i]), textsizeStdGuard)
			lrg := strings.TrimPrefix(strings.TrimSpace(twins[i]), textsizeLargeGuard)
			if std != lrg {
				t.Errorf("%s: twin %d differs from its tablet block", name, i)
			}
		}
	}
}
