# ============================================================
# Stage 1 — Builder
# ============================================================
# golang:1.23-alpine is used instead of the full Debian image to keep the
# build layer small and to avoid pulling in unnecessary tooling.
FROM golang:1.25-alpine AS builder

WORKDIR /app

# ca-certificates is needed at build time so `go mod download` can reach
# HTTPS module proxies (sum.golang.org, proxy.golang.org). The certs are
# also copied into the final image for TLS connections to Postgres/Redis.
RUN apk add --no-cache ca-certificates

# Copy dependency manifests first. Docker caches this layer separately from
# the source code, so `go mod download` is only re-run when go.mod/go.sum
# change — not on every code edit.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source tree.
COPY . .

# Compile a fully static binary:
#   CGO_ENABLED=0  — no libc linkage; required for the scratch final stage
#   GOOS=linux     — explicit target OS (builder may be darwin/windows in CI)
#   -ldflags "-s -w" — strip symbol table and DWARF debug info (~30% smaller)
#   -trimpath      — remove local filesystem paths from the binary (reproducibility + security)
RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -trimpath \
    -o goutbox-relay ./cmd/relay

# ============================================================
# Stage 2 — Final image
# ============================================================
# scratch is the absolute minimal base: zero OS layer, zero shell, zero attack
# surface. The binary and TLS certs are the only files in the image.
FROM scratch

# Copy CA bundle so the relay can open TLS connections to managed Postgres
# (e.g. RDS, Cloud SQL) and Redis (e.g. Redis Cloud, Upstash).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary from the builder stage.
COPY --from=builder /app/goutbox-relay /goutbox-relay

# Provide safe defaults for optional tuning variables.
# POSTGRES_URL and REDIS_URL are intentionally omitted — they contain
# credentials and must be injected at runtime via Secrets, never baked in.
ENV POLL_INTERVAL=10 \
    BATCH_SIZE=500 \
    RETENTION_DAYS=7

# Use the exec form (JSON array) to ensure the binary receives OS signals
# directly (PID 1), which is required for graceful shutdown on SIGTERM.
ENTRYPOINT ["/goutbox-relay"]