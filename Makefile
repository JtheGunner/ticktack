VERSION ?= dev
LDFLAGS := -X github.com/JtheGunner/ticktack/internal/cli.Version=$(VERSION)

.PHONY: lint test build

lint:
	golangci-lint run ./...

test:
	CGO_ENABLED=0 go test ./...

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ticktack ./cmd/ticktack
