BINARY := bin/syncwell
PKG := ./cmd/syncwell

.PHONY: all build run test test-js e2e persist vet fmt clean docker

all: vet test test-js e2e persist build

build:
	@mkdir -p bin
	go build -o $(BINARY) $(PKG)
	@echo "built $(BINARY)"

run:
	go run $(PKG)

test:
	go test ./... -race -count=1

# JS CRDT unit tests (mirror of the Go laws).
test-js:
	cd sdk && node --test

# End-to-end offline-convergence proof against the real server binary.
e2e: build
	node test/offline_e2e.mjs

# Durability proof: write, SIGKILL the server, restart, verify state survived.
persist: build
	node test/persist_e2e.mjs

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin

docker:
	docker build -t syncwell:latest .
