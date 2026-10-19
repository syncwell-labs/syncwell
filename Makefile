BINARY := bin/syncwell
PKG := ./cmd/syncwell
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X syncwell/internal/version.Version=$(VERSION) -X syncwell/internal/version.Commit=$(COMMIT) -X syncwell/internal/version.Date=$(BUILD_DATE)

.PHONY: all build run test test-js browser-e2e e2e persist gc chaos bench vet fmt fmt-check clean docker container-smoke test-multi-client

all: fmt-check vet test test-js e2e persist gc test-multi-client chaos build

build:
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "built $(BINARY)"
run:
	go run $(PKG)

test:
	go test ./... -race -count=1

# JS CRDT unit tests (mirror of the Go laws).
test-js:
	cd sdk && node --test

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
