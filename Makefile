# Tendshelf build. Standard Go toolchain only; no Node, no npm.
GO       ?= go
BIN      ?= bin/tendshelf
ADDR     ?= 127.0.0.1:8095
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build test run check fmt vet clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/tendshelf

test:
	$(GO) test ./...

run: build
	./$(BIN) -addr $(ADDR)

fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

# The commit gate: formatting, vet and race-enabled tests.
check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(GO) test -race ./...

clean:
	rm -rf bin
