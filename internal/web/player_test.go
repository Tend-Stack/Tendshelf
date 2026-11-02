package web

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// playerMsgNames are the data-ts-msg-* attributes player.js and player-full.js read
// (msg("camelCase") -> kebab-case).
func playerMsgNames(t *testing.T) []string {
	t.Helper()
	var camel [][]string
	for _, name := range []string{"player.js", "player-full.js"} {
		js, err := os.ReadFile("static/js/" + name)
		if err != nil {
			t.Fatal(err)
		}
		found := regexp.MustCompile(`msg\("(\w+)"\)`).FindAllStringSubmatch(string(js), -1)
		if len(found) == 0 {
			t.Fatalf("%s reads no message", name)
		}
		camel = append(camel, found...)
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range camel {
		kebab := regexp.MustCompile(`[A-Z]`).ReplaceAllStringFunc(m[1], func(s string) string { return "-" + strings.ToLower(s) })
		if !seen[kebab] {
			seen[kebab] = true
			out = append(out, "data-ts-msg-"+kebab)
		}
	}
	return out
}

func TestPlayerSlotMarkup(t *testing.T) {
	rec, body := getPage(t, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		`<section id="player" class="ts-player-slot" aria-label="Now playing" hx-preserve hidden`,
		`<div class="ts-mini" id="mini-player">`, `data-ts-mini-progress`, `data-ts-open`, `data-ts-cover`, `data-ts-title`, `data-ts-meta`,
		`data-ts-back`, `data-ts-back-num`, `data-ts-toggle`, `data-ts-icon-play`, `data-ts-icon-pause`, `data-ts-forward`, `data-ts-forward-num`,
		`<audio id="audio" preload="none" hx-preserve></audio>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout lacks %s", want)
		}
	}
	for _, name := range playerMsgNames(t) {
		if !strings.Contains(body, name+`="`) {
			t.Errorf("#player lacks %s", name)
		}
	}
	if n := strings.Count(body, "<audio"); n != 1 {
		t.Errorf("%d audio elements, want 1", n)
	}
	// Every message the server sends has a value, and the copy is the one in copy.go.
	for name, want := range map[string]string{
		"data-ts-msg-rewound": msgRewound, "data-ts-msg-continue-ask": msgContinueAsk, "data-ts-msg-compat-wait": compatWaitText,
	} {
		if !strings.Contains(body, name+`="`+strings.ReplaceAll(want, "'", "&#39;")) {
			t.Errorf("%s is not %q", name, want)
		}
	}
	if strings.Contains(body, "style=") {
		t.Error("the layout carries a style attribute")
	}
	if i := strings.Index(body, `id="mini-player"`); i >= 0 {
		mini := body[i : i+strings.Index(body[i:], "</section>")]
		for _, bad := range []string{"<script", "onclick", "innerHTML"} {
			if strings.Contains(mini, bad) {
				t.Errorf("the mini-player contains %q", bad)
			}
		}
	}
}

func TestPlayerPlayerMsgsAreAllSet(t *testing.T) {
	m := playerMsgs()
	for name, v := range m.Attrs() {
		if v == "" {
			t.Errorf("%s is empty", name)
		}
	}
	if len(m.Attrs()) < 15 {
		t.Errorf("only %d messages", len(m.Attrs()))
	}
}

func TestPlayerLayoutAssets(t *testing.T) {
	_, body := getPage(t, "/", nil)
	css := regexp.MustCompile(`href="/static/css/player\.[0-9a-f]+\.css"|href="[^"]*css/player[^"]*\.css"`)
	if !css.MatchString(body) {
		t.Error("the layout does not link player.css")
	}
	// async: a plain module script delays DOMContentLoaded on every page (make perf at 4x went
	// from 64 to 135 ms); async lets pages become interactive while the player loads, and a
	// click that arrives first follows the play link's plain /stream fallback.
	js := regexp.MustCompile(`<script type="module" async src="[^"]*js/player[^"]*\.js"></script>`)
	if !js.MatchString(body) {
		t.Error("the layout does not load player.js as an async module")
	}
	if n := len(regexp.MustCompile(`js/player\.[0-9a-f]*\.?js`).FindAllString(body, -1)); n != 1 {
		t.Errorf("player.js is loaded %d times", n)
	}
	full := regexp.MustCompile(`<script type="module" async src="[^"]*js/player-full[^"]*\.js"></script>`)
	if n := len(full.FindAllString(body, -1)); n != 1 || strings.Index(body, "js/player-full") < strings.Index(body, "js/player.") {
		t.Errorf("player-full.js is loaded %d times as an async module after player.js", n)
	}
	wave := regexp.MustCompile(`<script type="module" async src="[^"]*js/waveform[^"]*\.js"></script>`)
	if n := len(wave.FindAllString(body, -1)); n != 1 || strings.Index(body, "js/waveform") < strings.Index(body, "js/player-full") {
		t.Errorf("waveform.js is loaded %d times as an async module after player-full.js", n)
	}
	// Player CSS comes before a page's own sheet and after the shared ones.
	if i, j := strings.Index(body, "css/motion"), strings.Index(body, "css/player"); i < 0 || j < i {
		t.Error("player.css is not linked after motion.css")
	}
	// The sign-in frame has no player.
	frame := send(newTestApp(t).h, http.MethodGet, "/setup", nil, nil).Body.String()
	if frame == "" || strings.Contains(frame, "css/player") || strings.Contains(frame, "js/player") || strings.Contains(frame, `id="audio"`) {
		t.Error("the sign-in frame links the player")
	}
}

func TestPlayerKeysHelp(t *testing.T) {
	_, body := getPage(t, "/", nil)
	for _, want := range []string{
		`<dialog id="keys-help" class="ts-keys" aria-labelledby="keys-help-title">`, `id="keys-help-title"`, msgKeysTitle,
		msgKeysPlay, msgKeysSkip, msgKeysSearch, msgKeysLibrary, msgKeysHome, msgKeysList,
		`<form method="dialog"><button class="ts-btn">` + msgKeysClose + `</button></form>`,
		`<kbd>Space</kbd>`, `<kbd>/</kbd>`, `<kbd>?</kbd>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("keys sheet lacks %q", want)
		}
	}
	// It comes after the toasts in the page, outside #main.
	if i, j := strings.Index(body, `id="toasts"`), strings.Index(body, `id="keys-help"`); i < 0 || j < i {
		t.Error("the keys sheet is not after #toasts")
	}
	if strings.Index(body, `id="keys-help"`) < strings.Index(body, "</main>") {
		t.Error("the keys sheet is inside #main")
	}
}

// TestPlayerJSRules is the static CSP check: no way for text to become markup or code, no second media
// element, no imports, and style writes only through the CSSOM.
func TestPlayerJSRules(t *testing.T) {
	banned := []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "eval(", "new Function", "new Audio", "document.write", "createElement(\"audio\")", "createElement(\"video\")", "setAttribute(\"style\""}
	style := regexp.MustCompile(`\.style\.(\w+)`)
	for _, name := range []string{"player.js", "player-full.js", "shell.js", "waveform.js"} {
		b, err := os.ReadFile("static/js/" + name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, bad := range banned {
			if strings.Contains(src, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
		for i, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "import ") {
				t.Errorf("%s:%d imports a module", name, i+1)
			}
		}
		for _, m := range style.FindAllStringSubmatch(src, -1) {
			if m[1] != "setProperty" {
				t.Errorf("%s uses .style.%s (only style.setProperty is allowed)", name, m[1])
			}
		}
	}
	b, _ := os.ReadFile("static/js/player.js")
	if n := strings.Count(string(b), `getElementById("audio")`); n != 1 {
		t.Errorf("player.js looks up #audio %d times", n)
	}
	// Words come from the server: no sentence is hard-coded in the player.
	for _, name := range []string{"player.js", "player-full.js"} {
		src, _ := os.ReadFile("static/js/" + name)
		for _, m := range regexp.MustCompile(`"[A-Z][a-z]+ [a-z]+[^"]*"`).FindAllString(string(src), -1) {
			t.Errorf("%s hard-codes the text %s", name, m)
		}
	}
	// Only player.js touches the media element; player-full.js goes through window.tsPlayer.
	full, _ := os.ReadFile("static/js/player-full.js")
	if strings.Contains(string(full), "getElementById(\"audio\")") || regexp.MustCompile(`\baudio\.`).Match(full) {
		t.Error("player-full.js touches #audio")
	}
	// Volume, rate and pitch belong to player.js alone (P1c-5b).
	for _, banned := range []string{".volume =", "playbackRate", "preservesPitch"} {
		if strings.Contains(string(full), banned) {
			t.Errorf("player-full.js contains %q", banned)
		}
	}
}
