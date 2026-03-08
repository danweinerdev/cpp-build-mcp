BINARY := cpp-build-mcp
MODULE := github.com/danweinerdev/cpp-build-mcp
GOFLAGS := CGO_ENABLED=0

GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
PLATFORM := $(GOOS)-$(GOARCH)

PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64

.PHONY: build build-all $(PLATFORMS) test vet lint check clean install

build:
	$(GOFLAGS) go build -o bin/$(PLATFORM)/$(BINARY) .

build-all: $(PLATFORMS)

linux-amd64:
	$(GOFLAGS) GOOS=linux GOARCH=amd64 go build -o bin/$@/$(BINARY) .

linux-arm64:
	$(GOFLAGS) GOOS=linux GOARCH=arm64 go build -o bin/$@/$(BINARY) .

darwin-amd64:
	$(GOFLAGS) GOOS=darwin GOARCH=amd64 go build -o bin/$@/$(BINARY) .

darwin-arm64:
	$(GOFLAGS) GOOS=darwin GOARCH=arm64 go build -o bin/$@/$(BINARY) .

windows-amd64:
	$(GOFLAGS) GOOS=windows GOARCH=amd64 go build -o bin/$@/$(BINARY).exe .

windows-arm64:
	$(GOFLAGS) GOOS=windows GOARCH=arm64 go build -o bin/$@/$(BINARY).exe .

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	staticcheck ./...

check: vet lint test

clean:
	rm -rf bin/

install:
	go install .
