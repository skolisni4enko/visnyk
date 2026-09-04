package cascade

import "time"

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
