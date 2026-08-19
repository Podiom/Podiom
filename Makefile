# Podiom build. The web UI is built first (vite -> web/dist/) and embedded into
# podiomd via go:embed, so `make build` always produces a single self-contained
# binary with the current SPA baked in.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -X github.com/Podiom/Podiom/internal/buildinfo.Version=$(VERSION) \
           -X github.com/Podiom/Podiom/internal/buildinfo.Commit=$(COMMIT)

GO      ?= go
BINDIR  ?= bin

# Which platform pairs `cross` builds. Overridable so callers that only need one
# target (CI's add-on smoke job) reuse this file's LDFLAGS instead of restating
# them and drifting out of sync.
CROSS_OS   ?= linux darwin windows
CROSS_ARCH ?= amd64 arm64

HA_IMAGE ?= ghcr.io/podiom/podiom-ha
HA_TAG   ?= dev

.PHONY: all build web go-build podiom podiomd check test tidy clean cross package ha-image help

all: build ## Build the web UI and both binaries (default)

build: web go-build ## Build web UI + binaries

# Installs from the repo root: web/ is an npm workspace of the root package
# (which also owns the Capacitor shell), so there is one lockfile for both.
web: ## Build the embedded SPA (npm install + vite build)
	npm install
	npm run build -w web

go-build: podiomd podiom ## Build both Go binaries (assumes web already built)

podiomd: ## Build the daemon
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/podiomd ./cmd/podiomd

podiom: ## Build the CLI client
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/podiom ./cmd/podiom

check: ## go vet + svelte-check
	$(GO) vet ./...
	npm run check -w web

test: ## Run Go tests
	$(GO) test ./...

tidy: ## Tidy go modules
	$(GO) mod tidy

# Cross-compile both binaries for the three supported OSes. Requires the web UI
# to be built first (run `make web`); the embed is OS-independent.
# One target pair per background subshell, then wait and collect. `set -e` does
# not reach into a background subshell, hence the explicit && and the exit-code
# sweep — without both, a failed cross build would pass silently.
cross: ## Cross-compile podiomd/podiom for linux, darwin, windows (amd64+arm64)
	@set -e; pids=""; \
	for os in $(CROSS_OS); do \
	  for arch in $(CROSS_ARCH); do \
	    ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	    echo "building $$os/$$arch"; \
	    ( GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$os-$$arch/podiomd$$ext ./cmd/podiomd && \
	      GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$os-$$arch/podiom$$ext ./cmd/podiom ) & \
	    pids="$$pids $$!"; \
	  done; \
	done; \
	rc=0; for p in $$pids; do wait $$p || rc=1; done; exit $$rc

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

# Build the Home Assistant add-on image for the host arch and load it into the
# local docker daemon. Pins come from ha/versions.env (every key is passed as a
# build-arg); binaries come from bin/linux-<arch>/ via the `cross` dependency.
ha-image: web cross ## Build the HA add-on image for the host arch (docker buildx --load)
	@set -e; set -a; . ha/versions.env; set +a; \
	arch=$$(uname -m); \
	case "$$arch" in \
	  x86_64) arch=amd64 ;; \
	  arm64|aarch64) arch=arm64 ;; \
	  *) echo "unsupported host arch: $$arch" >&2; exit 1 ;; \
	esac; \
	build_args=$$(sed -n 's/^\([A-Z_][A-Z0-9_]*\)=.*/--build-arg \1/p' ha/versions.env); \
	echo "building $(HA_IMAGE):$(HA_TAG) for linux/$$arch"; \
	docker buildx build --load \
	  --platform "linux/$$arch" \
	  -f ha/Dockerfile \
	  $$build_args \
	  --build-arg PODIOM_VERSION="$(VERSION)" \
	  -t "$(HA_IMAGE):$(HA_TAG)" .

clean: ## Remove build artifacts
	rm -rf $(BINDIR) dist web/dist/assets

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
