# visnyk

Cascade broadcast for your inner circle. Checks number availability across messengers in priority **WhatsApp → Telegram → Viber** and sends the message to the first available. Uses a single personal account per messenger. Educational Go project.

[![Go](https://img.shields.io/badge/Go-1.26-blue)](#)
[![Wails](https://img.shields.io/badge/Wails-v2-red)](#)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Stack

- Go 1.26+, Wails v2 (Go backend + Vite frontend → single binary)
- whatsmeow (WhatsApp Web — QR + PairPhone, IsOnWhatsApp, Send)
- gotd/td (Telegram MTProto — code/QR + 2FA)
- SQLite (delivery history), phonenumbers (E.164 `+380`)

## Quick start

```bash
# deps
go mod tidy
cd frontend && npm install && cd ..

# dev (Wails — requires WebKit)
wails dev
# or logic only without UI
go run ./cmd/app

# checks before commit (required)
go vet ./...
go test ./... -v
gofmt -l .
golangci-lint run

# production build (frontend/dist is not committed — build before wails build)
cd frontend && npm run build && cd ..
wails build
# Goland workaround (adjust PKG_CONFIG_PATH to your system)
PKG_CONFIG_PATH=./.pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig \
  go build -tags=desktop,production -o /tmp/visnyk .
```

## Configuration

Copy examples and fill in your own data (never commit secrets):

```bash
cp .env.example .env
cp telegram-store/config.json.example telegram-store/config.json
# edit TG_API_ID / TG_API_HASH (get at https://my.telegram.org → API development tools)
```

Sessions are stored locally and ignored by git:
- `telegram-store/session.json` — Telegram AuthKey
- `whatsapp-store/whatsapp.db` — WhatsApp session
- `.env`, `.env.wails`

## Architecture

```
Wails UI (frontend/*) → internal/ui.App (thin binding)
                    → internal/cascade (WA > TG > VIBER, jitter 8–15s)
                    → internal/whatsapp / internal/telegram / internal/viber
                    → internal/normalizer + internal/common + internal/storage
```

Details: `docs/ARCHITECTURE.md`, rules: `AGENTS.md`.

## License

MIT — see [LICENSE](LICENSE).
