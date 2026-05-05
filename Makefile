.PHONY: all build install test cover bench fmt fmt-check lint vet check examples clean help

GO         ?= go
BIN        ?= tartalo
PKG        := ./...
CMD_PKG    := ./cmd/tartalo
EXAMPLES   := $(wildcard examples/*.tt) $(wildcard examples/modules/*.tt) $(wildcard examples/modules/lib/*.tt)

all: build

## build: compile the tartalo binary into ./tartalo
build:
	$(GO) build -o $(BIN) $(CMD_PKG)

## install: install tartalo into $GOBIN (or $GOPATH/bin)
install:
	$(GO) install $(CMD_PKG)

## test: run the full test suite
test:
	$(GO) test $(PKG)

## cover: produce coverage.out and an HTML report at coverage.html
cover:
	$(GO) test -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

## bench: run benchmarks
bench:
	$(GO) test -bench=. -benchmem -run=^$$ $(PKG)

## fmt: format every .tt example and every .go file in place
fmt: build
	./$(BIN) fmt $(EXAMPLES)
	$(GO) fmt $(PKG)

## fmt-check: fail if any .tt example or .go file is not canonically formatted (CI)
fmt-check: build
	@./$(BIN) fmt -l $(EXAMPLES); \
	if [ $$? -ne 0 ]; then \
		echo "tartalo files need formatting (run: make fmt)"; exit 1; \
	fi
	@out=$$($(GO) fmt $(PKG)); \
	if [ -n "$$out" ]; then \
		echo "go files need formatting:"; echo "$$out"; exit 1; \
	fi

## vet: run go vet
vet:
	$(GO) vet $(PKG)

## lint: alias for vet + fmt-check (no external linter required)
lint: vet fmt-check

## check: full CI gate — vet, fmt-check, test
check: vet fmt-check test

## examples: build every example (sanity-check that they all compile)
examples: build
	@set -e; for f in $(EXAMPLES); do \
		echo "  build $$f"; \
		./$(BIN) check $$f; \
	done

## clean: remove the binary and coverage artifacts
clean:
	rm -f $(BIN) coverage.out coverage.html
	rm -f examples/*.sh

## help: list available targets
help:
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} /^## / {sub(/^## /, ""); split($$0, a, ":"); printf "  \033[36m%-12s\033[0m %s\n", a[1], a[2]}' $(MAKEFILE_LIST)
