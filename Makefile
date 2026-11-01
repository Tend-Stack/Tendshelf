# Tendshelf build. Standard Go toolchain only; no Node, no npm.
GO       ?= go
BIN      ?= bin/tendshelf
ADDR     ?= 127.0.0.1:8095
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: app build generate test run run-dev check fmt vet clean visual visual-webkit test-media

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
	$(GO) test -race ./...
	$(GO) test -race -tags dev ./internal/web/...

# Screenshots and motion checks in Chromium through its remote-debugging pipe (no driver, no module).
# Needs TENDSHELF_TEST_BROWSER=/path/to/chrome. Output: .local/tests/visual/<run>/chromium/.
visual:
	$(GO) test -count=1 -tags dev -run 'Visual' ./internal/web/

# The same pages in WebKitGTK through WebKitWebDriver and MiniBrowser. MiniBrowser has no headless
# mode: use `xvfb-run -a make visual-webkit` to keep the window off the desktop.
# Needs TENDSHELF_TEST_WEBKIT_DRIVER=/usr/bin/WebKitWebDriver (the browser defaults to the 4.1 MiniBrowser).
visual-webkit:
	$(GO) test -count=1 -tags dev -run 'VisualWebKit' ./internal/web/

# Tests that run real ffmpeg and ffprobe (they skip without the variable). All output goes to temp directories.
test-media:
	TENDSHELF_TEST_FFMPEG=1 $(GO) test -count=1 -run 'Real' ./internal/media/...

clean:
	rm -rf bin
