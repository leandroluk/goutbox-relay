// internal/queue/redis_test.go
package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redisURL returns the test Redis URL or skips when absent.
func redisURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set — skipping Redis-dependent test")
	}
	return url
}

// openTestQueue opens a RedisQueue and registers cleanup automatically.
func openTestQueue(t *testing.T) *RedisQueue {
	t.Helper()
	q, err := NewRedisQueue(redisURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { q.Close() })
	return q
}

// ctx5s returns a context that times out after 5 seconds.
func ctx5s(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// --- NewRedisQueue ----------------------------------------------------------

func TestNewRedisQueue_FailsOnInvalidURL(t *testing.T) {
	_, err := NewRedisQueue("not-a-redis-url")
	assert.Error(t, err, "invalid URL should return an error")
}

func TestNewRedisQueue_FailsOnUnreachableHost(t *testing.T) {
	_, err := NewRedisQueue("redis://127.0.0.1:19998")
	assert.Error(t, err, "unreachable host should return an error")
}

func TestNewRedisQueue_Succeeds(t *testing.T) {
	q := openTestQueue(t)
	assert.NotNil(t, q)
}

// --- Pipeline ---------------------------------------------------------------

func TestPipeline_ReturnsNonNilPipeliner(t *testing.T) {
	q := openTestQueue(t)
	pipe := q.Pipeline()
	require.NotNil(t, pipe)
	pipe.Discard()
}

func TestPipeline_ExecutesSingleXAdd(t *testing.T) {
	q := openTestQueue(t)
	ctx := ctx5s(t)
	stream := "test:queue:single"

	t.Cleanup(func() {
		if err := q.Del(context.Background(), stream); err != nil {
			t.Logf("cleanup: del %s: %v", stream, err)
		}
	})

	pipe := q.Pipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		ID:     "*",
		Values: map[string]any{"data": `{"test":true}`},
	})

	cmds, err := pipe.Exec(ctx)
	require.NoError(t, err)
	require.Len(t, cmds, 1)
	assert.NoError(t, cmds[0].Err())

	length, err := q.XLen(ctx, stream)
	require.NoError(t, err)
	assert.Equal(t, int64(1), length)
}

func TestPipeline_ExecutesMultipleXAdds(t *testing.T) {
	q := openTestQueue(t)
	ctx := ctx5s(t)
	stream := "test:queue:multi"

	t.Cleanup(func() {
		if err := q.Del(context.Background(), stream); err != nil {
			t.Logf("cleanup: del %s: %v", stream, err)
		}
	})

	const n = 5
	pipe := q.Pipeline()
	for i := 0; i < n; i++ {
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: stream,
			ID:     "*",
			Values: map[string]any{"data": `{"i":` + string(rune('0'+i)) + `}`},
		})
	}

	cmds, err := pipe.Exec(ctx)
	require.NoError(t, err)
	assert.Len(t, cmds, n)

	length, err := q.XLen(ctx, stream)
	require.NoError(t, err)
	assert.Equal(t, int64(n), length)
}

// --- Close ------------------------------------------------------------------

func TestClose_Succeeds(t *testing.T) {
	q, err := NewRedisQueue(redisURL(t))
	require.NoError(t, err)
	assert.NoError(t, q.Close())
}
