BINARY := bin/syncwell
PKG := ./cmd/syncwell
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X syncwell/internal/version.Version=$(VERSION) -X syncwell/internal/version.Commit=$(COMMIT) -X syncwell/internal/version.Date=$(BUILD_DATE)

.PHONY: all build run loadtest-bin test test-js sdk-install sdk-build sdk-typecheck docs-install docs-build docs-dev cover-check browser-e2e e2e persist restore-drill gc chaos bench vet fmt fmt-check clean docker container-smoke test-multi-client test-python help

help:
	@echo "Syncwell make targets:"
	@echo "  all              fmt-check + vet + test + cover-check + JS tests + e2e + persist + restore-drill + gc + multi-client + chaos + build"
	@echo "  build            compile bin/syncwell"
	@echo "  run              go run the server"
	@echo "  test             go test -race ./..."
	@echo "  cover-check      per-package coverage floors"
	@echo "  test-js          SDK unit tests (node --test)"
	@echo "  sdk-build        ESM+CJS+IIFE bundles into sdk/dist"
	@echo "  sdk-typecheck    tsc over the SDK declarations"
	@echo "  test-python      Python client E2E against a live server"
	@echo "  e2e              offline -> reconnect convergence"
	@echo "  persist          kill -9 durability drill"
	@echo "  restore-drill    backup -> restore round-trip"
	@echo "  gc               tombstone GC check"
	@echo "  chaos            partition/reorder convergence"
	@echo "  bench            latency/throughput benchmark"
	@echo "  loadtest-bin     build cmd/loadtest into bin/"
	@echo "  browser-e2e      firefox + geckodriver demo walk"
	@echo "  docs-build       VitePress production build"
	@echo "  docker           container image build"
	@echo "  container-smoke  run container + curl /healthz"
	@echo "  vet / fmt / fmt-check / clean"

all: fmt-check vet test cover-check test-js e2e persist restore-drill gc test-multi-client chaos build

build:
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "built $(BINARY)"

# Build the load/soak harness (not part of `all`).
loadtest-bin:
	@mkdir -p bin
	go build -trimpath -o bin/loadtest ./cmd/loadtest
	@echo "built bin/loadtest"
run:
	go run $(PKG)

test:
	go test ./... -race -count=1

# Per-package statement-coverage floors for the core packages.
cover-check:
	@./scripts/cover-check.sh

# JS CRDT unit tests (mirror of the Go laws).
test-js:
	cd sdk && node --test

# SDK packaging (requires network for npm ci; not part of `all`).
sdk-install:
	cd sdk && npm ci

sdk-typecheck: sdk-install
	cd sdk && npm run typecheck

sdk-build: sdk-install
	cd sdk && npm run build

# Documentation site (VitePress). Kept out of `all` because it needs npm ci.
docs-install:
	cd website && npm ci

docs-build: docs-install
	cd website && npm run build

docs-dev: docs-install
	cd website && npm run dev

# Browser E2E / accessibility smoke for the kanban demo.
browser-e2e:
	node test/browser_kanban_e2e.mjs

# End-to-end offline-convergence proof against the real server binary.
e2e: build
	node test/offline_e2e.mjs

# End-to-end multi-client convergence proof.
test-multi-client: build
	node test/multi_client_e2e.mjs

# Durability proof: write, SIGKILL the server, restart, verify state survived.
persist: build
	node test/persist_e2e.mjs

# Backup/restore proof: back up, reject a corrupt backup, wipe data, restore.
restore-drill: build
	node test/restore_drill.mjs

# Polyglot proof: dependency-free Python client against the real server.
test-python: build
	python3 -m unittest python/test_client_e2e.py -v

# Tombstone-GC proof: churn deletes, verify tombstones are collected.
gc: build
	node test/gc_e2e.mjs

# Adversarial convergence: many replicas, partitions, reorder, duplicates.
chaos:
	go test ./internal/crdt/ -run TestChaosConvergence -v

# Latency/throughput benchmark; writes docs/BENCHMARKS.md.
bench: build
	node test/bench.mjs

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
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t syncwell:latest .

container-smoke:
	IMAGE=syncwell:ci VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(BUILD_DATE) node test/container_smoke.mjs
