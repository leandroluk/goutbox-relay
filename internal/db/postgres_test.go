// internal/db/postgres_test.go
package db

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/leandroluk/goutbox-relay/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postgresURL returns the test database URL or skips the test when the
// environment variable is absent. This keeps `go test ./...` green in
// environments without Postgres (e.g. pure unit test CI jobs).
func postgresURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set — skipping Postgres-dependent test")
	}
	return url
}

// openTestDB opens a connection and registers cleanup automatically.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := NewPostgres(postgresURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// testConfig returns a Config whose table names are unique to the test,
// preventing collisions when tests run in parallel.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	// Postgres lowercases unquoted identifiers, so the table name must be
	// lowercase. Non-alphanumeric characters are replaced with underscores.
	safe := func(s string) string {
		s = strings.ToLower(s)
		out := []byte(s)
		for i, c := range out {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				out[i] = '_'
			}
		}
		return string(out)
	}
	name := safe(t.Name())
	return config.Config{
		OutboxTable: "test_outbox_" + name,
		ConfigTable: "test_config_" + name,
	}
}

// dropTables removes the tables created during a test.
func dropTables(t *testing.T, db *sql.DB, cfg config.Config) {
	t.Helper()
	if _, err := db.Exec("DROP TABLE IF EXISTS " + cfg.OutboxTable); err != nil {
		t.Logf("cleanup: drop %s: %v", cfg.OutboxTable, err)
	}
	if _, err := db.Exec("DROP TABLE IF EXISTS " + cfg.ConfigTable); err != nil {
		t.Logf("cleanup: drop %s: %v", cfg.ConfigTable, err)
	}
}

// --- NewPostgres ------------------------------------------------------------

func TestNewPostgres_FailsOnInvalidURL(t *testing.T) {
	_, err := NewPostgres("not-a-valid-url")
	assert.Error(t, err, "invalid URL should return an error")
}

func TestNewPostgres_FailsOnUnreachableHost(t *testing.T) {
	// Port 19999 is almost certainly not listening; connect_timeout=1 keeps
	// the test fast.
	url := "postgres://postgres:postgres@127.0.0.1:19999/postgres?sslmode=disable&connect_timeout=1"
	_, err := NewPostgres(url)
	assert.Error(t, err, "unreachable host should return an error")
}

func TestNewPostgres_SucceedsAndPings(t *testing.T) {
	db := openTestDB(t)
	assert.NoError(t, db.Ping())
}

func TestNewPostgres_SetsConnectionPoolLimits(t *testing.T) {
	db := openTestDB(t)
	// MaxOpenConnections is set to 5 inside NewPostgres.
	assert.Equal(t, 5, db.Stats().MaxOpenConnections)
}

// --- SetupSchema ------------------------------------------------------------

func TestSetupSchema_IsIdempotent(t *testing.T) {
	db := openTestDB(t)
	cfg := testConfig(t)
	t.Cleanup(func() { dropTables(t, db, cfg) })

	// Calling SetupSchema twice must succeed — all DDL uses IF NOT EXISTS /
	// ON CONFLICT DO NOTHING.
	require.NoError(t, SetupSchema(db, cfg), "first call")
	require.NoError(t, SetupSchema(db, cfg), "second call")
}

func TestSetupSchema_CreatesOutboxTable(t *testing.T) {
	db := openTestDB(t)
	cfg := testConfig(t)
	t.Cleanup(func() { dropTables(t, db, cfg) })

	require.NoError(t, SetupSchema(db, cfg))

	// Verify that all expected columns exist.
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		  FROM information_schema.columns
		 WHERE table_name = $1
		   AND column_name IN ('id', 'topic', 'payload', 'created_at')`,
		cfg.OutboxTable,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 4, count, "outbox table must have all 4 expected columns")
}

func TestSetupSchema_CreatesConfigTable(t *testing.T) {
	db := openTestDB(t)
	cfg := testConfig(t)
	t.Cleanup(func() { dropTables(t, db, cfg) })

	require.NoError(t, SetupSchema(db, cfg))

	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		  FROM information_schema.columns
		 WHERE table_name = $1
		   AND column_name IN ('key', 'last_id', 'updated_at')`,
		cfg.ConfigTable,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count, "config table must have all 3 expected columns")
}

func TestSetupSchema_SeedsCursorAtZero(t *testing.T) {
	db := openTestDB(t)
	cfg := testConfig(t)
	t.Cleanup(func() { dropTables(t, db, cfg) })

	require.NoError(t, SetupSchema(db, cfg))

	var lastID int64
	err := db.QueryRow(
		"SELECT last_id FROM " + cfg.ConfigTable + " WHERE key = 'outbox_cursor'",
	).Scan(&lastID)
	require.NoError(t, err, "cursor row must exist after SetupSchema")
	assert.Equal(t, int64(0), lastID)
}

func TestSetupSchema_DoesNotResetExistingCursor(t *testing.T) {
	db := openTestDB(t)
	cfg := testConfig(t)
	t.Cleanup(func() { dropTables(t, db, cfg) })

	require.NoError(t, SetupSchema(db, cfg))

	// Simulate the relay having advanced the cursor.
	_, err := db.Exec(
		"UPDATE " + cfg.ConfigTable + " SET last_id = 99 WHERE key = 'outbox_cursor'",
	)
	require.NoError(t, err)

	// Re-running SetupSchema must not overwrite the existing cursor.
	require.NoError(t, SetupSchema(db, cfg))

	var lastID int64
	err = db.QueryRow(
		"SELECT last_id FROM " + cfg.ConfigTable + " WHERE key = 'outbox_cursor'",
	).Scan(&lastID)
	require.NoError(t, err)
	assert.Equal(t, int64(99), lastID, "existing cursor must be preserved")
}
