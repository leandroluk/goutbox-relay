// internal/db/postgres.go
//
// Package db handles all PostgreSQL connectivity and schema lifecycle for
// the relay. It intentionally keeps a thin surface area: one connection
// factory and one idempotent schema-setup function.
package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/leandroluk/goutbox-relay/internal/config"
	_ "github.com/lib/pq" // registers the "postgres" driver with database/sql
)

// NewPostgres opens a connection pool to PostgreSQL and verifies it with a
// Ping. Connection-pool knobs are tuned for the relay's single-goroutine,
// batch-oriented access pattern.
func NewPostgres(url string) (*sql.DB, error) {
	return newPostgresWithDriver("postgres", url)
}

func newPostgresWithDriver(driver, url string) (*sql.DB, error) {
	db, err := sql.Open(driver, url)
	if err != nil {
		return nil, fmt.Errorf("db: open connection: %w", err)
	}

	// The relay runs a single polling goroutine. More than a handful of
	// connections would be wasteful and could exhaust the server's limit.
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)

	// Recycle connections periodically to avoid hitting server-side
	// idle-connection timeouts (common in cloud-managed databases).
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return db, nil
}


// SetupSchema creates the outbox and config tables when they do not already
// exist, then seeds the initial cursor row. Every statement uses IF NOT
// EXISTS / ON CONFLICT so the function is safe to call on every startup —
// no manual migration step is required for the relay's own bookkeeping tables.
//
// Note: this function deliberately does NOT create the application's outbox
// table in production deployments — that table must be owned by the
// application. It is created here only to simplify local development and
// integration testing via Docker Compose.
func SetupSchema(db *sql.DB, cfg config.Config) error {
	statements := []struct {
		desc string
		sql  string
	}{
		{
			desc: "create outbox table",
			sql: fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS %s (
					id         BIGSERIAL    PRIMARY KEY,
					topic      TEXT         NOT NULL,
					payload    JSONB        NOT NULL,
					created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
				)`, cfg.OutboxTable),
		},
		{
			// An index on (id) already exists via PRIMARY KEY.
			// Adding one on (topic) speeds up consumer-side queries
			// when the application filters events by stream name.
			desc: "create index on outbox topic",
			sql: fmt.Sprintf(`
				CREATE INDEX IF NOT EXISTS idx_%s_topic ON %s (topic)`,
				cfg.OutboxTable, cfg.OutboxTable),
		},
		{
			desc: "create relay config table",
			sql: fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS %s (
					key        TEXT    PRIMARY KEY,
					last_id    BIGINT  NOT NULL DEFAULT 0,
					updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
				)`, cfg.ConfigTable),
		},
		{
			// Seed the cursor row exactly once. If the relay is redeployed
			// against an existing database the current cursor is preserved.
			desc: "seed outbox cursor",
			sql: fmt.Sprintf(`
				INSERT INTO %s (key, last_id)
				VALUES ('outbox_cursor', 0)
				ON CONFLICT (key) DO NOTHING`,
				cfg.ConfigTable),
		},
	}

	for _, s := range statements {
		if _, err := db.Exec(s.sql); err != nil {
			return fmt.Errorf("db: setup schema (%s): %w", s.desc, err)
		}
	}

	return nil
}
