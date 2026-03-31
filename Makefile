# ============================================================
# goutbox-relay — Build Automation
# ============================================================

.PHONY: lint build test test-unit test-e2e deps deps-down coverage docker-build run clean

BINARY_NAME = goutbox-relay
DOCKER_IMAGE = leandroluk/goutbox-relay

# Load .env from the project root if it exists.
# Variables defined there are exported to all child processes (go test, etc.)
# so POSTGRES_URL and REDIS_URL are available without setting them manually.
-include .env
export

lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./...

build:
	go run ./_tools/build -out=bin/$(BINARY_NAME) -pkg=./cmd/relay

test-unit:
	go test -v ./internal/...

test-e2e:
	go test -v -timeout 120s ./test/e2e/...

test: test-unit test-e2e

# Generate coverage report and badge.
# go test runs here (inside Make) so .env vars are already exported.
# _tools/badge only handles HTML report + SVG generation.
#
#   make coverage        — runs tests, generates report, updates badge
#   make coverage OPEN=1 — also opens the HTML report in the browser
coverage:
	go run ./_tools/mkdir -path=.public
	go test -coverprofile=.public/coverage.out -covermode=atomic ./internal/...
	go tool cover -html=.public/coverage.out -o .public/coverage.html
	go run ./_tools/badge -in=.public/coverage.out -out=.public/coverage.svg
	go run ./_tools/open -path=.public/coverage.html -enabled=$(OPEN)

compose-up:
	docker compose up -d postgres redis

compose-down:
	docker compose down --remove-orphans

docker-build:
	docker build -t $(DOCKER_IMAGE):latest -f deployments/Dockerfile .

run:
	go run ./cmd/relay

clean:
	go run ./_tools/rm -path=bin/
	go run ./_tools/rm -path=.public/