.PHONY: all build build-runtime build-hub build-ui build-site clean test test-ui test-all lint dev-hub dev-runtime dev-ui dev-site release-snapshot

GO := go
GOFLAGS := -trimpath
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

BIN_DIR := bin

all: build

build: build-runtime build-hub build-ui

build-runtime:
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/amurg-runtime ./runtime/cmd/amurg-runtime

build-hub:
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/amurg-hub ./hub/cmd/amurg-hub

build-ui:
	cd ui && npm install && npm run build

build-site:
	cd site && npm install && npm run build

clean:
	rm -rf $(BIN_DIR) ui/dist ui/node_modules site/dist site/node_modules

test:
	$(GO) test ./...

test-cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

test-ui:
	cd ui && npx vitest run

test-all: test test-ui

lint:
	golangci-lint run ./...

fmt:
	$(GO) fmt ./...
	cd ui && npx prettier --write src/

dev-hub:
	$(GO) run ./hub/cmd/amurg-hub run --config hub/deploy/config.local.json

dev-runtime:
	$(GO) run ./runtime/cmd/amurg-runtime run --config runtime/deploy/config.local.json

dev-ui:
	cd ui && npm run dev

dev-site:
	cd site && npm run dev

release-snapshot:
	goreleaser release --snapshot --clean

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down
