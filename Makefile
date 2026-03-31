# ============================================================
# goutbox-relay — Build Automation
# ============================================================

.PHONY: build test lint docker-build run clean

# Binary and image names align with the repository/module name.
BINARY_NAME = goutbox-relay
DOCKER_IMAGE = leandroluk/goutbox-relay

# Inject version metadata at build time via linker flags.
# -s -w strips debug symbols, reducing binary size significantly.
BUILD_FLAGS = -ldflags="-s -w" -trimpath

build:
	CGO_ENABLED=0 GOOS=linux go build $(BUILD_FLAGS) -o bin/$(BINARY_NAME) ./cmd/relay

test:
	# Run all tests with race detector enabled to catch concurrency issues.
	go test -race -v ./...

lint:
	# Requires golangci-lint: https://golangci-lint.run/usage/install/
	golangci-lint run ./...

docker-build:
	docker build -t $(DOCKER_IMAGE):latest -f deployments/Dockerfile .

run:
	go run ./cmd/relay

clean:
	# Remove all build artefacts.
	rm -rf bin/