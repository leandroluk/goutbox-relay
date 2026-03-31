// cmd/relay/main.go
//
// main is the entrypoint for the goutbox-relay binary.
// It wires together the configuration, database, and queue dependencies,
// then drives the polling loop until the process receives a termination signal.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/leandroluk/goutbox-relay/internal/config"
	"github.com/leandroluk/goutbox-relay/internal/db"
	"github.com/leandroluk/goutbox-relay/internal/queue"
	"github.com/leandroluk/goutbox-relay/internal/service"
)

func main() {
	// ── Configuration ────────────────────────────────────────────────────────
	cfg := config.Load()

	// ── Dependencies ─────────────────────────────────────────────────────────
	pg, err := db.NewPostgres(cfg.PostgresURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pg.Close()

	// RedisQueue wraps the client and validates the connection at startup.
	rq, err := queue.NewRedisQueue(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer rq.Close()

	// ── Schema bootstrap ─────────────────────────────────────────────────────
	// Creates the relay's bookkeeping tables (outbox + config) if they do not
	// exist yet. Safe to call on every startup — all statements are idempotent.
	if err := db.SetupSchema(pg, cfg); err != nil {
		log.Fatalf("schema setup: %v", err)
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	// ctx is cancelled when SIGINT or SIGTERM is received (e.g. kubectl delete
	// pod, Ctrl-C, or a container orchestrator rolling update). The polling
	// loop checks ctx.Done() so the current batch finishes cleanly before exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Polling loop ──────────────────────────────────────────────────────────
	svc := service.NewRelayService(pg, rq, cfg)

	log.Printf("goutbox-relay started (batch=%d, poll=%v, retention=%v)",
		cfg.BatchSize, cfg.PollInterval, cfg.Retention)

	for {
		// Respect cancellation at the top of every iteration so the relay
		// exits promptly even when it is continuously at full batch capacity.
		select {
		case <-ctx.Done():
			log.Println("goutbox-relay shutting down")
			return
		default:
		}

		processed, err := svc.Process(ctx)
		if err != nil {
			// Context cancellation is not an application error — it means we
			// received a shutdown signal mid-batch. Exit cleanly.
			if errors.Is(err, context.Canceled) {
				log.Println("goutbox-relay shutting down")
				return
			}

			log.Printf("process error: %v — retrying in 5s", err)

			// Back off on error to avoid hammering a degraded database or
			// Redis instance. The 5-second delay is intentionally not
			// configurable to keep the failure mode simple and predictable.
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				log.Println("goutbox-relay shutting down")
				return
			}
			continue
		}

		log.Printf("forwarded %d event(s)", processed)

		// When the batch is smaller than BatchSize the outbox is caught up;
		// sleep for PollInterval before the next cycle to reduce idle load.
		// When a full batch was returned, loop immediately — there may be
		// more events waiting.
		if processed < cfg.BatchSize {
			select {
			case <-time.After(cfg.PollInterval):
			case <-ctx.Done():
				log.Println("goutbox-relay shutting down")
				return
			}
		}
	}
}
