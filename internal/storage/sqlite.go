package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"visnyk/internal/crypto"
	"visnyk/internal/paths"
)

// Store is the central SQLite for settings/history/logs.
// All sensitive settings (api_id/hash) are encrypted via crypto.Manager.
type Store struct {
	db     *sql.DB
	crypto *crypto.Manager
	path   string
}

// Open creates or opens visnyk.db at DataDir, runs migrations, inits crypto.
func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		dataDir = paths.DataDir()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("storage mkdir: %w", err)
	}
	// Ensure subdirs
	_ = os.MkdirAll(paths.TelegramDir(), 0o700)
	_ = os.MkdirAll(paths.WhatsAppDir(), 0o700)

	dbPath := filepath.Join(dataDir, "visnyk.db")
	cm, err := crypto.New(dataDir)
	if err != nil {
		return nil, fmt.Errorf("crypto init: %w", err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db, crypto: cm, path: dbPath}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	// opportunistic migration from legacy JSON
	_ = s.migrateLegacyTelegramConfig()
	return s, nil
}

// OpenWithKey for tests (in-memory or temp file).
func OpenWithKey(dbPath string, cm *crypto.Manager) (*Store, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on", dbPath))
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, crypto: cm, path: dbPath}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS history(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			phone TEXT NOT NULL,
			normalized TEXT NOT NULL,
			channel TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT,
			sent_at DATETIME NOT NULL,
			message_preview TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_history_phone ON history(phone)`,
		`CREATE INDEX IF NOT EXISTS idx_history_sent ON history(sent_at)`,
		`CREATE TABLE IF NOT EXISTS app_logs(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			level TEXT NOT NULL,
			source TEXT NOT NULL,
			msg TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_ts ON app_logs(ts)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// migrate: add name column if missing (for existing DBs)
	_, _ = s.db.Exec(`ALTER TABLE history ADD COLUMN name TEXT`)
	// ensure sent_at index exists for new sorting
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_history_sent ON history(sent_at)`)
	return nil
}

// Close closes DB.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Path returns DB path.
func (s *Store) Path() string { return s.path }

// --- Settings (encrypted) ---

var encryptedKeys = map[string]bool{
	"telegram.api_id":   true,
	"telegram.api_hash": true,
	"telegram.phone":    true,
	"whatsapp.phone":    true,
}

// SetSetting stores key/value, encrypting if sensitive.
func (s *Store) SetSetting(key, value string) error {
	if encryptedKeys[key] && value != "" {
		enc, err := s.crypto.Encrypt(value)
		if err != nil {
			return err
		}
		value = enc
	}
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// GetSetting retrieves and decrypts if needed. Empty string if not found.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if encryptedKeys[key] && v != "" {
		dec, err := s.crypto.Decrypt(v)
		if err != nil {
			// fallback: return raw (migration)
			return v, nil
		}
		return dec, nil
	}
	return v, nil
}

// DeleteSetting removes key.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, key)
	return err
}

// --- History ---

// AddHistory inserts history entry.
func (s *Store) AddHistory(e HistoryEntry) error {
	preview := e.MessagePreview
	if len(preview) > 200 {
		preview = preview[:200]
	}
	_, err := s.db.Exec(`INSERT INTO history(phone,normalized,name,channel,status,error,sent_at,message_preview) VALUES(?,?,?,?,?,?,?,?)`,
		e.Phone, e.Normalized, e.Name, e.Channel, e.Status, e.Error, e.SentAt.UTC().Format(time.RFC3339Nano), preview)
	return err
}

// ListHistory returns last N entries sorted newest first by sent_at.
func (s *Store) ListHistory(limit int) ([]HistoryEntry, error) {
	return s.ListHistoryPaged(limit, 0)
}

// ListHistoryPaged returns history with server pagination (limit/offset).
func (s *Store) ListHistoryPaged(limit, offset int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`SELECT id,phone,normalized,COALESCE(name,''),channel,status,COALESCE(error,''),sent_at,COALESCE(message_preview,'') FROM history ORDER BY sent_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var sentStr string
		if err := rows.Scan(&e.ID, &e.Phone, &e.Normalized, &e.Name, &e.Channel, &e.Status, &e.Error, &sentStr, &e.MessagePreview); err != nil {
			return nil, err
		}
		e.SentAt, _ = time.Parse(time.RFC3339Nano, sentStr)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountHistory returns count.
func (s *Store) CountHistory() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM history`).Scan(&n)
	return n, err
}

// ListHistoryFiltered returns filtered history with pagination.
func (s *Store) ListHistoryFiltered(limit, offset int, channel, status string) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT id,phone,normalized,COALESCE(name,''),channel,status,COALESCE(error,''),sent_at,COALESCE(message_preview,'') FROM history WHERE 1=1`
	args := []interface{}{}
	if channel != "" && channel != "all" {
		q += ` AND channel = ?`
		args = append(args, channel)
	}
	if status != "" && status != "all" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY sent_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var sentStr string
		if err := rows.Scan(&e.ID, &e.Phone, &e.Normalized, &e.Name, &e.Channel, &e.Status, &e.Error, &sentStr, &e.MessagePreview); err != nil {
			return nil, err
		}
		e.SentAt, _ = time.Parse(time.RFC3339Nano, sentStr)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountHistoryFiltered returns count for filters.
func (s *Store) CountHistoryFiltered(channel, status string) (int, error) {
	q := `SELECT COUNT(*) FROM history WHERE 1=1`
	args := []interface{}{}
	if channel != "" && channel != "all" {
		q += ` AND channel = ?`
		args = append(args, channel)
	}
	if status != "" && status != "all" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// --- Logs ---

// Log inserts app log and also enforces retention (keep 10k or 30 days).
func (s *Store) Log(level, source, msg string) error {
	level = strings.ToUpper(level)
	if level == "" {
		level = "INFO"
	}
	_, err := s.db.Exec(`INSERT INTO app_logs(level,source,msg,ts) VALUES(?,?,?,?)`, level, source, msg, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	// retention: keep last 10000 or 30 days — best effort, ignore errors
	_, _ = s.db.Exec(`DELETE FROM app_logs WHERE id NOT IN (SELECT id FROM app_logs ORDER BY id DESC LIMIT 10000)`)
	_, _ = s.db.Exec(`DELETE FROM app_logs WHERE ts < datetime('now','-30 days')`)
	return nil
}

// ListLogs returns last N logs.
func (s *Store) ListLogs(limit int) ([]LogEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id,ts,level,source,msg FROM app_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Level, &e.Source, &e.Msg); err != nil {
			return nil, err
		}
		e.TS, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Clear all / Export ---

// ClearAll deletes history, logs, and non-essential settings, vacuums DB.
// It keeps encrypted api credentials if keepCredentials true.
func (s *Store) ClearAll(keepCredentials bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM history`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM app_logs`); err != nil {
		return err
	}
	if keepCredentials {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key NOT IN ('telegram.api_id','telegram.api_hash')`); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM settings`); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, _ = s.db.Exec(`VACUUM`)
	return nil
}

// ClearHistoryOnly helper for UI.
func (s *Store) ClearHistoryOnly() error {
	_, err := s.db.Exec(`DELETE FROM history`)
	if err == nil {
		_, _ = s.db.Exec(`VACUUM`)
	}
	return err
}

// DeleteHistory deletes single entry by id.
func (s *Store) DeleteHistory(id int64) error {
	res, err := s.db.Exec(`DELETE FROM history WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("запис %d не знайдено", id)
	}
	_, _ = s.db.Exec(`VACUUM`)
	return nil
}

// migrateLegacyTelegramConfig reads old DataDir/telegram/config.json (plain) and imports into encrypted settings.
func (s *Store) migrateLegacyTelegramConfig() error {
	cfgPath := paths.TelegramConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}
	// naive JSON parse without struct to avoid import cycle
	txt := string(data)
	// quick check: if already migrated (settings exist), skip
	if v, _ := s.GetSetting("telegram.api_id"); v != "" {
		return nil
	}
	// parse with simple extraction
	extract := func(key string) string {
		needle := `"` + key + `"`
		idx := strings.Index(txt, needle)
		if idx < 0 {
			return ""
		}
		rest := txt[idx+len(needle):]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			return ""
		}
		rest = strings.TrimSpace(rest[colon+1:])
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end >= 0 {
				return rest[1 : 1+end]
			}
		} else {
			// numeric api_id
			end := 0
			for end < len(rest) && (rest[end] >= '0' && rest[end] <= '9' || rest[end] == '-') {
				end++
			}
			return strings.TrimSpace(rest[:end])
		}
		return ""
	}
	if v := extract("app_id"); v != "" {
		_ = s.SetSetting("telegram.api_id", v)
	}
	if v := extract("app_hash"); v != "" {
		_ = s.SetSetting("telegram.api_hash", v)
	}
	if v := extract("phone"); v != "" {
		_ = s.SetSetting("telegram.phone", v)
	}
	return nil
}
