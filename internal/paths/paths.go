package paths

import (
	"os"
	"path/filepath"
)

// DataDir returns the directory where application data is stored.
// Priority:
//  1. VISNYK_DATA_DIR env (for tests/dev)
//  2. If legacy ./telegram-store or ./whatsapp-store exists next to cwd — use cwd (portable migration)
//  3. os.UserConfigDir()/visnyk — standard for installed app:
//     Windows: %AppData%\visnyk
//     Linux:   ~/.config/visnyk
//     macOS:   ~/Library/Application Support/visnyk
//  4. fallback to executable dir
func DataDir() string {
	if p := os.Getenv("VISNYK_DATA_DIR"); p != "" {
		return p
	}
	// Portable migration hint: if legacy dirs exist in cwd, keep using cwd for one run
	// Caller (MigrateLegacy) will move them to UserConfigDir afterwards.
	if hasLegacyInCwd() {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "visnyk")
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	return "."
}

func hasLegacyInCwd() bool {
	for _, p := range []string{"telegram-store", "whatsapp-store", "visnyk.db", "visnyk.sqlite"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// AppDBPath is the central SQLite for settings/history/logs.
func AppDBPath() string { return filepath.Join(DataDir(), "visnyk.db") }

// TelegramDir returns directory for Telegram session/config.
func TelegramDir() string { return filepath.Join(DataDir(), "telegram") }

// TelegramSessionPath — gotd/td session JSON.
func TelegramSessionPath() string { return filepath.Join(TelegramDir(), "session.json") }

// TelegramConfigPath — encrypted api_id/hash (legacy fallback).
func TelegramConfigPath() string { return filepath.Join(TelegramDir(), "config.json") }

// WhatsAppDBPath — whatsmeow SQLite.
func WhatsAppDBPath() string { return filepath.Join(DataDir(), "whatsapp", "store.db") }

// WhatsAppDir returns whatsapp directory.
func WhatsAppDir() string { return filepath.Join(DataDir(), "whatsapp") }

// LogsPath — optional file log (also stored in DB).
func LogsPath() string { return filepath.Join(DataDir(), "app.log") }

// EnsureDataDirs creates all required directories.
func EnsureDataDirs() error {
	for _, d := range []string{DataDir(), TelegramDir(), WhatsAppDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// MigrateLegacy moves old ./telegram-store and ./whatsapp-store into DataDir if new locations empty.
// Call once on startup before services init.
func MigrateLegacy() error {
	target := ""
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		target = filepath.Join(dir, "visnyk")
	} else {
		return nil
	}
	// Only migrate if target empty and legacy exists
	if _, err := os.Stat(target); err == nil {
		// check if already has data — skip
		if _, err := os.Stat(filepath.Join(target, "telegram", "session.json")); err == nil {
			return nil
		}
		if _, err := os.Stat(filepath.Join(target, "visnyk.db")); err == nil {
			return nil
		}
	}
	legacyMap := map[string]string{
		"telegram-store/session.json": filepath.Join(target, "telegram", "session.json"),
		"telegram-store/config.json":  filepath.Join(target, "telegram", "config.json"),
		"whatsapp-store/whatsapp.db":  filepath.Join(target, "whatsapp", "store.db"),
		"visnyk.db":                   filepath.Join(target, "visnyk.db"),
	}
	migrated := false
	for src, dst := range legacyMap {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o700)
		// try rename, fallback to copy
		if err := os.Rename(src, dst); err != nil {
			// copy file content
			if data, err2 := os.ReadFile(src); err2 == nil {
				_ = os.WriteFile(dst, data, 0o600)
				migrated = true
			}
			continue
		}
		migrated = true
	}
	// Also migrate -wal/-shm for whatsapp
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		src := "whatsapp-store/whatsapp.db" + suffix
		dst := filepath.Join(target, "whatsapp", "store.db"+suffix)
		if _, err := os.Stat(src); err == nil {
			if _, err := os.Stat(dst); err != nil {
				_ = os.Rename(src, dst)
			}
		}
	}
	if migrated {
		// best effort: leave legacy dirs (empty) to avoid confusion
	}
	return nil
}
