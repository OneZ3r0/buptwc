.PHONY: build test check

build:
	CGO_ENABLED=0 go build -trimpath -o bin/buptwc ./cmd/buptwc

test:
	go test ./...

check:
	go vet ./...
	go test -race ./...
