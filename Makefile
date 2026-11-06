# ytplay: build, test and install.
#
#   make              build bin/ytplay
#   make test         run the tests
#   make install      install to ~/.local/bin (PREFIX=/usr/local for system-wide)
#   make help         list all targets

BINARY  := ytplay
PKG     := ./cmd/ytplay
OUT     := bin/$(BINARY)
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
GOFLAGS ?= -trimpath
LDFLAGS ?= -s -w

.DEFAULT_GOAL := build
.PHONY: build test race vet fmt check install uninstall clean help

build: ## Build bin/ytplay
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(OUT) $(PKG)

test: ## Run the tests
	go test ./...

race: ## Run the tests with the race detector (slower)
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format the code with gofmt
	gofmt -w .

check: ## Everything CI would check: formatting, vet, tests
	@test -z "$$(gofmt -l .)" || { echo "not formatted (run make fmt):"; gofmt -l .; exit 1; }
	$(MAKE) vet test

install: build ## Install the binary (default ~/.local/bin; PREFIX=... to change)
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 755 $(OUT) "$(DESTDIR)$(BINDIR)/$(BINARY)"
	@echo "installed $(DESTDIR)$(BINDIR)/$(BINARY)"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "note: $(BINDIR) is not in your PATH";; esac

uninstall: ## Remove the installed binary
	rm -f "$(DESTDIR)$(BINDIR)/$(BINARY)"

clean: ## Remove build output
	rm -rf bin

help: ## Show this help
	@grep -E '^[a-z]+:.*## ' $(MAKEFILE_LIST) | sed -E 's/^([a-z]+):.*## /\1\t/' | expand -t10
