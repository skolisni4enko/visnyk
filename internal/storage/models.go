package storage

import "time"

// HistoryEntry — one cascade send attempt.
type HistoryEntry struct {
	ID             int64     `json:"id"`
	Phone          string    `json:"phone"`
	Normalized     string    `json:"normalized"`
	Name           string    `json:"name,omitempty"`
	Channel        string    `json:"channel"`
	Status         string    `json:"status"` // sent/failed/skipped
	Error          string    `json:"error,omitempty"`
	SentAt         time.Time `json:"sentAt"`
	MessagePreview string    `json:"messagePreview,omitempty"`
	BatchID        string    `json:"batchId,omitempty"`
	BatchName      string    `json:"batchName,omitempty"`
}

// Batch — aggregate for history grouping.
type Batch struct {
	ID             string    `json:"id"`
	Name           string    `json:"name,omitempty"`
	Channel        string    `json:"channel"`
	MessagePreview string    `json:"messagePreview,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	Total          int       `json:"total"`
}

// LogEntry — app log row.
type LogEntry struct {
	ID     int64     `json:"id"`
	TS     time.Time `json:"ts"`
	Level  string    `json:"level"`  // INFO/WARN/ERROR
	Source string    `json:"source"` // telegram/whatsapp/cascade/ui
	Msg    string    `json:"msg"`
}
