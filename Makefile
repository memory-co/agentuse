.PHONY: build test vet fmt

build:
	go build -o bin/agentuse ./cmd/agentuse

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
