package cascade

import (
	"context"
	"time"
)

// Contact is one applicant row from CSV/XLSX.
type Contact struct {
	Name            string
	PhoneRaw        string
	NormalizedPhone string
}

// Channel is the messenger where the message was delivered.
type Channel string

const (
	ChannelWhatsApp Channel = "whatsapp"
	ChannelTelegram Channel = "telegram"
	ChannelViber    Channel = "viber"
	ChannelNone     Channel = "none"
)

// SendResult is the outcome for a single contact.
type SendResult struct {
	Contact Contact   `json:"contact"`
	Channel Channel   `json:"channel"`
	Status  string    `json:"status"` // "sent", "skipped", "failed"
	Error   string    `json:"error"`
	SentAt  time.Time `json:"sentAt"`
}

// Progress is emitted per contact during cascade.
type Progress struct {
	Index      int       `json:"index"` // 1-based
	Total      int       `json:"total"`
	Contact    Contact   `json:"contact"`
	Channel    Channel   `json:"channel"`
	Status     string    `json:"status"` // "checking", "sending", "sent", "failed", "skipped"
	Error      string    `json:"error,omitempty"`
	SentAt     time.Time `json:"sentAt,omitempty"`
	ETASeconds int       `json:"etaSeconds,omitempty"`
}

// Messenger is the interface all messenger services implement.
type Messenger interface {
	Name() Channel
	IsAvailable(phone string) (bool, error)
	Send(phone, message string) error
}

// DirectSender is an optional fast path: resolve + send in a single call
// (e.g. Telegram via contacts.resolvePhone) instead of IsAvailable+Send.
// sendOne prefers it when the messenger implements it — half the API calls.
type DirectSender interface {
	Messenger
	ResolveAndSend(phone, message string) error
}

// BatchSender is an optional batch path for Telegram: import all contacts
// in chunks, send to all resolved users, then delete the temporary
// contacts in one batch. Avoids per-contact "Test" pollution and
// reduces API calls from N*2 to ~N/20 imports.
type BatchSender interface {
	Messenger
	// BatchSend imports phones in chunks, sends message to each found user,
	// and deletes the temporary contacts. Returns per-phone error (nil = sent).
	BatchSend(ctx context.Context, phones []string, message string) (map[string]error, error)
}

// BatchImporter is the hybrid variant: batch import only, per-contact
// paced send via paceChannel, then batch delete. This keeps the import
// benefit (no per-contact "Test") but respects per-message pacing (8-15s
// capped at 30s) so PEER_FLOOD is avoided.
type BatchImporter interface {
	Messenger
	BatchImport(ctx context.Context, phones []string) (map[string]bool, []ImportedContact, error)
	BatchDelete(ctx context.Context, toDelete []ImportedContact) error
}

// ImportedContact is a minimal handle for batch delete, avoids importing tg.
type ImportedContact struct {
	UserID     int64
	AccessHash int64
}
