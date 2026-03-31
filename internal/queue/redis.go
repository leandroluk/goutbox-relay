// internal/queue/redis.go
//
// Package queue wraps the Redis client with the narrow interface that the
// relay needs: pipeline execution for bulk XADD and graceful shutdown.
// Keeping Redis concerns isolated here makes the service layer testable
// without a real Redis instance.
package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisQueue wraps a *redis.Client and exposes only the operations required
// by the relay. This prevents service-layer code from depending on the full
// redis.Client API surface.
type RedisQueue struct {
	client *redis.Client
}

// NewRedisQueue parses url, dials Redis, and verifies the connection with a
// Ping before returning. Returns an error if the URL is malformed or the
// server is unreachable within 5 seconds.
func NewRedisQueue(url string) (*RedisQueue, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("queue: parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	// Use a short timeout for the startup probe so a misconfigured URL
	// surfaces quickly rather than blocking the relay's boot sequence.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("queue: ping redis: %w", err)
	}

	return &RedisQueue{client: client}, nil
}

// Pipeline returns a new Redis pipeline. Callers are responsible for calling
// Exec on the returned Pipeliner and closing it when done.
//
// Using a pipeline for XADD batches reduces round-trips from O(n) to O(1),
// which is the primary throughput optimisation in the relay's hot path.
func (q *RedisQueue) Pipeline() redis.Pipeliner {
	return q.client.Pipeline()
}

// Close gracefully shuts down the underlying Redis connection pool.
// It should be called via defer in main() after the polling loop exits.
func (q *RedisQueue) Close() error {
	return q.client.Close()
}
