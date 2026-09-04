# Plan — visnyk

Priority: WA → TG → Viber. See AGENTS.md.

## Phases
1. WhatsApp (whatsmeow, QR, IsOnWhatsApp, Send) — `internal/whatsapp/`
2. Telegram (gotd/td, contacts.ImportContacts) — `internal/telegram/`
3. Cascade service (WA > TG > Viber, jitter 8-15s) — `internal/cascade/service.go` [DONE skeleton]
4. Storage (SQLite history, XLSX export via excelize) — `internal/storage/`
5. UI (Wails v2 or Fyne) — `internal/ui/`
6. Import (CSV/XLSX) + template rendering — `internal/normalizer` + `internal/cascade`
