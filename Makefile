# dk build and release.
#
# No goreleaser: this is a stdlib-only Go binary with no cgo, so cross-compiling
# is four `go build` invocations and adding a release tool would be a dependency
# with more moving parts than the thing it builds.

BIN     := dk
MODULE  := github.com/mcavage/dk-cli
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DIST    := dist

PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

# -s -w strips the symbol table and DWARF; this is a CLI, not something anyone
# attaches a debugger to, and it cuts the download roughly in half.
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test check fmtcheck ignorecheck dist clean install checksums

build:
	go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/dk

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/dk

test:
	go test ./...

# What CI runs and what must pass before a tag.
check: fmtcheck ignorecheck
	go vet ./...
	go test ./...

# POSIX sh, because make runs /bin/sh and the `(! read)` idiom is a bashism
# that fails under dash with "read: arg count".
fmtcheck:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# A gitignored .go file is essentially never intentional, and it fails in the
# worst possible way: `git add -A` succeeds, local builds pass because the file
# is on disk, and the commit ships a feature it does not contain. This repo
# already lost cmd/dk/orders.go to an unanchored "dk" pattern that matched the
# cmd/dk directory.
ignorecheck:
	@out="$$(git ls-files --others --ignored --exclude-standard | grep '\.go$$' || true)"; \
	if [ -n "$$out" ]; then \
	  echo "these .go files are gitignored and would not be committed:"; \
	  echo "$$out"; exit 1; fi

dist: clean
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  echo "building $(BIN)_$${os}_$${arch}"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	    go build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $(DIST)/$(BIN)_$${os}_$${arch} ./cmd/dk || exit 1; \
	done
	@$(MAKE) --no-print-directory checksums

# Reproducible-ish: sorted, bare filenames, so install.sh and the Homebrew
# formula can both read the same file.
checksums:
	@cd $(DIST) && \
	  if command -v sha256sum >/dev/null 2>&1; then sha256sum $(BIN)_* > checksums.txt; \
	  else shasum -a 256 $(BIN)_* > checksums.txt; fi && \
	  sed -i.bak 's| \*| |' checksums.txt && rm -f checksums.txt.bak && \
	  cat checksums.txt

clean:
	rm -rf $(DIST) $(BIN)
