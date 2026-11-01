# Tendshelf build. Standard Go toolchain only; no Node, no npm.
GO       ?= go
BIN      ?= bin/tendshelf
ADDR     ?= 127.0.0.1:8095
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build generate test run run-dev check fmt vet clean

generate:
	$(GO) tool templ generate

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/tendshelf

test:
	$(GO) test ./...

# P0c adds -tags dev (assets read from disk) here.
run-dev: generate
	$(GO) run ./cmd/tendshelf -addr $(ADDR)

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
	$(GO) test -race ./...

clean:
	rm -rf bin
