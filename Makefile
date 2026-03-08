BINARY := cpp-build-mcp

.PHONY: build test vet lint check clean install

build:
	go build -o $(BINARY) .

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	staticcheck ./...

check: vet lint test

clean:
	rm -f $(BINARY)

install:
	go install .
