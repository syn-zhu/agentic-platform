.PHONY: build test lint

build:
	go build -o bin/executor ./cmd/executor

test:
	go test -race -count=1 ./...

lint:
	go vet ./...
