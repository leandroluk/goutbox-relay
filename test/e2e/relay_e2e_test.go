// test/e2e/relay_e2e_test.go
//
// End-to-end tests for goutbox-relay.
//
// # Dependency resolution
//
// The suite supports two modes, chosen automatically at runtime:
//
//  1. External instances (recommended for local dev on Windows/macOS):
//     Set POSTGRES_URL and REDIS_URL before running.
//
//     docker compose up -d postgres redis
//     $env:POSTGRES_URL="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
//     $env:REDIS_URL="redis://localhost:6379"
//     make test-e2e
//
//  2. Automatic containers via testcontainers-go (CI / Linux only):
//     Leave POSTGRES_URL and REDIS_URL unset. Requires a Docker daemon
//     accessible without root (rootless Docker is not supported on Windows).
//
// Run:
//
//	make test-e2e
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/leandroluk/goutbox-relay/internal/config"
	"github.com/leandroluk/goutbox-relay/internal/db"
	"github.com/leandroluk/goutbox-relay/internal/queue"
	"github.com/leandroluk/goutbox-relay/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Dependency resolution ─────────────────────────────────────────────────────

// resolveURLs returns Postgres and Redis connection URLs.
//
// If POSTGRES_URL and REDIS_URL are already set in the environment the values
// are used directly (external instances mode). Otherwise testcontainers-go
// starts fresh containers — this path requires a working Docker daemon and
// will be skipped automatically on Windows where rootless Docker is not
// supported.
func resolveURLs(t *testing.T) (pgURL, rdURL string) {
	t.Helper()

	pgURL = os.Getenv("POSTGRES_URL")
	rdURL = os.Getenv("REDIS_URL")

	if pgURL != "" && rdURL != "" {
		t.Log("e2e: using external Postgres and Redis from environment variables")
		return pgURL, rdURL
	}

	// Attempt container startup. Skip gracefully if Docker is unavailable
	// (e.g. Windows without Docker Desktop, or CI without Docker socket).
	t.Log("e2e: POSTGRES_URL/REDIS_URL not set — attempting testcontainers")
	return startContainers(t)
}

// ── Suite setup ───────────────────────────────────────────────────────────────

type testEnv struct {
	svc   *service.RelayService
	rdb   *redis.Client
	rq    *queue.RedisQueue
	cfg   config.Config
	pgURL string
	rdURL string
}

func setup(t *testing.T) *testEnv {
	t.Helper()

	pgURL, rdURL := resolveURLs(t)

	pg, err := db.NewPostgres(pgURL)
	require.NoError(t, err)
	t.Cleanup(func() { pg.Close() })

	rq, err := queue.NewRedisQueue(rdURL)
	require.NoError(t, err)
	t.Cleanup(func() { rq.Close() })

	cfg := config.Config{
		OutboxTable:  "outbox",
		ConfigTable:  "relay_config",
		BatchSize:    100,
		PollInterval: time.Second,
		Retention:    7 * 24 * time.Hour,
	}

	require.NoError(t, db.SetupSchema(pg, cfg))

	opts, err := redis.ParseURL(rdURL)
	require.NoError(t, err)
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })

	// Wipe all state from previous runs so each test starts clean.
	// Postgres: truncate outbox rows and reset the cursor to 0.
	// Redis: flush the entire DB — safe because this is a dedicated test instance.
	ctx := context.Background()
	_, err = pg.Exec("TRUNCATE " + cfg.OutboxTable)
	require.NoError(t, err)
	_, err = pg.Exec("UPDATE " + cfg.ConfigTable + " SET last_id = 0 WHERE key = 'outbox_cursor'")
	require.NoError(t, err)
	require.NoError(t, rdb.FlushDB(ctx).Err())

	svc := service.NewRelayService(pg, rq, cfg)

	return &testEnv{
		svc:   svc,
		rdb:   rdb,
		rq:    rq,
		cfg:   cfg,
		pgURL: pgURL,
		rdURL: rdURL,
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (e *testEnv) insertEvent(t *testing.T, topic string, payload any) int64 {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	pg, err := db.NewPostgres(e.pgURL)
	require.NoError(t, err)
	defer pg.Close()

	var id int64
	err = pg.QueryRow(
		fmt.Sprintf("INSERT INTO %s (topic, payload) VALUES ($1, $2) RETURNING id", e.cfg.OutboxTable),
		topic, raw,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func (e *testEnv) streamLen(t *testing.T, stream string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := e.rdb.XLen(ctx, stream).Result()
	require.NoError(t, err)
	return n
}

func (e *testEnv) cursor(t *testing.T) int64 {
	t.Helper()
	pg, err := db.NewPostgres(e.pgURL)
	require.NoError(t, err)
	defer pg.Close()

	var id int64
	err = pg.QueryRow(
		"SELECT last_id FROM " + e.cfg.ConfigTable + " WHERE key = 'outbox_cursor'",
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestE2E_EmptyOutbox(t *testing.T) {
	env := setup(t)

	n, err := env.svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, int64(0), env.cursor(t))
}

func TestE2E_SingleEventIsForwarded(t *testing.T) {
	env := setup(t)

	id := env.insertEvent(t, "orders.created", map[string]any{"order_id": 1})

	n, err := env.svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, id, env.cursor(t))
	assert.Equal(t, int64(1), env.streamLen(t, "orders.created"))
}

func TestE2E_MultipleTopicsRoutedToCorrectStreams(t *testing.T) {
	env := setup(t)

	env.insertEvent(t, "orders.created", map[string]any{"order_id": 1})
	env.insertEvent(t, "orders.created", map[string]any{"order_id": 2})
	env.insertEvent(t, "payments.processed", map[string]any{"amount": 99})

	n, err := env.svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, int64(2), env.streamLen(t, "orders.created"))
	assert.Equal(t, int64(1), env.streamLen(t, "payments.processed"))
}

func TestE2E_BatchingProcessesAllEvents(t *testing.T) {
	env := setup(t)

	pg, err := db.NewPostgres(env.pgURL)
	require.NoError(t, err)
	t.Cleanup(func() { pg.Close() })

	rq, err := queue.NewRedisQueue(env.rdURL)
	require.NoError(t, err)
	t.Cleanup(func() { rq.Close() })

	cfg := env.cfg
	cfg.BatchSize = 3
	svc := service.NewRelayService(pg, rq, cfg)

	const total = 7
	for i := 0; i < total; i++ {
		env.insertEvent(t, "test.stream", map[string]any{"i": i})
	}

	var processed int
	for {
		n, err := svc.Process(context.Background())
		require.NoError(t, err)
		processed += n
		if n == 0 {
			break
		}
	}

	assert.Equal(t, total, processed)
	assert.Equal(t, int64(total), env.streamLen(t, "test.stream"))
}

func TestE2E_NoReprocessingAfterRestart(t *testing.T) {
	env := setup(t)

	env.insertEvent(t, "orders.created", map[string]any{"order_id": 1})

	n, err := env.svc.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Simulate restart: fresh service instances, same backing stores.
	pg2, err := db.NewPostgres(env.pgURL)
	require.NoError(t, err)
	t.Cleanup(func() { pg2.Close() })

	rq2, err := queue.NewRedisQueue(env.rdURL)
	require.NoError(t, err)
	t.Cleanup(func() { rq2.Close() })

	svc2 := service.NewRelayService(pg2, rq2, env.cfg)

	n, err = svc2.Process(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "restarted relay must not reprocess already-forwarded events")
	assert.Equal(t, int64(1), env.streamLen(t, "orders.created"))
}

func TestE2E_EventPayloadArrivesIntact(t *testing.T) {
	env := setup(t)

	payload := map[string]any{
		"order_id":   42,
		"product":    "widget",
		"quantity":   3,
		"unit_price": 9.99,
	}
	env.insertEvent(t, "orders.created", payload)

	_, err := env.svc.Process(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msgs, err := env.rdb.XRange(ctx, "orders.created", "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	rawData, ok := msgs[0].Values["data"].(string)
	require.True(t, ok, "stream entry must have a 'data' field")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(rawData), &got))
	assert.Equal(t, float64(42), got["order_id"])
	assert.Equal(t, "widget", got["product"])
}

func TestE2E_ConcurrentReplicasDoNotDuplicateEvents(t *testing.T) {
	env := setup(t)

	env.insertEvent(t, "orders.created", map[string]any{"order_id": 1})

	pg2, err := db.NewPostgres(env.pgURL)
	require.NoError(t, err)
	t.Cleanup(func() { pg2.Close() })

	rq2, err := queue.NewRedisQueue(env.rdURL)
	require.NoError(t, err)
	t.Cleanup(func() { rq2.Close() })

	svc2 := service.NewRelayService(pg2, rq2, env.cfg)

	results := make(chan int, 2)

	go func() {
		n, err := env.svc.Process(context.Background())
		if err != nil {
			n = 0
		}
		results <- n
	}()

	go func() {
		n, err := svc2.Process(context.Background())
		if err != nil {
			n = 0
		}
		results <- n
	}()

	a, b := <-results, <-results
	assert.Equal(t, 1, a+b, "exactly 1 event must be forwarded across both replicas")
	assert.Equal(t, int64(1), env.streamLen(t, "orders.created"))
}
