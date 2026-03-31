// internal/service/relay_test.go
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/leandroluk/goutbox-relay/internal/config"
	"github.com/leandroluk/goutbox-relay/internal/db"
	"github.com/leandroluk/goutbox-relay/internal/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Helpers ──────────────────────────────────────────────────────────────────

func postgresURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set — skipping Postgres-dependent test")
	}
	return url
}

func redisURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set — skipping Redis-dependent test")
	}
	return url
}

// testDeps wires all dependencies needed for RelayService and registers
// cleanup callbacks so tables and streams are removed after each test.
func testDeps(t *testing.T) (*sql.DB, *queue.RedisQueue, config.Config) {
	t.Helper()

	pg, err := db.NewPostgres(postgresURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { pg.Close() })

	rq, err := queue.NewRedisQueue(redisURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { rq.Close() })

	// Sanitise the test name so it is safe to use as a SQL identifier.
	safeName := func(s string) string {
		// Postgres lowercases unquoted identifiers — must match exactly.
		s = strings.ToLower(s)
		b := []byte(s)
		for i, c := range b {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				b[i] = '_'
			}
		}
		return string(b)
	}

	cfg := config.Config{
		OutboxTable:  "test_outbox_" + safeName(t.Name()),
		ConfigTable:  "test_config_" + safeName(t.Name()),
		BatchSize:    10,
		PollInterval: time.Second,
		Retention:    7 * 24 * time.Hour,
	}

	require.NoError(t, db.SetupSchema(pg, cfg))

	t.Cleanup(func() {
		if _, err := pg.Exec("DROP TABLE IF EXISTS " + cfg.OutboxTable); err != nil {
			t.Logf("cleanup: drop %s: %v", cfg.OutboxTable, err)
		}
		if _, err := pg.Exec("DROP TABLE IF EXISTS " + cfg.ConfigTable); err != nil {
			t.Logf("cleanup: drop %s: %v", cfg.ConfigTable, err)
		}
	})

	return pg, rq, cfg
}

// insertEvent writes a single event directly into the outbox table and
// returns the generated id.
func insertEvent(t *testing.T, pg *sql.DB, cfg config.Config, topic string, payload any) int64 {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	var id int64
	err = pg.QueryRow(
		fmt.Sprintf("INSERT INTO %s (topic, payload) VALUES ($1, $2) RETURNING id", cfg.OutboxTable),
		topic, raw,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// cursor reads the current outbox_cursor value from the config table.
func cursor(t *testing.T, pg *sql.DB, cfg config.Config) int64 {
	t.Helper()
	var id int64
	err := pg.QueryRow(
		"SELECT last_id FROM " + cfg.ConfigTable + " WHERE key = 'outbox_cursor'",
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// ctx5s returns a context that times out after 5 seconds.
func ctx5s(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// ── Process ──────────────────────────────────────────────────────────────────

func TestProcess_ReturnsZeroWhenOutboxIsEmpty(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	svc := NewRelayService(pg, rq, cfg)

	n, err := svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "empty outbox should return 0 processed events")
}

func TestProcess_ForwardsOneEvent(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	svc := NewRelayService(pg, rq, cfg)

	id := insertEvent(t, pg, cfg, "orders.created", map[string]any{"order_id": 1})

	n, err := svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, id, cursor(t, pg, cfg))
}

func TestProcess_ForwardsMultipleEvents(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	svc := NewRelayService(pg, rq, cfg)

	const total = 5
	var lastID int64
	for i := 0; i < total; i++ {
		lastID = insertEvent(t, pg, cfg, "orders.created", map[string]any{"i": i})
	}

	n, err := svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, total, n)
	assert.Equal(t, lastID, cursor(t, pg, cfg))
}

func TestProcess_RespectsConfiguredBatchSize(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	cfg.BatchSize = 3
	svc := NewRelayService(pg, rq, cfg)

	for i := 0; i < 7; i++ {
		insertEvent(t, pg, cfg, "orders.created", map[string]any{"i": i})
	}

	n, err := svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n, "first batch should be limited to BatchSize")

	n, err = svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	n, err = svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Outbox drained.
	n, err = svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestProcess_DoesNotReprocessEvents(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	svc := NewRelayService(pg, rq, cfg)

	insertEvent(t, pg, cfg, "orders.created", map[string]any{"order_id": 42})

	n, err := svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Second call: cursor is already past the event.
	n, err = svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "already-processed events must not be forwarded again")
}

func TestProcess_AdvancesCursorToHighestID(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	svc := NewRelayService(pg, rq, cfg)

	var lastID int64
	for i := 0; i < 3; i++ {
		lastID = insertEvent(t, pg, cfg, "test.topic", map[string]any{"i": i})
	}

	_, err := svc.Process(context.Background())
	require.NoError(t, err)

	assert.Equal(t, lastID, cursor(t, pg, cfg),
		"cursor must be set to the highest processed event ID")
}

func TestProcess_ForwardsToCorrectRedisStream(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	svc := NewRelayService(pg, rq, cfg)

	stream := "payments.processed"
	insertEvent(t, pg, cfg, stream, map[string]any{"amount": 100})

	_, err := svc.Process(context.Background())
	require.NoError(t, err)

	// Use the public XLen method to verify the event landed in the right stream.
	length, err := rq.XLen(ctx5s(t), stream)
	require.NoError(t, err)
	assert.Equal(t, int64(1), length)

	t.Cleanup(func() {
		if err := rq.Del(context.Background(), stream); err != nil {
			t.Logf("cleanup: del %s: %v", stream, err)
		}
	})
}

func TestProcess_RespectsContextCancellation(t *testing.T) {
	pg, rq, cfg := testDeps(t)
	svc := NewRelayService(pg, rq, cfg)

	insertEvent(t, pg, cfg, "test.topic", map[string]any{"x": 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before calling Process

	_, err := svc.Process(ctx)
	assert.Error(t, err, "cancelled context should cause Process to return an error")
}
