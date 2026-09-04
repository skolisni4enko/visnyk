---
name: docker-database
description: Use when containerizing Go/Wails apps with Docker, writing Dockerfiles or docker-compose.yml, configuring SQLite/PostgreSQL, handling migrations, persistence, volumes or database secrets in visnyk
---

# Docker & Database — Best Practices

## Overview
Build deterministically, persist reliably. One image = one responsibility, one volume = one source of truth. For visnyk, SQLite stores delivery history locally — not a dump for session files.

## When to Use
- Writing `Dockerfile`, `docker-compose.yml`, `.dockerignore`, `healthcheck`
- Adding or changing databases: `modernc.org/sqlite`, `postgres`, `migrations`, `sqlite.go`, `storage/*`
- Configuring `volumes`, `env`, `master.key`, `telegram-store/`, `whatsapp-store/`
- Debugging builds failing due to `CGO`, `webkit2gtk`, `PKG_CONFIG_PATH`
- Choosing `SQLite` vs `Postgres` for MVP (70-100 numbers)

When NOT to use: pure Go business logic without I/O, frontend without container, one-off `go run ./cmd/app`.

## Core Pattern

### Docker — Before/After
```dockerfile
# ❌ BAD: root, fat image, secrets in ENV, cache broken
FROM golang:latest
COPY . .
RUN go build -o visnyk ./cmd/app
ENV TG_API_HASH=secret123
CMD ["./visnyk"]

# ✅ GOOD: multi-stage, non-root, layer cache, secrets via mount
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -tags desktop,production -o /out/visnyk ./cmd/app

FROM debian:bookworm-slim
RUN useradd -m -u 10001 visnyk
USER visnyk
COPY --from=builder /out/visnyk /usr/local/bin/visnyk
HEALTHCHECK --interval=30s --timeout=3s CMD ["visnyk", "health"] || exit 1
ENTRYPOINT ["visnyk"]
```

### Database — Before/After
```go
// ❌ BAD: hardcoded path, no transactions, no migrations, commits *.db
db, _ := sql.Open("sqlite", "./visnyk.db")
db.Exec("CREATE TABLE IF NOT EXISTS history ...") // schema drift

// ✅ GOOD: path from env/volume, migrations, transactions, WAL, gitignored
// internal/storage/sqlite.go
path := os.Getenv("VISNYK_DB_PATH") // /data/visnyk.db inside container
db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
if err != nil { return fmt.Errorf("open db: %w", err) }
// migrations via embed + golang-migrate or goose
if err := runMigrationsFS(db, migrationsFS); err != nil { ... }
tx, _ := db.BeginTx(ctx, nil)
defer tx.Rollback()
```

## Quick Reference

| Topic | Rule | Example |
|-------|------|---------|
| **Base image** | `debian:bookworm-slim` or `alpine` only if no `CGO` | `FROM golang:1.26-bookworm AS builder` |
| **.dockerignore** | Ignore `*.db`, `*-store/`, `master.key`, `.env`, `frontend/node_modules`, `build/bin` | mirror `.gitignore` |
| **Layer cache** | `COPY go.mod/go.sum` → `go mod download` → then `COPY .` | saves 90% build time |
| **Non-root** | `USER 10001` | `useradd -m visnyk` |
| **Healthcheck** | `HEALTHCHECK CMD` for Wails/HTTP | `curl -f http://localhost:8080/health` |
| **Compose volumes** | Named volumes for DB, bind mounts for sessions | `volumes: [visnyk-data:/data, ./telegram-store:/app/telegram-store]` |
| **Secrets** | Never `ENV TG_API_HASH`, use `env_file` or `secrets:` | `env_file: .env` + `.gitignore` |
| **SQLite WAL** | `journal_mode=WAL`, `foreign_keys=1`, `busy_timeout=5000` | DSN param |
| **Migrations** | Versioned, `embed.FS`, idempotent | `0001_create_history.up.sql` |
| **Backup** | `sqlite3 .dump` or `VACUUM INTO` | cron inside container |
| **Go SQLite** | `modernc.org/sqlite` (pure Go, no CGO) vs `mattn/go-sqlite3` (CGO) | prefer `modernc` for Docker |
| **Postgres** | Only if >1000 numbers / concurrency | `pgx`, `pool`, `migrate` |

## Implementation

**Dockerfile for visnyk (Wails):**
- Builder: `golang:1.26-bookworm` + `node:20` for `frontend/dist` (`npm run build`)
- `libgtk-3-0`, `libwebkit2gtk-4.1-0` needed only in builder for `wails build`; runtime needs them only for GUI, otherwise `go build -tags desktop` without WebKit for headless
- Copy `PKG_CONFIG_PATH=.pkgconfig` into image

**docker-compose.yml (MVP):**
```yaml
services:
  visnyk:
    build: .
    volumes:
      - visnyk-data:/data
      - ./telegram-store:/app/telegram-store:rw
      - ./whatsapp-store:/app/whatsapp-store:rw
    env_file: .env
    restart: unless-stopped
volumes:
  visnyk-data:
```

**SQLite in visnyk:**
- `VISNYK_DB_PATH=/data/visnyk.db`, `MASTER_KEY` from env (not baked into image)
- `internal/storage/sqlite.go` — single place for SQL, UI/Cascade never touch DB directly
- Migrations in `internal/storage/migrations/*.sql` + `go:embed`
- Test: `go test -tags=integration ./internal/storage -run TestMigrations`

## Common Mistakes

| Mistake | Consequence | Fix |
|---------|-------------|-----|
| `COPY . .` before `go mod download` | cache invalidated every build | reorder |
| `USER root` in prod | RCE → root on host | `USER visnyk` |
| Commit `*.db`, `master.key` | session leak, account bans | already in `.gitignore:14` |
| `ENV TG_API_HASH` in Dockerfile | secret in `docker history` | `secrets:` / `env_file` |
| SQLite without `WAL` + `busy_timeout` | `database is locked` during cascade | DSN `_pragma=busy_timeout(5000)` |
| Migrations via `CREATE TABLE IF NOT EXISTS` | schema drift, cannot rollback | versioned `*.up.sql/*.down.sql` |
| Single volume for everything | sessions wiped on `docker compose down -v` | split `visnyk-data` vs `*-store` |
| `latest` tag | non-deterministic build | pin `golang:1.26-bookworm`, `debian:bookworm-slim` |

## Verification
```bash
docker build -t visnyk:test . && docker run --rm visnyk:test visnyk --help
docker compose config --quiet && docker compose up -d --build
hadolint Dockerfile
go vet ./... && go test ./... -run TestStorage -count=1
ls -lh /data/visnyk.db && sqlite3 /data/visnyk.db "PRAGMA integrity_check;"
```
