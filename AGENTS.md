# AGENTS.md — visnyk

Visnyk — cascade broadcast for your inner circle. Checks number availability across messengers in priority WhatsApp → Telegram → Viber and sends the message to the first available. Educational Go project by a PHP engineer.

## Stack

- Go 1.26+
- Wails v2 (Go backend + React/HTML frontend → single .exe) or Fyne v2 (pure Go UI) — UI choice at implementation stage
- whatsmeow — WhatsApp Web API (QR auth, IsOnWhatsApp, Send)
- gotd/td — Telegram MTProto (ImportContacts, Send)
- Viber — REST API / stub (no official check for personal account)
- SQLite — delivery history to avoid duplicate sends
- `internal/normalizer` — +380 normalization via phonenumbers
- `internal/cascade` — priority queue WA > TG > VIBER
- `internal/storage` — SQLite + XLSX export (excelize)

## How to run everything

```bash
# Desktop via Wails (if Wails selected)
wails dev

# Plain Go run (without UI, for logic testing)
go run ./cmd/app

# Build single binary
wails build
# or
go build -o visnyk ./cmd/app

# Fyne alternative (if Fyne selected)
go run ./cmd/app
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

## Architectural rules — mandatory, not optional

**All business logic in `internal/cascade/`, all integrations in `internal/whatsapp/`, `internal/telegram/`, `internal/viber/`. UI in `internal/ui/` contains no business logic.**

One Service = one messenger (`WhatsAppService`, `TelegramService`). One Cascade = priority queue.

```
UI (Wails/Fyne) -> CascadeService -> WhatsAppService / TelegramService / ViberService
                -> Normalizer -> Storage (SQLite)
```

- `cmd/app/main.go` — init, DI, UI launch only. No logic.
- `internal/cascade/service.go` — WA > TG > VIBER cascade, delays, retries.
- `internal/normalizer/phone.go` — +380 E.164 normalization, validation.
- `internal/storage/sqlite.go` — history to avoid duplicate delivery.
- UI handlers only call `cascadeService.SendBatch(phones, template)` and render progress. No direct number checks.

Enforced by arch tests in `tests/arch/` (if added). If a test fails — architecture is violated.

## Normalization

All numbers normalized in `internal/normalizer` to E.164 (`+380XXXXXXXXX`) via `github.com/nyaruka/phonenumbers`. Input may be `099 123-45-67`, `(099)1234567`, `380991234567` — output always `+380991234567`. Invalid numbers filtered before messenger checks.

## Cascade priority

WA → TG → Viber. If WhatsApp available — send there and skip further checks. If not — Telegram. Viber is last resort / stub (no official check for personal account, only send attempt or skip).

Delay between messages: 8-15s + random, typing simulation. Limit 30-50/hour per account.

## DTOs / Models

Go equivalent of DTOs:

- `internal/cascade/models.go` — `Contact{ Name, Phone, NormalizedPhone }`, `SendResult{ Phone, Channel, Status, Error }`
- `internal/storage/models.go` — `HistoryEntry{ Phone, Channel, SentAt, Message }`

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

Initialization stage. Agent structure and skeleton created. No WhatsApp/Telegram integrations, cascade, or UI yet. MVP for 70-100 +380 numbers.

## Git & branch conventions

Branches: `<type>/<YYYY-MM-DD>/<short-kebab-description>` — `feature`, `fix`, `chore`, `refactor`, `docs`. Example: `feature/2026-09-02/whatsapp-check`.

Commits: `feat:`/`fix:`/`chore:` (see `git log`).

## Agent skills

Skills live in `.opencode/skills/<name>/`. UI work uses `ui-ux-pro-max` (`.opencode/skills/ui-ux-pro-max/`).
