package paths

import (
	"os"
	"path/filepath"
)

// DataDir returns the directory where application data is stored.
// Priority:
//  1. VISNYK_DATA_DIR env (for tests/dev, e.g. VISNYK_DATA_DIR=/tmp/visnyk-test wails dev)
//  2. os.UserConfigDir()/visnyk — standard for installed app:
//     Windows: %AppData%\visnyk
//     Linux:   ~/.config/visnyk
//     macOS:   ~/Library/Application Support/visnyk
//  3. fallback to executable dir
//
// Never returns cwd — legacy ./telegram-store etc. are migrated by MigrateLegacy().
func DataDir() string {
	if p := os.Getenv("VISNYK_DATA_DIR"); p != "" {
		return p
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "visnyk")
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	return "."
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

// MigrateLegacy moves legacy files from cwd (./telegram-store, ./whatsapp-store, ./visnyk.db etc.)
// into DataDir (UserConfigDir/visnyk) if target files don't exist.
// Call once on startup before services init. Does not depend on DataDir() returning cwd.
func MigrateLegacy() error {
	target := ""
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		target = filepath.Join(dir, "visnyk")
	} else {
		return nil
	}
	// Migrate only missing files — never overwrite existing DataDir content (preserves settings/master.key)
	legacyMap := map[string]string{
		"telegram-store/session.json": filepath.Join(target, "telegram", "session.json"),
		"telegram-store/config.json":  filepath.Join(target, "telegram", "config.json"),
		"whatsapp-store/whatsapp.db":  filepath.Join(target, "whatsapp", "store.db"),
		"visnyk.db":                   filepath.Join(target, "visnyk.db"),
	}
	for src, dst := range legacyMap {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o700)
		if err := os.Rename(src, dst); err != nil {
			if data, err2 := os.ReadFile(src); err2 == nil {
				_ = os.WriteFile(dst, data, 0o600)
			}
			continue
		}
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
	return nil
}
