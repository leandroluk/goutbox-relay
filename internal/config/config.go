// internal/config/config.go
//
// Package config centralises all runtime configuration for the relay.
// Every value is sourced from environment variables so the binary can be
// deployed without recompilation (12-factor app principle).
package config

import (
	"log"
	"os"
	"strconv"
	"time"
)

// Config holds the complete, validated configuration for the relay.
// All fields are immutable after Load() returns.
type Config struct {
	// PostgresURL is the libpq-compatible connection string used to reach
	// the source database that contains the outbox table.
	PostgresURL string

	// RedisURL is the Redis connection string (redis://[user:pass@]host:port[/db]).
	RedisURL string

	// OutboxTable is the name of the table that stores outgoing domain events.
	OutboxTable string

	// ConfigTable is the name of the table used to persist the relay cursor
	// (last processed outbox ID) across restarts.
	ConfigTable string

	// BatchSize controls the maximum number of events fetched and forwarded
	// in a single polling cycle. Larger values increase throughput at the
	// cost of longer transactions.
	BatchSize int

	// PollInterval is the duration the relay sleeps between polling cycles
	// when the previous batch was smaller than BatchSize (i.e. the outbox
	// is caught up).
	PollInterval time.Duration

	// Retention is the maximum age of messages kept in Redis Streams.
	// Older entries are trimmed automatically via XADD MINID.
	Retention time.Duration
}

// Load reads configuration from environment variables and returns a populated
// Config. It calls log.Fatal if a required variable is missing.
func Load() Config {
	return Config{
		PostgresURL:  requireEnv("POSTGRES_URL"),
		RedisURL:     requireEnv("REDIS_URL"),
		OutboxTable:  getEnv("TABLE_OUTBOX", "outbox"),
		ConfigTable:  getEnv("TABLE_CONFIG", "relay_config"),
		BatchSize:    getEnvInt("BATCH_SIZE", 500),
		PollInterval: time.Duration(getEnvInt("POLL_INTERVAL", 10)) * time.Second,
		Retention:    time.Duration(getEnvInt("RETENTION_DAYS", 7)) * 24 * time.Hour,
	}
}

// requireEnv returns the value of key or terminates the process if the
// variable is not set. Connection strings have no safe default, so
// failing fast here prevents silent misconfiguration in production.
func requireEnv(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		log.Fatalf("config: required environment variable %q is not set", key)
	}
	return v
}

// getEnv returns the value of key, or fallback when the variable is absent.
func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// getEnvInt parses key as a base-10 integer.
// If the variable is absent or cannot be parsed, fallback is returned
// and no error is surfaced — callers rely on sane defaults in that case.
func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			return i
		}
	}
	return fallback
}
