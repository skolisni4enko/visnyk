# Architecture — visnyk 0.1.0

## Purpose
Visnyk — cascade broadcast for your inner circle (up to 100 numbers `+380`).
Checks number availability across messengers in priority `WhatsApp → Telegram → Viber` and sends the message to the first available. Uses a single personal account per messenger. Educational Go project. Product lives in `visnyk` (module `visnyk`).

## Stack
- **Go 1.26+**, **Wails v2** — Go backend + Vite frontend → single binary
- **whatsmeow** — WhatsApp Web (QR + PairPhone, IsOnWhatsApp, Send)
- **gotd/td v0.161.0** — Telegram MTProto (Auth code/QR + 2FA, ContactsImportContacts, message)
- **Viber** — stub (no personal check API)
- **SQLite** (`modernc.org/sqlite` — pure Go, CGO-free) — central `visnyk.db` (encrypted settings, history, app_logs) + separate `whatsapp/store.db` for whatsmeow (driver `sqlite`, DSN `file:...?_foreign_keys=on`). `mattn/go-sqlite3` remains `indirect` via `whatsmeow` but is not used (it caused `CGO_ENABLED=0` failure on Windows)
- **Vite 5.4** — frontend bundler, `frontend/dist` is embedded via `//go:embed all:frontend/dist`
- **nyaruka/phonenumbers** — E.164, **skip2/go-qrcode** — QR PNG
- **golang.org/x/crypto** — AES-GCM key encryption (master.key 0600, PBKDF2 fallback)

## High-level flow
```
Wails UI (frontend/main.js + src/*)
   ↓  window.go.ui.App.*
internal/ui.App  — thin binding, version + ClearAllData
   ↓  cascadeService.SendBatch / Check + storage.Log/AddHistory
internal/cascade.Service — priority WA>TG>VIBER, jitter 8–15s
   ↓
internal/whatsapp.Service / internal/telegram.Service / internal/viber
   ↓
internal/normalizer + internal/common/phone — +380 → E.164
internal/paths — OS-specific DataDir (UserConfigDir/visnyk)
internal/crypto — AES-GCM for api_id/hash
internal/storage — visnyk.db (encrypted settings, history, logs, ClearAll)
```

## Directory map (installed app)
```
~/.config/visnyk/                 # Linux (XDG), %AppData%\visnyk on Windows, ~/Library/Application Support/visnyk on macOS
  visnyk.db                       # central SQLite: settings (encrypted), history, app_logs
  master.key                       # 32-byte AES key, 0600 (generated, or PBKDF2 from VISNYK_MASTER_PASSWORD)
  telegram/session.json             # gotd/td AuthKey (4KB), FileStorage
  telegram/config.json              # legacy, migrates to visnyk.db (api_hash encrypted)
  whatsapp/store.db                # whatsmeow SQLite (device + identity)
  app.log                          # optional

cmd/app/main.go          — wiring only
main.go                  — wails.Run, embed frontend/dist, version 0.1.0, MigrateLegacy + EnsureDataDirs
internal/
  cascade/service.go     — cascade, SendBatch, models
  normalizer/phone.go    — E.164 via phonenumbers
  common/phone.go        — CleanPhone (reusable, deduped)
  common/path.go         — AbsPath, EnsureDir (reusable)
  common/qr.go           — EncodeQR (reusable)
  paths/paths.go         — DataDir(), TelegramSessionPath(), WhatsAppDBPath(), MigrateLegacy()
  crypto/crypto.go       — Manager.Encrypt/Decrypt (AES-GCM), master.key 0600
  storage/sqlite.go      — Open(), SetSetting(encrypted), AddHistory, Log, ClearAll(VACUUM), migrateLegacyTelegramConfig
  storage/models.go      — HistoryEntry, LogEntry
  whatsapp/service.go    — whatsmeow, QR + PairPhone, path via paths.WhatsAppDBPath()
  telegram/service.go    — gotd/td, code/QR + 2FA, path via paths.TelegramSessionPath(), api_hash encrypted
  viber/doc.go           — stub
  ui/app.go              — Wails bindings, Startup auto-connect, GetVersion, GetDataDir, ClearAllData, GetHistory/Logs
frontend/
  index.html             — single page: Connections (WA/TG compact) + Check + Send + modal
  style.css              — compact 580px, messenger rows
  main.js                — UI logic, imports from src/*
  src/lib/wails.js       — isWailsAvailable
  src/components/badge.js — updateBadge, updateMessengerUI
  src/components/modal.js — createLogoutModal
  src/components/tabs.js  — initTabs, switchToTab
docs/
  ARCHITECTURE.md        — this file
  plan.md                — original plan
AGENTS.md                — agent rules, build cmds
wails.json               — wails config (frontend/dist, info 0.1.0, author)
build/
  nfpm.yaml              — .deb via nfpm (libgtk, libwebkit)
  debian/DEBIAN/control  — dpkg-deb fallback
  debian/visnyk.desktop — .desktop
  windows/installer/nsis.conf — NSIS config
scripts/
  build-deb.sh           — build .deb (nfpm or dpkg-deb)
  sign-windows.sh        — osslsigncode CSC_LINK
  sign-macos.sh          — codesign + notarytool

Legacy (dev/portable): ./telegram-store/, ./whatsapp-store/ — migrate to UserConfigDir on first launch of installed version
```

## Messenger details

### WhatsApp
- Session: `DataDir/whatsapp/store.db` via `sqlstore`, `GetQRChannel` → `forwardQR` → `latestQR/latestQRPNG` (polling, fixes 20s rotation)
- PairPhone: `PairPhone(ctx, phoneWithoutPlus, true, "1", "Chrome (Windows)")` — needs `Connect()` + QR ready
- Check: `parseJID(+380) → IsOnWhatsApp`
- Phone: **not hardcoded** — taken from `Store.ID.User` dynamically (`GetPhone()`), not from config. When SIM changes — old DB is deleted via `ClearDB()`/`ClearAllData`.
- UI: QR tab + Code tab, badge `Not connected`/`Connected ✓`, `Logout` deletes DB

### Telegram
- API: `my.telegram.org` → `api_id`/`api_hash` stored **encrypted** in `visnyk.db` settings (`telegram.api_id/hash` AES-GCM) + legacy `telegram/config.json` (encrypted, migrates). `master.key` 0600 or `VISNYK_MASTER_PASSWORD` PBKDF2.
- Session: `DataDir/telegram/session.json` via `session.FileStorage`, `paths.TelegramSessionPath()` (handles UserConfigDir vs legacy cwd).
- Phone: **not hardcoded as identity** — stored encrypted only as display cache (`telegram.phone`), source of truth is `session.json` AuthKey + `Auth().Status`. When number changes — `Logout()` deletes session, new Connect with new phone.
- Auth: `auth.NewFlow(tgAuth{phone,svc})` — `Code()` waits on `codeChan`, `Password()` waits on `pwdChan` (2FA). `Run` context is `context.Background()` (not caller 30s timeout, otherwise `context canceled` after 500ms). QR: `client.QR().Auth` + 2FA retry loop for `SESSION_PASSWORD_NEEDED`/`invalid password` (keeps `pwdNeeded=true` and re-waits, shows `invalid password — try again`)
- Check: `ContactsImportContacts` with `InputPhoneContact`
- UI: api_id/hash fields (prefill via `GetTelegramConfig` — decrypts), QR/Code tabs share `tg-pwd-wrap` outside panels, 2FA hint, badge, `Logout`
- Restore: `Restore()` on `Startup` — if session exists and `Auth().Status` authorized, keeps `Run` alive, so restart needs no login

### Storage (0.1.0 new)
- `visnyk.db` — WAL mode, tables: `settings(key TEXT PK, value TEXT)` (encrypted for `telegram.*`), `history(...)`, `app_logs(...)` with rotation 10k / 30 days, indexes on phone/ts.
- `ClearAll(keepCredentials)` — `DELETE FROM history/logs` + `DELETE FROM settings` (optionally keep api_id/hash) + `VACUUM`. Called from `ui.App.ClearAllData()` + cleanup of `whatsapp/store.db` and `telegram/session.json`.
- Logs: `storage.Log(level,source,msg)` called from cascade/ui, duplicates `fmt.Printf` for `wails dev`.

## Wails bindings (internal/ui/app.go)
Thin layer: `ConnectWhatsApp`, `GetQRCodePNG`, `RequestPairCode`, `IsWhatsApp*`, `CheckWhatsApp`, `SendWhatsApp`, `SendCascadeBatch` (now with `AddHistory`+`Log`), `GetTelegramConfig`, `ConnectTelegram`/`ConnectTelegramQR`, `ProvideTelegramCode`/`ProvideTelegramPassword`/`IsTelegramPasswordNeeded`/`GetLastError`, `IsTelegram*`, `CheckTelegram`, `SendTelegram`, `Logout*`, `Restore`, **new** `GetVersion`, `GetDataDir`, `GetAppDBPath`, `ClearAllData(keepCredentials)`, `ClearHistoryOnly`, `GetHistory`, `GetAppLogs`, `LogApp`. All `window.go.ui.App.*` used via `src/lib/wails.js`.

## Build & Run
```bash
# dev (needs PKG_CONFIG for webkit)
wails dev
# or plain Vite dev for UI only
cd frontend && npm run dev -- --host 127.0.0.1 --port 34115
# vet/test/lint — check also with CGO_ENABLED=0 (Windows without gcc)
CGO_ENABLED=0 go vet ./...; CGO_ENABLED=0 go test ./...; golangci-lint run; gofmt -l .

# production — with signing
# Windows (CGO-free after 2026-09-03, gcc not needed)
CGO_ENABLED=0 wails build -platform windows/amd64
CGO_ENABLED=0 wails build -platform windows/amd64 -nsis  # installer — requires NSIS 3.08+ (makensis)
./scripts/sign-windows.sh build/bin/visnyk.exe

# Linux .deb (needs nfpm or dpkg-deb)
wails build -platform linux/amd64
./scripts/build-deb.sh 0.1.0
# or: nfpm pkg --packager deb --config build/nfpm.yaml --target build/bin/visnyk_0.1.0_amd64.deb

# macOS .app + DMG (needs Apple Developer ID)
wails build -platform darwin/universal
./scripts/sign-macos.sh build/bin/Visnyk.app

# Goland workaround (adjust PKG_CONFIG_PATH to your system)
PKG_CONFIG_PATH=./.pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig \
  go build -ldflags "-X main.Version=0.1.0" -tags=desktop,production -o /tmp/visnyk .

# frontend only
npm run build && wails build
```

## Version & Signing
- `wails.json:info.productVersion` = `0.1.0` (git tag `0.1.0`), `author` = `Visnyk Team <team@visnyk.local>`, `companyName` = `Visnyk`.
- `main.Version` ldflag, `ui.AppVersion` for `GetVersion()`.
- Windows: `scripts/sign-windows.sh` via `osslsigncode` (env `CSC_LINK`, `CSC_KEY_PASSWORD`, timestamp `http://timestamp.digicert.com`).
- macOS: `scripts/sign-macos.sh` via `codesign --options runtime` + `notarytool` (env `APPLE_ID`, `APPLE_APP_PASSWORD`, `TEAM_ID`), `hdiutil create` DMG + `stapler staple`.

## Gotchas
- **DataDir**: `paths.DataDir()` = `UserConfigDir/visnyk` for installed version, legacy `./telegram-store` migrates on first launch. `VISNYK_DATA_DIR` env overrides for tests. Do not use `filepath.Abs("...")` relative to cwd.
- **Context**: `telegram.Connect` must use `context.WithCancel(context.Background())` for `Run`, not caller 30s `bgCtx`, otherwise `defer cancel()` kills flow with `context canceled`.
- **Session path**: use `paths.TelegramSessionPath()` — Goland builds with `/tmp` cwd break relative paths.
- **QR rotation**: 20s, frontend must poll `GetLatestQR`/`GetTelegramQR`, not one-shot channel.
- **2FA**: Telegram code flow + QR both need password. `SESSION_PASSWORD_NEEDED`/`invalid password` must keep `pwdNeeded=true` and re-wait, not exit `Run`. UI shows `tg-pwd-wrap` outside panels so visible on both tabs.
- **Encryption**: `master.key` 0600, AES-GCM, `VISNYK_MASTER_PASSWORD` for CI. All `telegram.*` in `settings` are encrypted. On migration plain `config.json` is automatically imported encrypted.
- **Phone hardening**: WhatsApp phone from `Store.ID.User`, Telegram phone — cache, not identity. `ClearAllData` deletes all traces for account switch.
- **Flood**: 8–15s jitter, 30–50/hour.

## Tests
- `internal/cascade/service_test.go` (4 cases), `internal/normalizer/phone_test.go` (6), `internal/whatsapp/service_test.go` (parseJID)
- New: `internal/paths`, `internal/crypto`, `internal/storage` — manual checks for encryption and ClearAll (see `go test ./...`)
- QA: `frontend` via Playwright `with_server.py` + `npm run dev` — 13 checks (tabs, validation, no horiz scroll)
