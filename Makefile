BINARY := bin/syncwell
PKG := ./cmd/syncwell

.PHONY: all build run test vet fmt clean docker

all: vet test build

build:
	@mkdir -p bin
	go build -o $(BINARY) $(PKG)
	@echo "built $(BINARY)"

run:
	go run $(PKG)

test:
	go test ./... -race -count=1

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin

docker:
	docker build -t syncwell:latest .
