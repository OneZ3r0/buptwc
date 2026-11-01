.PHONY: build test test-unit test-integration check

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/buptwc ./cmd/buptwc

test:
	go test ./...

test-unit:
	go test ./internal/... ./cmd/...

test-integration:
	go test ./tests/...

check:
	go vet ./...
	go test -race ./...
