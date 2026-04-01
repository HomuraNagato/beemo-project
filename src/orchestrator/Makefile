.PHONY: build run test fmt vet

BINARY=orchestrator

build:
	go build -o ./bin/$(BINARY) .

run:
	go run .

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
