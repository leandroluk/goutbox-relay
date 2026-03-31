// internal/service/relay.go
//
// Package service contains the core relay logic: polling the outbox table,
// forwarding events to Redis Streams, and advancing the persistent cursor —
// all within a single, atomic database transaction.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leandroluk/goutbox-relay/internal/config"
	"github.com/leandroluk/goutbox-relay/internal/queue"
	"github.com/redis/go-redis/v9"
)

// RelayService orchestrates the outbox polling loop.
// It is intentionally stateless beyond its dependencies: all durable state
// (the cursor) lives in PostgreSQL so the relay can be restarted or scaled
// to multiple replicas without coordination.
type RelayService struct {
	db    *sql.DB
	queue *queue.RedisQueue
	cfg   config.Config
}

// NewRelayService constructs a RelayService. All parameters are required.
func NewRelayService(db *sql.DB, q *queue.RedisQueue, cfg config.Config) *RelayService {
	return &RelayService{
		db:    db,
		queue: q,
		cfg:   cfg,
	}
}

// Process runs one polling cycle:
//
//  1. Opens a database transaction and locks the cursor row with FOR UPDATE,
//     preventing concurrent relay instances from processing the same batch.
//  2. Reads up to BatchSize events with IDs greater than the cursor, using
//     FOR SHARE SKIP LOCKED so rows locked by other writers are skipped
//     rather than causing the relay to block.
//  3. Appends every event to its corresponding Redis Stream via a pipeline,
//     trimming entries older than the configured retention window (MINID).
//  4. Advances the cursor to the highest processed ID and commits.
//
// Returns the number of events forwarded in this cycle, or an error.
// On error the caller should back off before retrying.
func (s *RelayService) Process(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("relay: begin tx: %w", err)
	}
	// Rollback is a no-op after a successful Commit, so it is always safe
	// to defer here — it only takes effect when we return early with an error.
	defer tx.Rollback() //nolint:errcheck

	// Lock the cursor row exclusively so concurrent relay replicas cannot
	// read the same lastID and forward duplicate events.
	var lastID int64
	cursorQuery := fmt.Sprintf(
		`SELECT last_id FROM %s WHERE key = 'outbox_cursor' FOR UPDATE`,
		s.cfg.ConfigTable,
	)
	if err := tx.QueryRowContext(ctx, cursorQuery).Scan(&lastID); err != nil {
		return 0, fmt.Errorf("relay: read cursor: %w", err)
	}

	// Fetch the next batch of unprocessed events in insertion order.
	// FOR SHARE allows multiple readers but prevents concurrent deletes or
	// updates on these rows while we hold the lock.
	// SKIP LOCKED ensures the query returns immediately even when some rows
	// are locked by an in-progress application transaction.
	eventsQuery := fmt.Sprintf(
		`SELECT id, topic, payload
		   FROM %s
		  WHERE id > $1
		  ORDER BY id ASC
		  LIMIT $2
		    FOR SHARE SKIP LOCKED`,
		s.cfg.OutboxTable,
	)
	rows, err := tx.QueryContext(ctx, eventsQuery, lastID, s.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("relay: fetch events: %w", err)
	}
	defer rows.Close()

	// minID is the oldest Redis Stream entry ID we want to keep.
	// Any entry older than Retention will be trimmed by Redis during XADD.
	// Using UnixMilli matches Redis's millisecond-precision stream IDs.
	minID := fmt.Sprintf("%d", time.Now().Add(-s.cfg.Retention).UnixMilli())

	pipe := s.queue.Pipeline()
	var count int
	var newLastID int64

	for rows.Next() {
		var id int64
		var topic string
		var payload json.RawMessage

		if err := rows.Scan(&id, &topic, &payload); err != nil {
			return 0, fmt.Errorf("relay: scan row: %w", err)
		}

		// XADD with ID "*" lets Redis assign an auto-generated, monotonic
		// stream ID. MINID trims entries older than the retention window
		// and Approx=true allows Redis to batch the trim for performance.
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: topic,
			ID:     "*",
			MinID:  minID,
			Approx: true,
			// Wrapping the raw JSON payload under the "data" key keeps the
			// stream message schema stable — future metadata fields (e.g.
			// "trace_id") can be added without breaking consumers.
			Values: map[string]any{"data": string(payload)},
		})

		newLastID = id
		count++
	}

	// rows.Err() must be checked after the loop to catch any network or
	// decoding errors that occurred during iteration.
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("relay: iterate rows: %w", err)
	}

	// Skip the Redis round-trip and cursor update when nothing was fetched.
	if count == 0 {
		return 0, nil
	}

	// Flush all XADD commands to Redis in a single round-trip.
	// If the pipeline fails the transaction will be rolled back by defer,
	// so the cursor is not advanced and the same batch will be retried.
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("relay: redis pipeline exec: %w", err)
	}

	// Advance the cursor only after Redis has accepted all events.
	// This guarantees at-least-once delivery: a crash between Exec and
	// Commit will cause the batch to be replayed on the next startup.
	updateCursor := fmt.Sprintf(
		`UPDATE %s SET last_id = $1, updated_at = NOW() WHERE key = 'outbox_cursor'`,
		s.cfg.ConfigTable,
	)
	if _, err := tx.ExecContext(ctx, updateCursor, newLastID); err != nil {
		return 0, fmt.Errorf("relay: update cursor: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("relay: commit tx: %w", err)
	}

	return count, nil
}
