BINARY := bin/syncwell
PKG := ./cmd/syncwell
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X syncwell/internal/version.Version=$(VERSION) -X syncwell/internal/version.Commit=$(COMMIT) -X syncwell/internal/version.Date=$(BUILD_DATE)

.PHONY: all build run loadtest-bin test test-js sdk-install sdk-build sdk-typecheck docs-install docs-build docs-dev cover-check browser-e2e browser-e2e-chromium browser-e2e-firefox a11y e2e persist restore-drill gc chaos bench vuln vet fmt fmt-check clean docker container-smoke test-multi-client test-python redis-e2e workflows release-dry-run redis-drill soak soak-quick help

help:
	@echo "Syncwell make targets:"
	@echo "  all              workflows + fmt-check + vet + test + cover-check + JS tests + e2e + persist + restore-drill + gc + multi-client + chaos + build"
	@echo "  build            compile bin/syncwell"
	@echo "  run              go run the server (loopback demo at http://localhost:8080; ADDR=... to override)"
	@echo "  test             go test -race ./..."
	@echo "  cover-check      per-package coverage floors"
	@echo "  test-js          SDK unit tests (node --test)"
	@echo "  sdk-build        ESM+CJS+IIFE bundles into clients/javascript/dist"
	@echo "  sdk-typecheck    tsc over the SDK declarations"
	@echo "  test-python      Python client E2E against a live server"
	@echo "  e2e              offline -> reconnect convergence"
	@echo "  persist          kill -9 durability drill"
	@echo "  restore-drill    backup -> restore round-trip"
	@echo "  gc               tombstone GC check"
	@echo "  chaos            partition/reorder convergence"
	@echo "  bench            latency/throughput benchmark"
	@echo "  vuln             run govulncheck ./... when installed"
	@echo "  loadtest-bin     build cmd/loadtest into bin/"
	@echo "  workflows        validate workflow YAML, action pins, and release config (offline)"
	@echo "  release-dry-run  cross-build release matrix + checksums + optional SBOM (no publish)"
	@echo "  redis-drill      local 3-node Redis SIGKILL/re-home + relay rollback evidence"
	@echo "  soak             local 2000-client bench + 10m loadtest artifacts (SOAK_PORT=...)"
	@echo "  soak-quick       short soak smoke (200 clients, 30s) to validate the harness"
	@echo "  browser-e2e      kanban + Chromium + Firefox browser suites"
	@echo "  browser-e2e-chromium  Brave/Chromium websocket auth + collab + reconnect"
	@echo "  browser-e2e-firefox   Firefox WebDriver BiDi auth + collab + reconnect"
	@echo "  a11y             axe-core WCAG scan: demos + built docs (chromedriver)"
	@echo "  docs-build       VitePress production build"
	@echo "  docker           container image build"
	@echo "  container-smoke  run container + curl /healthz"
	@echo "  vet / fmt / fmt-check / clean"

all: workflows fmt-check vet test cover-check test-js e2e persist restore-drill gc test-multi-client chaos build

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "built $(BINARY)"

# Build the load/soak harness (not part of `all`).
loadtest-bin:
	@mkdir -p bin
	go build -trimpath -o bin/loadtest ./cmd/loadtest
	@echo "built bin/loadtest"
ADDR ?= 127.0.0.1:8080
run:
	go run $(PKG) -addr $(ADDR)

test:
	go test ./... -race -count=1

# Per-package statement-coverage floors for the core packages.
cover-check:
	@./scripts/cover-check.sh

# JS CRDT unit tests (mirror of the Go laws).
test-js:
	cd clients/javascript && node --test

# SDK packaging (requires network for npm ci; not part of `all`).
sdk-install:
	cd clients/javascript && npm ci

sdk-typecheck: sdk-install
	cd clients/javascript && npm run typecheck

sdk-build: sdk-install
	cd clients/javascript && npm run build

# Documentation site (VitePress). Kept out of `all` because it needs npm ci.
docs-install:
	cd website && npm ci

docs-build: docs-install
	cd website && npm run build

docs-dev: docs-install
	cd website && npm run dev

# Browser E2E / accessibility smoke for the kanban demo.
browser-e2e: build
	node tests/e2e/browser_kanban_e2e.mjs
	node --test tests/e2e/browser_chromium_e2e.mjs
	node --test tests/e2e/browser_firefox_e2e.mjs

# Chromium/Brave E2E: websocket auth, collaboration, reconnect, console/network.
browser-e2e-chromium:
	node --test tests/e2e/browser_chromium_e2e.mjs

# Firefox E2E through WebDriver BiDi: auth, collaboration, reconnect, logs.
browser-e2e-firefox: build
	node --test tests/e2e/browser_firefox_e2e.mjs

# axe-core WCAG 2.1 A/AA scan over the demo surfaces and the built docs site.
a11y: build docs-build
	python3 tests/a11y/axe_a11y.py

# End-to-end offline-convergence proof against the real server binary.
e2e: build
	node tests/e2e/offline_e2e.mjs

# End-to-end multi-client convergence proof.
test-multi-client: build
	node tests/e2e/multi_client_e2e.mjs

# Durability proof: write, SIGKILL the server, restart, verify state survived.
persist: build
	node tests/e2e/persist_e2e.mjs

# Backup/restore proof: back up, reject a corrupt backup, wipe data, restore.
restore-drill: build
	node tests/e2e/restore_drill.mjs

# Polyglot proof: dependency-free Python client against the real server.
test-python: build
	python3 -m unittest clients/python/test_client_e2e.py -v

redis-e2e: build
	NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/redis_rehome.mjs

# Offline release-config gate: YAML parse, SHA-pinned actions, exact Go toolchain.
workflows:
	@./scripts/check-workflows.sh

# Local release rehearsal. Never publishes or signs; see RELEASE_CHECKLIST.md.
release-dry-run:
	@./scripts/release-dry-run.sh

# Local Redis three-node SIGKILL/re-home drill with RPO/RTO artifacts.
redis-drill:
	@./scripts/redis-recovery-drill.sh

# Local counterpart of the nightly soak job (bench + long loadtest artifacts).
soak:
	@./scripts/soak.sh

# Short soak smoke so the harness can be validated without a 20-minute run.
soak-quick:
	@CLIENTS=200 MAX_CLIENTS=256 DURATION_MS=30000 LOADTEST_CLIENTS=200 ROOMS=10 LOADTEST_DURATION=30s ./scripts/soak.sh

# Tombstone-GC proof: churn deletes, verify tombstones are collected.
gc: build
	node tests/e2e/gc_e2e.mjs

# Adversarial convergence: many replicas, partitions, reorder, duplicates.
chaos:
	go test ./internal/crdt/ -run TestChaosConvergence -v

# Latency/throughput benchmark; writes docs/product/BENCHMARKS.md.
bench: build
	node tests/bench/bench.mjs

vuln:
	@vulncheck_bin="$$(go env GOPATH)/bin/govulncheck"; \
	if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	elif [ -x "$$vulncheck_bin" ]; then \
		"$$vulncheck_bin" ./...; \
	else \
		echo "govulncheck is not installed. Install it with: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
	fi

vet:
	go vet ./...

fmt:
	gofmt -w .

# Fail if any Go file is not gofmt-clean (used by `all` and CI).
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; echo "run 'make fmt'"; exit 1; fi

clean:
	rm -rf bin

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t syncwell:$(VERSION) -t syncwell:latest .

container-smoke:
	IMAGE=syncwell:ci VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(BUILD_DATE) node tests/e2e/container_smoke.mjs
