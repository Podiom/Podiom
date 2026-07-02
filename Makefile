# Podiom build. The web UI is built first (vite -> web/dist/) and embedded into
# podiomd via go:embed, so `make build` always produces a single self-contained
# binary with the current SPA baked in.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -X github.com/Podiom/Podiom/internal/buildinfo.Version=$(VERSION) \
           -X github.com/Podiom/Podiom/internal/buildinfo.Commit=$(COMMIT)

GO      ?= go
BINDIR  ?= bin

.PHONY: all build web go-build podiom podiomd check test tidy clean cross package help

all: build ## Build the web UI and both binaries (default)

build: web go-build ## Build web UI + binaries

web: ## Build the embedded SPA (npm install + vite build)
	cd web && npm install && npm run build

go-build: podiomd podiom ## Build both Go binaries (assumes web already built)

podiomd: ## Build the daemon
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/podiomd ./cmd/podiomd

podiom: ## Build the CLI client
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/podiom ./cmd/podiom

check: ## go vet + svelte-check
	$(GO) vet ./...
	cd web && npm run check

test: ## Run Go tests
	$(GO) test ./...

tidy: ## Tidy go modules
	$(GO) mod tidy

# Cross-compile both binaries for the three supported OSes. Requires the web UI
# to be built first (run `make web`); the embed is OS-independent.
cross: ## Cross-compile podiomd/podiom for linux, darwin, windows (amd64+arm64)
	@set -e; for os in linux darwin windows; do \
	  for arch in amd64 arm64; do \
	    ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	    echo "building $$os/$$arch"; \
	    GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$os-$$arch/podiomd$$ext ./cmd/podiomd; \
	    GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$os-$$arch/podiom$$ext ./cmd/podiom; \
	  done; \
	done

package: web cross ## Archive release binaries and emit SHA256SUMS in dist/
	@set -e; \
	rm -rf dist; mkdir -p dist; \
	for os in linux darwin windows; do \
	  for arch in amd64 arm64; do \
	    name="podiom_$(VERSION)_$${os}_$${arch}"; \
	    src="$(BINDIR)/$${os}-$${arch}"; \
	    if [ "$$os" = "windows" ]; then \
	      (cd "$$src" && zip -q "../../dist/$${name}.zip" podiom.exe podiomd.exe); \
	    else \
	      tar -C "$$src" -czf "dist/$${name}.tar.gz" podiom podiomd; \
	    fi; \
	  done; \
	done; \
	(cd dist && { command -v sha256sum >/dev/null 2>&1 && sha256sum podiom_* || shasum -a 256 podiom_*; } > SHA256SUMS)

clean: ## Remove build artifacts
	rm -rf $(BINDIR) dist web/dist/assets

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
