# goutbox-relay

[![CI](https://github.com/leandroluk/goutbox-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/leandroluk/goutbox-relay/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/leandroluk/goutbox-relay)](https://goreportcard.com/report/github.com/leandroluk/goutbox-relay)
[![Go Reference](https://pkg.go.dev/badge/github.com/leandroluk/goutbox-relay.svg)](https://pkg.go.dev/github.com/leandroluk/goutbox-relay)
![Coverage](.public/coverage.svg)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

High-performance Transactional Outbox relay written in Go. Synchronizes events from PostgreSQL to Redis Streams for reliable event-driven and CQRS architectures.

## Table of Contents

- [goutbox-relay](#goutbox-relay)
  - [Table of Contents](#table-of-contents)
  - [How It Works](#how-it-works)
  - [Features](#features)
  - [Quick Start](#quick-start)
  - [Configuration](#configuration)
  - [Database Setup](#database-setup)
  - [Kubernetes Deployment](#kubernetes-deployment)
    - [Deployment (recommended for production)](#deployment-recommended-for-production)
    - [CronJob (for low-volume workloads)](#cronjob-for-low-volume-workloads)
  - [Development](#development)
      - [Running e2e tests locally](#running-e2e-tests-locally)
    - [Project Structure](#project-structure)
  - [Architecture Notes](#architecture-notes)
  - [License](#license)

---

## How It Works

The [Transactional Outbox Pattern](https://microservices.io/patterns/data/transactional-outbox.html) eliminates the **Dual Write** problem: instead of writing to both a database and a message broker in the same operation (which can leave them out of sync on partial failure), your service writes domain events to an `outbox` table **within the same database transaction** as the business data.

`goutbox-relay` then polls that table and forwards events to Redis Streams atomically.

```
┌─────────────────────────────────────────────────┐
│  Your Service                                   │
│                                                 │
│  BEGIN TRANSACTION                              │
│    INSERT INTO orders   (...)                   │
│    INSERT INTO outbox   (topic, payload)        │  ← single atomic write
│  COMMIT                                         │
└─────────────────────────────────────────────────┘
                      │
                      ▼  polls via FOR SHARE SKIP LOCKED
┌─────────────────────────────────────────────────┐
│  goutbox-relay                                  │
│                                                 │
│  BEGIN TRANSACTION                              │
│    SELECT cursor    FOR UPDATE                  │  ← prevents duplicate processing
│    SELECT events    WHERE id > cursor           │
│    XADD  → Redis Streams  (pipeline)            │  ← O(1) round-trips
│    UPDATE cursor    = max(id)                   │
│  COMMIT                                         │
└─────────────────────────────────────────────────┘
                      │
                      ▼
           Redis Streams consumers
           (your read models, projections, etc.)
```

**Delivery guarantee:** at-least-once. If the relay crashes after forwarding events to Redis but before committing the cursor update, the same batch will be replayed on the next startup. Consumers should be idempotent.

---

## Features

- **Transactional Integrity** — Outbox Pattern eliminates Dual Writes entirely.
- **Concurrent-safe** — `FOR UPDATE` on the cursor row prevents duplicate processing when multiple replicas run simultaneously.
- **Low Footprint** — ~10 MB RAM, static CGO-free binary, scratch Docker image.
- **Efficient Forwarding** — Redis pipeline batches all `XADD` commands into a single round-trip per cycle.
- **Automatic Retention** — Stream entries older than `RETENTION_DAYS` are trimmed via `XADD MINID` with no extra jobs needed.
- **Graceful Shutdown** — Handles `SIGTERM`/`SIGINT` cleanly, finishing the current batch before exiting (Kubernetes-friendly).
- **Cloud Native** — Environment-variable configuration, Kubernetes manifests included, distroless-equivalent image.
- **Two Deployment Modes** — Long-lived Deployment (low latency) or CronJob (low resource usage), depending on your needs.

---

## Quick Start

```bash
# Start Postgres, Redis, and the relay together
docker compose up --build
```

The relay will create its own bookkeeping tables on first startup. Your application only needs to write to the `outbox` table (see [Database Setup](#database-setup)).

---

## Configuration

All configuration is done through environment variables. `POSTGRES_URL` and `REDIS_URL` are required; all others have safe defaults.

| Variable         | Required | Default        | Description                                                    |
| :--------------- | :------: | :------------- | :------------------------------------------------------------- |
| `POSTGRES_URL`   |    ✅     | —              | PostgreSQL connection string (libpq format)                    |
| `REDIS_URL`      |    ✅     | —              | Redis connection string (`redis://[user:pass@]host:port[/db]`) |
| `TABLE_OUTBOX`   |          | `outbox`       | Name of the outbox table in PostgreSQL                         |
| `TABLE_CONFIG`   |          | `relay_config` | Name of the cursor/config table in PostgreSQL                  |
| `BATCH_SIZE`     |          | `500`          | Max events fetched and forwarded per polling cycle             |
| `POLL_INTERVAL`  |          | `10`           | Seconds to sleep between cycles when the outbox is caught up   |
| `RETENTION_DAYS` |          | `7`            | Days before stream entries are trimmed from Redis              |

---

## Database Setup

The relay manages its own bookkeeping tables (`relay_config`) automatically on startup. The `outbox` table must be created by your application's migrations so it lives under the same ownership and access controls as your business data.

```sql
-- Outbox table: written by your application inside business transactions.
-- The relay only reads from this table — it never writes to it.
CREATE TABLE IF NOT EXISTS outbox (
    id         BIGSERIAL    PRIMARY KEY,
    topic      TEXT         NOT NULL,          -- maps to a Redis Stream name
    payload    JSONB        NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Optional but recommended: speeds up consumer-side queries filtered by topic.
CREATE INDEX IF NOT EXISTS idx_outbox_topic ON outbox (topic);
```

Writing an event from your application:

```sql
-- Inside your existing business transaction:
INSERT INTO outbox (topic, payload)
VALUES ('orders.created', '{"order_id": 42, "total": 99.90}');
```

---

## Kubernetes Deployment

Two modes are available under `deployments/k8s/`. They are **mutually exclusive** — apply only one.

### Deployment (recommended for production)

Best for low-latency, continuous forwarding. Runs as a single long-lived pod.

```bash
# Create the Secret with your actual connection strings
kubectl create secret generic goutbox-relay-secrets \
  --from-literal=postgres-url='postgres://user:pass@host:5432/db?sslmode=require' \
  --from-literal=redis-url='redis://:password@host:6379'

kubectl apply -f deployments/k8s/deployment.yaml
```

> **Why `replicas: 1`?** The `FOR UPDATE` cursor lock prevents duplicate processing, but multiple replicas would cause unnecessary lock contention with no throughput benefit. Horizontal scaling is better achieved by partitioning the outbox table.

### CronJob (for low-volume workloads)

Best when near-real-time delivery is not required and you want zero resource usage between runs.

```bash
kubectl apply -f deployments/k8s/cronjob.yaml
```

The CronJob runs every minute with `concurrencyPolicy: Forbid`, so it will never overlap itself even if a run takes longer than expected. `activeDeadlineSeconds: 55` ensures a stuck Job is killed before the next scheduled run.

---

## Development

```bash
make run          # Run directly via `go run`
make build        # Compile a static Linux binary into ./bin/
make test-unit    # Run unit tests (no Docker required)
make test-e2e     # Run end-to-end tests (see below)
make test         # Run unit + e2e
make compose-up   # Start Postgres and Redis
make compose-down # Stop Postgres and Redis
make coverage     # Generate coverage report and badge at .public/
make lint         # Run golangci-lint (requires golangci-lint installed)
make docker-build # Build the Docker image
make clean        # Remove build artefacts
```

#### Running e2e tests locally

On **Linux/macOS**, `make test-e2e` starts Postgres and Redis automatically via [testcontainers-go](https://golang.testcontainers.org/) — no setup needed.

On **Windows**, testcontainers-go does not support rootless Docker. Start the dependencies manually and export the connection URLs before running:

```powershell
# Terminal 1 — start dependencies
docker compose up -d postgres redis

# Terminal 2 — run e2e tests
$env:POSTGRES_URL = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
$env:REDIS_URL    = "redis://localhost:6379"
make test-e2e
```

### Project Structure

```
goutbox-relay/
├── cmd/
│   └── relay/
│       └── main.go              # Entrypoint: wires deps and runs the polling loop
├── deployments/
│   └── k8s/
│       ├── Dockerfile           # Multi-stage build → scratch final image
│       ├── deployment.yaml      # Long-lived relay (recommended)
│       └── cronjob.yaml         # Periodic relay (low-volume alternative)
├── internal/
│   ├── config/
│   │   └── config.go            # Environment-variable configuration
│   ├── db/
│   │   └── postgres.go          # Connection pool + schema bootstrap
│   ├── queue/
│   │   └── redis.go             # Redis client wrapper (pipeline-oriented)
│   └── service/
│       └── relay.go             # Core polling and forwarding logic
├── docker-compose.yaml          # Local development stack
├── Makefile
└── README.md
```

---

## Architecture Notes

**Why Redis Streams instead of Pub/Sub?**
Redis Pub/Sub is fire-and-forget: messages sent while a consumer is offline are lost. Streams are a persistent, ordered log — consumers can replay from any point, and entries survive restarts up to the configured retention window.

**Why polling instead of CDC (logical replication)?**
CDC via `pg_logical` or tools like Debezium is more operationally complex: it requires replication slots, superuser permissions, and additional infrastructure. Polling with `FOR SHARE SKIP LOCKED` is simpler to operate, works with any Postgres version ≥ 9.5, and is sufficient for most workloads up to several thousand events per second.

**At-least-once delivery and idempotency**
If the relay crashes after writing to Redis but before committing the cursor update in Postgres, the same batch will be forwarded again on the next run. Consumers must be idempotent — for example, by using the `outbox.id` embedded in the payload as an idempotency key.

---

## License

MIT — see [LICENSE](LICENSE).