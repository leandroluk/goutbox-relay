// test/e2e/containers_test.go
//
// Provides startContainers(), called by resolveURLs() only when
// POSTGRES_URL and REDIS_URL are not set in the environment.
//
// This file is compiled on all platforms but startContainers() calls
// t.Skip() immediately on Windows, where rootless Docker is not supported
// by testcontainers-go.
package e2e

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startContainers starts Postgres and Redis via testcontainers-go and returns
// their connection URLs. On Windows the test is skipped because
// testcontainers-go does not support rootless Docker on that platform.
//
// Containers are terminated automatically when the test finishes.
func startContainers(t *testing.T) (pgURL, rdURL string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("e2e: testcontainers-go does not support rootless Docker on Windows — " +
			"set POSTGRES_URL and REDIS_URL to use external instances instead")
	}

	ctx := context.Background()

	// ── Postgres ─────────────────────────────────────────────────────────────
	pgCtr, err := tcpostgres.Run(ctx,
		"postgres:15-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			// Postgres logs this message twice during init (once before the
			// internal restart, once after). Waiting for 2 occurrences
			// ensures the server is fully ready before we connect.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "failed to start Postgres container")
	t.Cleanup(func() {
		if err := pgCtr.Terminate(ctx); err != nil {
			t.Logf("warn: failed to terminate Postgres container: %v", err)
		}
	})

	pgURL, err = pgCtr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdCtr, err := tcredis.Run(ctx,
		"redis:7-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err, "failed to start Redis container")
	t.Cleanup(func() {
		if err := rdCtr.Terminate(ctx); err != nil {
			t.Logf("warn: failed to terminate Redis container: %v", err)
		}
	})

	rdURL, err = rdCtr.ConnectionString(ctx)
	require.NoError(t, err)

	return pgURL, rdURL
}
