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
		`CREATE TABLE IF NOT EXISTS batches(
			id TEXT PRIMARY KEY,
			name TEXT,
			channel TEXT NOT NULL,
			message_preview TEXT,
			created_at DATETIME NOT NULL,
			total INT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_batches_created ON batches(created_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// migrate: add name column if missing (for existing DBs)
	_, _ = s.db.Exec(`ALTER TABLE history ADD COLUMN name TEXT`)
	// batch grouping
	_, _ = s.db.Exec(`ALTER TABLE history ADD COLUMN batch_id TEXT`)
	_, _ = s.db.Exec(`ALTER TABLE history ADD COLUMN batch_name TEXT`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_history_batch ON history(batch_id)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_history_batch_sent ON history(batch_id, sent_at)`)
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
	batchID := e.BatchID
	batchName := e.BatchName
	if len(batchName) > 100 {
		batchName = batchName[:100]
	}
	_, err := s.db.Exec(`INSERT INTO history(phone,normalized,name,channel,status,error,sent_at,message_preview,batch_id,batch_name) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		e.Phone, e.Normalized, e.Name, e.Channel, e.Status, e.Error, e.SentAt.UTC().Format(time.RFC3339Nano), preview, batchID, batchName)
	return err
}

// CreateBatch inserts batch meta.
func (s *Store) CreateBatch(b Batch) error {
	preview := b.MessagePreview
	if len(preview) > 200 {
		preview = preview[:200]
	}
	name := b.Name
	if len(name) > 100 {
		name = name[:100]
	}
	_, err := s.db.Exec(`INSERT INTO batches(id,name,channel,message_preview,created_at,total) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, channel=excluded.channel, message_preview=excluded.message_preview, total=excluded.total`,
		b.ID, name, b.Channel, preview, b.CreatedAt.UTC().Format(time.RFC3339Nano), b.Total)
	return err
}

// ListBatches returns batches newest first.
func (s *Store) ListBatches(limit int) ([]Batch, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id,COALESCE(name,''),channel,COALESCE(message_preview,''),created_at,total FROM batches ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Batch
	for rows.Next() {
		var b Batch
		var ts string
		if err := rows.Scan(&b.ID, &b.Name, &b.Channel, &b.MessagePreview, &ts, &b.Total); err != nil {
			return nil, err
		}
		b.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, b)
	}
	return out, rows.Err()
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
	rows, err := s.db.Query(`SELECT id,phone,normalized,COALESCE(name,''),channel,status,COALESCE(error,''),sent_at,COALESCE(message_preview,''),COALESCE(batch_id,''),COALESCE(batch_name,'') FROM history ORDER BY sent_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var sentStr string
		if err := rows.Scan(&e.ID, &e.Phone, &e.Normalized, &e.Name, &e.Channel, &e.Status, &e.Error, &sentStr, &e.MessagePreview, &e.BatchID, &e.BatchName); err != nil {
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

// escapeLike escapes % _ and \ for SQLite LIKE ESCAPE '\'
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ListHistoryFiltered returns filtered history with pagination and search.
// search is matched (case-insensitive) against phone, normalized, name, channel, status, error, message_preview.
// Empty search disables the filter. Search is trimmed and limited to 100 chars.
func (s *Store) ListHistoryFiltered(limit, offset int, channel, status string) ([]HistoryEntry, error) {
	return s.ListHistoryFilteredSearch(limit, offset, channel, status, "")
}

// ListHistoryFilteredSearch is like ListHistoryFiltered but with free-text search.
func (s *Store) ListHistoryFilteredSearch(limit, offset int, channel, status, search string) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	search = strings.TrimSpace(search)
	if len(search) > 100 {
		search = search[:100]
	}
	q := `SELECT id,phone,normalized,COALESCE(name,''),channel,status,COALESCE(error,''),sent_at,COALESCE(message_preview,''),COALESCE(batch_id,''),COALESCE(batch_name,'') FROM history WHERE 1=1`
	args := []interface{}{}
	if channel != "" && channel != "all" {
		// Support broadcast combined channels "whatsapp,telegram" — match if channel contains the filtered one
		q += ` AND (',' || channel || ',' LIKE '%,' || ? || ',%' ESCAPE '\')`
		args = append(args, escapeLike(channel))
	}
	if status != "" && status != "all" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if search != "" {
		esc := escapeLike(search)
		like := "%" + esc + "%"
		// Best practice: search all user-visible fields, case-insensitive via COLLATE NOCASE
		q += ` AND (phone LIKE ? ESCAPE '\' OR normalized LIKE ? ESCAPE '\' OR COALESCE(name,'') LIKE ? ESCAPE '\' COLLATE NOCASE OR channel LIKE ? ESCAPE '\' COLLATE NOCASE OR status LIKE ? ESCAPE '\' COLLATE NOCASE OR COALESCE(error,'') LIKE ? ESCAPE '\' COLLATE NOCASE OR COALESCE(message_preview,'') LIKE ? ESCAPE '\' COLLATE NOCASE OR COALESCE(batch_id,'') LIKE ? ESCAPE '\' COLLATE NOCASE OR COALESCE(batch_name,'') LIKE ? ESCAPE '\' COLLATE NOCASE)`
		args = append(args, like, like, like, like, like, like, like, like, like)
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
		if err := rows.Scan(&e.ID, &e.Phone, &e.Normalized, &e.Name, &e.Channel, &e.Status, &e.Error, &sentStr, &e.MessagePreview, &e.BatchID, &e.BatchName); err != nil {
			return nil, err
		}
		e.SentAt, _ = time.Parse(time.RFC3339Nano, sentStr)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountHistoryFiltered returns count for filters.
func (s *Store) CountHistoryFiltered(channel, status string) (int, error) {
	return s.CountHistoryFilteredSearch(channel, status, "")
}

// CountHistoryFilteredSearch returns count for filters with search.
func (s *Store) CountHistoryFilteredSearch(channel, status, search string) (int, error) {
	search = strings.TrimSpace(search)
	if len(search) > 100 {
		search = search[:100]
	}
	q := `SELECT COUNT(*) FROM history WHERE 1=1`
	args := []interface{}{}
	if channel != "" && channel != "all" {
		q += ` AND (',' || channel || ',' LIKE '%,' || ? || ',%' ESCAPE '\')`
		args = append(args, escapeLike(channel))
	}
	if status != "" && status != "all" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if search != "" {
		esc := escapeLike(search)
		like := "%" + esc + "%"
		q += ` AND (phone LIKE ? ESCAPE '\' OR normalized LIKE ? ESCAPE '\' OR COALESCE(name,'') LIKE ? ESCAPE '\' COLLATE NOCASE OR channel LIKE ? ESCAPE '\' COLLATE NOCASE OR status LIKE ? ESCAPE '\' COLLATE NOCASE OR COALESCE(error,'') LIKE ? ESCAPE '\' COLLATE NOCASE OR COALESCE(message_preview,'') LIKE ? ESCAPE '\' COLLATE NOCASE)`
		args = append(args, like, like, like, like, like, like, like)
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

// ClearAll deletes history, logs, batches and non-essential settings, vacuums DB.
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
	if _, err := tx.Exec(`DELETE FROM batches`); err != nil {
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
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`DELETE FROM batches`)
	_, _ = s.db.Exec(`VACUUM`)
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
		return fmt.Errorf("record %d not found", id)
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
