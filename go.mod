module github.com/leandroluk/goutbox-relay

// Minimum Go version required to build this module.
go 1.23

// toolchain pins the exact Go toolchain used during development,
// ensuring reproducible builds across different environments.
toolchain go1.23.0

require (
	// PostgreSQL driver — pure Go, no CGO required (matches CGO_ENABLED=0 build).
	github.com/lib/pq v1.12.1

	// Redis client with full support for Redis Streams (XADD, XREAD, etc.).
	github.com/redis/go-redis/v9 v9.18.0
)

require (
	// Indirect dependencies pulled in by go-redis/v9.
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)

require go.uber.org/atomic v1.11.0 // indirect
