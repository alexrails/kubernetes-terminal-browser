.PHONY: build test race vet check cross install
GO := ./scripts/go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
# A release is a vX.Y.Z tag; between tags this reads like v0.1.0-3-gabc1234-dirty.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ktb ./cmd/ktb
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
check: test race vet
cross:
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/ktb-linux-amd64 ./cmd/ktb
	GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/ktb-linux-arm64 ./cmd/ktb
	GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/ktb-darwin-amd64 ./cmd/ktb
	GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/ktb-darwin-arm64 ./cmd/ktb
# macOS kills a signed binary overwritten in place, so the old file is removed
# first. kubectl and gcloud are only checked: they have their own versions and
# logins.
install: build
	mkdir -p "$(BINDIR)"
	rm -f "$(BINDIR)/ktb"
	install -m 755 bin/ktb "$(BINDIR)/ktb"
	@echo "Installed $(BINDIR)/ktb"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "Note: $(BINDIR) is not in PATH; add it in your shell profile" ;; esac
	@command -v kubectl >/dev/null || echo "Note: kubectl not found in PATH: https://kubernetes.io/docs/tasks/tools/"
	@command -v gcloud >/dev/null || echo "Note: gcloud not found in PATH (needed for GKE clusters): https://cloud.google.com/sdk/docs/install"
