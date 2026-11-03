# Tendshelf build. Standard Go toolchain only; no Node, no npm.
GO       ?= go
BIN      ?= bin/tendshelf
ADDR     ?= 127.0.0.1:8095
# Version tags are vYEAR.RELEASE.BUGFIX, e.g. v2026.1.0 (docs/decisions/0006-version-numbers.md).
VERSION  ?= $(shell git describe --tags --long --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: app build generate test run run-dev check fmt vet clean visual visual-compare visual-full visual-webkit player perf test-media test-scan-real

generate:
	$(GO) tool templ generate

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/tendshelf

test:
	$(GO) test ./...

# The dev build tag adds the /dev pages (kitchen sink) and their fragments; release builds omit them.
run-dev: generate
	$(GO) run -tags dev ./cmd/tendshelf -addr $(ADDR)

# Opens the running server (make run or make run-dev) in a WebKitGTK 6.0 window, the engine the Wails
# desktop app uses on Linux. Linux only; development convenience, not part of the build.
WEBKIT_APP ?= /usr/lib/x86_64-linux-gnu/webkitgtk-6.0/MiniBrowser
app:
	$(WEBKIT_APP) http://$(ADDR)/ >/dev/null 2>&1 &

run: build
	./$(BIN) -addr $(ADDR)

fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

# The commit gate: formatting, vet and race-enabled tests.
check:
	@out="$$(gofmt -l $$($(GO) list -f '{{.Dir}}' ./...))"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(GO) vet -tags dev ./internal/web/...
	$(GO) test -race -timeout 25m ./...
	$(GO) test -race -timeout 25m -tags dev ./internal/web/...

# Screenshots and motion checks in Chromium through its remote-debugging pipe (no driver, no module).
# Needs TENDSHELF_TEST_BROWSER=/path/to/chrome. Output: .local/tests/visual/<run>/chromium/.
# VISUAL_RUN narrows the run to one test. `make visual` uses the fast matrix (visualMatrix in
# internal/web/visual_matrix_test.go); `make visual-full` captures every theme at every width and is
# what a page package runs before merge.
VISUAL_RUN ?= Visual

visual:
	$(GO) test -count=1 -tags dev -run '$(VISUAL_RUN)' ./internal/web/

# Runs the capture set twice into separate folders and fails, naming them, on any PNG that differs.
# To prove a change is pixel-identical: capture before (TENDSHELF_VISUAL_OUT=/tmp/before make visual),
# change, capture after, then `go run ./internal/testutil/capturediff /tmp/before /tmp/after`.
visual-compare:
	rm -rf .local/tests/visual-compare
	TENDSHELF_VISUAL_OUT=$(CURDIR)/.local/tests/visual-compare/a $(GO) test -count=1 -tags dev -run '$(VISUAL_RUN)' ./internal/web/
	TENDSHELF_VISUAL_OUT=$(CURDIR)/.local/tests/visual-compare/b $(GO) test -count=1 -tags dev -run '$(VISUAL_RUN)' ./internal/web/
	$(GO) run ./internal/testutil/capturediff .local/tests/visual-compare/a .local/tests/visual-compare/b

visual-full:
	TENDSHELF_VISUAL_FULL=1 $(GO) test -count=1 -timeout 40m -tags dev -run '$(VISUAL_RUN)' ./internal/web/

# The web player in Chromium: first sound, the file boundary, saves, the beacon, "Continue there?", compat,
# keys and layout, the book page and Home play buttons (P1c-6), and the full player (P1c-5) (headless Chromium
# plays without an audio device after a real click). Needs TENDSHELF_TEST_BROWSER. Numbers and captures:
# .local/tests/player/<run>/. About three minutes.
player:
	$(GO) test -count=1 -tags dev -timeout 10m -run 'TestPlayerBrowser|TestFullPlayerBrowser|TestPlayButtonBrowser|TestHomeHeroBrowser|TestPlayChaptersBrowser|TestBookmarkNoteKeepsPlaying|TestWaveformBrowser|TestBookPageBrowser|TestSwapPlayerBrowser|TestPlayerBarBrowser|TestShareLinkBrowser|TestNowPanelBrowser|TestPhoneCheck|TestPhoneBubble|TestNavRailBrowser' -v ./internal/web/

# The Library page on 2,000 books at 390 px with the CPU throttled 4x: median time to interactive must
# be 300 ms or less (one warm-up load, then five). Needs TENDSHELF_TEST_BROWSER. Numbers and the CPU
# model are logged; the JSON goes to .local/tests/perf/<run>/library.json.
perf:
	TENDSHELF_TEST_PERF=1 $(GO) test -count=1 -v -run 'TestPerf' ./internal/web/

# The same pages in WebKitGTK through WebKitWebDriver and MiniBrowser. MiniBrowser has no headless
# mode: use `xvfb-run -a make visual-webkit` to keep the window off the desktop.
# Needs TENDSHELF_TEST_WEBKIT_DRIVER=/usr/bin/WebKitWebDriver (the browser defaults to the 4.1 MiniBrowser).
visual-webkit:
	$(GO) test -count=1 -tags dev -run 'VisualWebKit' ./internal/web/

# Tests that run real ffmpeg and ffprobe (they skip without the variable). All output goes to temp directories.
test-media:
	TENDSHELF_TEST_FFMPEG=1 $(GO) test -count=1 -run 'Real' ./internal/media/...

# The library scan over files made by real ffmpeg: books, offsets, chapters and refusals.
test-scan-real:
	TENDSHELF_TEST_FFMPEG=1 $(GO) test -count=1 -run 'Real' ./internal/scan/

clean:
	rm -rf bin

# Cover, thumbnail and compatibility-copy jobs over files made by real ffmpeg, including WebP covers.
.PHONY: test-mediajobs-real
test-mediajobs-real:
	TENDSHELF_TEST_FFMPEG=1 $(GO) test -count=1 -run 'Real' ./internal/media/ ./internal/mediajobs/
