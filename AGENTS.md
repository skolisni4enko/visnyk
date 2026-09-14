# AGENTS.md — visnyk

Visnyk — broadcast messenger for your inner circle. Checks number availability across messengers and sends the message to ALL available at once (WhatsApp + Telegram in parallel). Educational Go project by a PHP engineer.

## Stack

- Go 1.26+
- Wails v2 (Go backend + HTML/JS frontend → single binary per OS)
- whatsmeow — WhatsApp Web API (QR/pair-code auth, IsOnWhatsApp, Send)
- gotd/td — Telegram MTProto (contacts.resolvePhone first, single ImportContacts fallback, Send)
- Viber — stub (no official check for personal account, always "Не підключено")
- SQLite (modernc.org/sqlite) — delivery history + app logs
- `internal/normalizer` — +380 normalization via phonenumbers
- `internal/cascade` — broadcast fan-out WA + TG (+ Viber when available)
- `internal/common` — shared Telegram error classification (flood/auth/skippable/not-found)
- `internal/storage` — SQLite + XLSX export (excelize)

## How to run everything

```bash
# Desktop via Wails
wails dev

# Plain Go run (without UI, for logic testing)
go run ./cmd/app

# Build single binary (Linux)
wails build -platform linux/amd64 -tags desktop,production
# Windows cross-build from Linux (works, WebView2 loader bundled)
wails build -platform windows/amd64 -tags desktop,production
# macOS can NOT be cross-built on Linux — ship source archives, build on Mac

# Debian package (dpkg-deb fallback, nfpm preferred if installed)
./scripts/build-deb.sh <version>
```

Project lives in `visnyk/` (module `visnyk`). Never run `go run` without `go mod tidy` after adding a dependency.

```bash
go mod tidy
go vet ./...
go test ./...
golangci-lint run
```

## Command permissions

### Run autonomously — no confirmation needed
Exploration & read-only inspection:
- `ls`, `cat`, `grep`, `head`, `tail`, `find`
- `git status`, `git diff`, `git log`

Verification after any code change (required on every change):
- `go vet ./...`
- `go test ./...`
- `golangci-lint run`
- `gofmt -l .`
- `wails dev` / `wails build` (read-only check)

Read-only introspection:
- `go list -m all`
- `go version`

### Always ask for explicit confirmation
Dependency & code generation:
- `go get ...`
- `go mod tidy`
- `wails build` (final build)
- `go install ...`

Git:
- `git commit`, `git push`, `git rebase`, `git reset --hard`
- `git tag`, `git merge`

## Forbidden

- `rm -rf` or any recursive delete
- Reading/editing `.env`, `vendor/`, `node_modules/`, `*.db`, `*.sqlite`
- Committing `*.db`, `wailsjs/`, `frontend/node_modules/`, `build/bin/`
- Hardcoding `api_id`, `api_hash`, tokens — use env/config only
- Sending >30-50 messages/hour without delay — account ban risk
- Killing `/usr/bin/visnyk` (user's installed instance) during builds — check `pgrep` first

## Architectural rules — mandatory, not optional

**All business logic in `internal/cascade/`, all integrations in `internal/whatsapp/`, `internal/telegram/`, `internal/viber/`. UI in `internal/ui/` contains no send logic (only the health-status cache, which never sends).**

One Service = one messenger (`WhatsAppService`, `TelegramService`). One Cascade = broadcast fan-out.

```
UI (Wails) -> CascadeService -> WhatsAppService / TelegramService (parallel)
           -> Normalizer -> Storage (SQLite)
           -> GetConnectionsStatus (cached health, no sends)
```

- `cmd/app/main.go` — init, DI, UI launch only. No logic.
- `internal/cascade/service.go` — broadcast to all available, per-channel pacing, flood waits, circuit breaker.
- `internal/cascade/models.go` — `Contact{}`, `SendResult{}`, `Messenger` + optional `DirectSender` (single resolve+send fast path).
- `internal/common/telegram_errors.go` — `FloodWaitDuration`, `IsTelegramAuthError`, `IsTelegramSkippable`, `IsTelegramNotFound`. Shared so `cascade` never imports `telegram` (no cycle).
- `internal/normalizer/phone.go` — +380 E.164 normalization, validation.
- `internal/storage/sqlite.go` — history + logs.
- UI handlers call `cascadeService.SendBatch/SendBatchDirect` and render progress. No direct number checks.

Enforced by arch tests in `tests/arch/` (if added). If a test fails — architecture is violated.

## Normalization

All numbers normalized in `internal/normalizer` to E.164 (`+380XXXXXXXXX`) via `github.com/nyaruka/phonenumbers`. Input may be `099 123-45-67`, `(099)1234567`, `380991234567` — output always `+380991234567`. Invalid numbers filtered before messenger checks. Bulk paste field auto-tidies into one number per line (frontend only, parser is source of truth).

## Broadcast delivery (NOT priority cascade)

Every contact is delivered to **all messengers where the number exists**, in parallel goroutines per channel. `SendResult.Channel` is a comma list (`whatsapp,telegram`), `Status=sent` if at least one succeeded.

- Telegram resolve-first: `contacts.resolvePhone` (no address-book pollution, 3s throttle), single `ImportContacts` fallback only for privacy-hidden numbers, cleanup delete only on the fallback path.
- Per-channel pacing: WA 8-15s + jitter, TG 8-15s capped at 30s (`TelegramPaceMax`). A TG flood pause never blocks WA.
- Flood: server-ordered `FLOOD_WAIT` is honored in full + retry once; 3 consecutive TG floods → 5min cooldown. Skippable errors (`PRIVACY_PREMIUM_REQUIRED`, blocks) are not retried.
- Circuit breaker: first fatal auth error (401) marks the channel dead for the rest of the batch — remaining contacts skip it instantly with "сесія втрачена, перелогінься", other channels continue.
- Limit 30-50/hour per account. 98 contacts ≈ 18-20 min.

## Direct mode

`SendBatchDirect(ch)` — one messenger, no fan-out, but with the same pacing + flood protection (`sendViaChannel`). Used by "Надіслати WA" / "Надіслати TG" buttons and `CheckContacts` preview.

## Health monitor

`internal/ui/app.go` — background ticker (20s) validates sessions (`Telegram.CheckSession` = cheap `Auth.Status`, no `contacts.*`), caches `ConnectionsStatus`. `GetConnectionsStatus()` serves the cache instantly and never blocks the UI on network. Logs only on OK transitions. Frontend polls every 10s, footer shows all three messengers + session-lost banner. Viber is a stub — always disconnected.

## DTOs / Models

Go equivalent of DTOs:

- `internal/cascade/models.go` — `Contact{ Name, Phone, NormalizedPhone }`, `SendResult{ Phone, Channel, Status, Error }`
- `internal/storage/models.go` — `HistoryEntry{ Phone, Channel, SentAt, Message }`
- `internal/ui/app.go` — `ChannelStatus{ Connected, LoggedIn, OK, Error }`, `ConnectionsStatus{ WhatsApp, Telegram, Viber }`

All structs with json/sql tags, validation in `normalizer`.

## Tests and code quality — required on every change

```bash
go vet ./...
go test ./... -v
golangci-lint run
gofmt -l .
```

New Service in `internal/*` requires new `*_test.go`. Do not touch `tests/arch/` without discussion — they guard boundaries.

## Code review subagents

After implementation and verification, for non-trivial changes (new Services, UI, arch boundaries, new files) dispatch `reviewer` and `tester` subagents before marking done. Trivial changes (single-line fixes, renames) need only `go vet/test`.

## Manual QA on demand

When user says "run QA" — run `webapp-testing` skill: open UI via Playwright, click through flows, collect screenshots, console and network errors. Click-verification, does NOT replace `go vet/test`.

## Current product status

v0.3.0. Broadcast WA+TG (parallel, resolve-first TG, flood protection, circuit breaker), file attachments (1 file per batch, template as caption, long-text auto-split, 16MB media / 100MB docs), direct WA/TG send, background health monitor, bulk import (paste/file/XLSX) with one-column tidy, history + logs + deb/Windows/macOS-source packaging. Viber — stub. MVP for 70-100 +380 numbers.

## Git & branch conventions

Branches: `<type>/<YYYY-MM-DD>/<short-kebab-description>` — `feature`, `fix`, `chore`, `refactor`, `docs`. Example: `feature/2026-09-02/whatsapp-check`.

Commits: `feat:`/`fix:`/`chore:` (see `git log`).

## Agent skills

Skills live in `.opencode/skills/<name>/`. UI work uses `ui-ux-pro-max` (`.opencode/skills/ui-ux-pro-max/`).
