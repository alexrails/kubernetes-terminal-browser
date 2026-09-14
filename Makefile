.PHONY: build test race vet check cross install
GO := ./scripts/go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
build:
	$(GO) build -trimpath -o bin/ktb ./cmd/ktb
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
check: test race vet
cross:
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -o dist/ktb-linux-amd64 ./cmd/ktb
	GOOS=linux GOARCH=arm64 $(GO) build -trimpath -o dist/ktb-linux-arm64 ./cmd/ktb
	GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -o dist/ktb-darwin-amd64 ./cmd/ktb
	GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -o dist/ktb-darwin-arm64 ./cmd/ktb
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
