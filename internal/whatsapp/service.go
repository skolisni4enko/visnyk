package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"

	"visnyk/internal/cascade"
	"visnyk/internal/common"
	"visnyk/internal/format"
	"visnyk/internal/paths"
)

// Service implements cascade.Messenger for WhatsApp via whatsmeow.
type Service struct {
	mu        sync.Mutex
	client    *whatsmeow.Client
	container *sqlstore.Container
	dbPath    string

	qrChan    chan string
	qrPNGChan chan []byte
	// latest QR kept for polling frontend — fixes stale QR issue where phone scan fails
	latestQR    string
	latestQRPNG []byte
	latestQRErr string
	connected   bool
	loggedIn    bool
}

func whatsappDefaultPath() string {
	if p := os.Getenv("VISNYK_DATA_DIR"); p != "" {
		return filepath.Join(p, "whatsapp", "store.db")
	}
	return paths.WhatsAppDBPath()
}

// New creates a WhatsApp service. dbPath may be empty to use default.
// Use NewWithDB for tests with ":memory:".
func New(dbPath string) (*Service, error) {
	if dbPath == "" {
		dbPath = whatsappDefaultPath()
	} else if !filepath.IsAbs(dbPath) {
		// if relative, resolve via AbsPath for backward compat
		if abs, err := filepath.Abs(dbPath); err == nil {
			dbPath = abs
		}
	}
	return NewWithDB(dbPath)
}

// NewWithDB creates service with explicit SQLite path.
func NewWithDB(dbPath string) (*Service, error) {
	return &Service{
		dbPath:    dbPath,
		qrChan:    make(chan string, 1),
		qrPNGChan: make(chan []byte, 1),
	}, nil
}

// Connect initializes whatsmeow client and starts connection.
// If already logged in (session exists), it connects without QR.
// Otherwise it emits QR codes via QRChannel()/QRPNGChannel().
func (s *Service) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil && s.client.IsConnected() {
		return nil
	}

	if err := common.EnsureDir(s.dbPath); err != nil {
		return fmt.Errorf("whatsapp mkdir: %w", err)
	}
	dbLog := waLog.Stdout("Database", "WARN", true)
	container, err := sqlstore.New(ctx, "sqlite", fmt.Sprintf("file:%s?_foreign_keys=on", s.dbPath), dbLog)
	if err != nil {
		return fmt.Errorf("whatsapp store: %w", err)
	}
	s.container = container

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp device: %w", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	s.client = whatsmeow.NewClient(deviceStore, clientLog)
	s.client.AddEventHandler(s.eventHandler)

	if s.client.Store.ID == nil {
		// No session — need QR pairing.
		qrChan, err := s.client.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("whatsapp qr channel: %w", err)
		}
		if err := s.client.Connect(); err != nil {
			return fmt.Errorf("whatsapp connect: %w", err)
		}
		go s.forwardQR(qrChan)
	} else {
		// Existing session — just connect.
		if err := s.client.Connect(); err != nil {
			return fmt.Errorf("whatsapp connect: %w", err)
		}
		// Wait briefly for connection.
		for i := 0; i < 50; i++ {
			if s.client.IsConnected() {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	s.connected = true
	return nil
}

func (s *Service) forwardQR(in <-chan whatsmeow.QRChannelItem) {
	for item := range in {
		if item.Event == "code" {
			png, _ := common.EncodeQR(item.Code)
			s.mu.Lock()
			s.latestQR = item.Code
			s.latestQRPNG = png
			s.latestQRErr = ""
			s.mu.Unlock()
			// also try to send to channels (non-blocking, for legacy)
			select {
			case s.qrChan <- item.Code:
			default:
				// drain and replace to keep latest
				select {
				case <-s.qrChan:
				default:
				}
				s.qrChan <- item.Code
			}
			if png != nil {
				select {
				case s.qrPNGChan <- png:
				default:
					select {
					case <-s.qrPNGChan:
					default:
					}
					s.qrPNGChan <- png
				}
			}
		} else if item.Event == "success" {
			s.mu.Lock()
			s.loggedIn = true
			s.connected = true
			s.latestQRErr = ""
			s.mu.Unlock()
		} else if item.Event == "error" || item.Event == "timeout" {
			s.mu.Lock()
			s.latestQRErr = item.Event
			if item.Error != nil {
				s.latestQRErr = item.Error.Error()
			}
			s.mu.Unlock()
		}
	}
}

// GetLatestQR returns the most recent QR code string and PNG, plus any error.
// Frontend should poll this instead of channels to avoid missing rotated QR (20s expiry).
func (s *Service) GetLatestQR() (string, []byte, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestQR, s.latestQRPNG, s.latestQRErr
}

// RequestPairCode generates an 8-digit pairing code for phone number login.
// Phone may be +380..., 380..., or 099... — normalized to international without +.
// Usage: Connect() first, wait for QR, then call this, then on phone: WhatsApp → Linked devices → Link with phone number → enter code.
// Must be called within ~160s of Connect() (QR lifetime).
func (s *Service) RequestPairCode(ctx context.Context, phone string) (string, error) {
	s.mu.Lock()
	connected := s.client != nil && s.client.IsConnected()
	hasQR := s.latestQR != ""
	s.mu.Unlock()
	if !connected {
		return "", fmt.Errorf("not connected: press Connect first and wait for QR")
	}
	// Wait briefly for QR to ensure websocket is fully established (PairPhone requires it)
	// But don't fail if QR not yet arrived — socket may be ready earlier. Wait max 4s for either QR or socket.
	if !hasQR {
		for i := 0; i < 20; i++ {
			time.Sleep(200 * time.Millisecond)
			s.mu.Lock()
			hasQR = s.latestQR != ""
			connected = s.client != nil && s.client.IsConnected()
			s.mu.Unlock()
			if hasQR && connected {
				break
			}
			if connected && i >= 5 {
				// socket ready even without QR — allow PairPhone
				break
			}
		}
		// Don't hard-fail on missing QR — PairPhone can work without it if socket connected
		if !connected {
			return "", fmt.Errorf("QR not ready: click Connect and wait 3s until QR appears, then request code again (hasQR=%v connected=%v)", hasQR, connected)
		}
	}
	origPhone := phone
	phone = common.CleanPhone(phone)
	if phone == "" {
		return "", fmt.Errorf("empty phone (got %q)", origPhone)
	}
	if len(phone) <= 6 {
		return "", fmt.Errorf("phone too short: %q (%d digits) — need +380...", phone, len(phone))
	}
	if strings.HasPrefix(phone, "0") {
		return "", fmt.Errorf("use international format +380... not 0... (got %q)", origPhone)
	}
	// Pair with Chrome (Windows) — most compatible, server validates Browser (OS) format
	// Debug: log full phone and context
	fmt.Printf("[whatsapp] PairPhone request phone=%q orig=%q ctx=%v\n", phone, origPhone, ctx.Err())
	code, err := s.client.PairPhone(ctx, phone, true, "1", "Chrome (Windows)")
	if err != nil {
		// Provide detailed diagnostics for UI — include type and full error
		fmt.Printf("[whatsapp] PairPhone failed phone=%q err=%T: %v\n", phone, err, err)
		return "", fmt.Errorf("pair phone %q failed: %T: %v", phone, err, err)
	}
	fmt.Printf("[whatsapp] PairPhone success code=%q phone=%q\n", code, phone)
	return code, nil
}

func (s *Service) eventHandler(evt interface{}) {
	switch e := evt.(type) {
	case *events.PairSuccess:
		s.mu.Lock()
		s.loggedIn = true
		s.connected = true
		s.latestQRErr = ""
		s.mu.Unlock()
		fmt.Printf("[whatsapp] PairSuccess ID=%s\n", e.ID.String())
	case *events.Connected:
		s.mu.Lock()
		s.connected = true
		if s.client != nil && s.client.IsLoggedIn() {
			s.loggedIn = true
		}
		s.mu.Unlock()
		fmt.Printf("[whatsapp] Connected IsLoggedIn=%v\n", s.client != nil && s.client.IsLoggedIn())
		_ = e
	case *events.LoggedOut:
		s.mu.Lock()
		s.loggedIn = false
		s.connected = false
		s.mu.Unlock()
		fmt.Printf("[whatsapp] LoggedOut reason=%v\n", e.Reason)
	case *events.Disconnected:
		s.mu.Lock()
		s.connected = false
		s.mu.Unlock()
		fmt.Printf("[whatsapp] Disconnected\n")
		_ = e
	}
	_ = evt
}

// QRChannel returns a channel emitting raw QR string codes (for custom rendering).
func (s *Service) QRChannel() <-chan string { return s.qrChan }

// QRPNGChannel returns a channel emitting QR PNG bytes (256x256), ready for Wails frontend.
func (s *Service) QRPNGChannel() <-chan []byte { return s.qrPNGChan }

// IsLoggedIn reports whether the pairing succeeded.
func (s *Service) IsLoggedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loggedIn {
		return true
	}
	if s.client != nil && s.client.IsLoggedIn() {
		return true
	}
	if s.client != nil && s.client.Store.ID != nil {
		return true
	}
	return false
}

// IsConnected reports transport connectivity.
func (s *Service) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.client.IsConnected() {
		return true
	}
	return s.connected
}

// Logout removes session and deletes DB file (full logout).
func (s *Service) Logout(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		if s.client.IsLoggedIn() {
			if err := s.client.Logout(ctx); err != nil {
				fmt.Printf("[whatsapp] Logout error: %v\n", err)
			}
		}
		s.client.Disconnect()
	}
	if s.container != nil {
		_ = s.container.Close()
		s.container = nil
		s.client = nil
	}
	s.loggedIn = false
	s.connected = false
	s.latestQR = ""
	s.latestQRPNG = nil
	s.latestQRErr = ""
	// delete DB files (sqlite creates -wal/-shm)
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(s.dbPath + suffix)
	}
	fmt.Printf("[whatsapp] Logged out, DB removed %s\n", s.dbPath)
	return nil
}

// Name returns cascade channel identifier.
func (s *Service) Name() cascade.Channel { return cascade.ChannelWhatsApp }

// IsAvailable checks if phone is on WhatsApp.
// phone must be E.164 e.g. "+380991234567".
func (s *Service) IsAvailable(phone string) (bool, error) {
	return s.IsOnWhatsApp(phone)
}

// IsOnWhatsApp checks via whatsmeow.IsOnWhatsApp.
func (s *Service) IsOnWhatsApp(phone string) (bool, error) {
	if s.client == nil || !s.client.IsConnected() {
		return false, fmt.Errorf("whatsapp not connected")
	}
	jid, err := parseJID(phone)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := s.client.IsOnWhatsApp(ctx, []string{jid.String()})
	if err != nil {
		return false, fmt.Errorf("IsOnWhatsApp: %w", err)
	}
	if len(result) == 0 {
		return false, nil
	}
	return result[0].IsIn, nil
}

// Send sends a text message to phone (E.164). Accepts HTML from Quill editor — converts to WhatsApp markdown so appearance is identical with Telegram.
func (s *Service) Send(phone, message string) error {
	if message != "" {
		// use format helper; if message is HTML, convert, else keep plain
		// import is dynamic to avoid cycle — call via helper function
		message = toWhatsAppMessage(message)
	}
	return s.sendConvertedText(phone, message)
}

// sendConvertedText delivers already-converted WhatsApp markdown.
func (s *Service) sendConvertedText(phone, md string) error {
	if s.client == nil || !s.client.IsConnected() {
		return fmt.Errorf("whatsapp not connected")
	}
	jid, err := parseJID(phone)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_ = ctx
	msg := &waProto.Message{
		Conversation: &md,
	}
	_, err = s.client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}

func toWhatsAppMessage(in string) string {
	if len(in) == 0 {
		return in
	}
	// HTMLToWhatsApp already handles plain text (postProcess spaces/dedup), so always route through it
	return format.HTMLToWhatsApp(in)
}

// Disconnect gracefully disconnects and closes the store.
func (s *Service) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Disconnect()
	}
	if s.container != nil {
		_ = s.container.Close()
	}
	s.connected = false
}

// DBPath returns the sqlite path (useful for Wails).
func (s *Service) DBPath() string { return s.dbPath }

// GetPhone returns the linked WhatsApp account phone (JID user part) if logged in, empty otherwise.
// Stored in whatsapp-store/whatsapp.db and removed on Logout (account/phone change).
func (s *Service) GetPhone() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.client.Store.ID != nil {
		return "+" + s.client.Store.ID.User
	}
	return ""
}

// ClearDB removes session DB without needing a live client — for phone/account change fallback
func (s *Service) ClearDB() error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(s.dbPath + suffix)
	}
	return nil
}

// Ensure sql import is used.
var _ = sql.ErrNoRows

// parseJID converts E.164 phone to WhatsApp JID.
// "+380991234567" -> "380991234567@s.whatsapp.net"
func parseJID(phone string) (types.JID, error) {
	phone = common.CleanPhone(phone)
	if phone == "" {
		return types.JID{}, fmt.Errorf("empty phone")
	}
	// Basic digit check.
	for _, r := range phone {
		if r < '0' || r > '9' {
			return types.JID{}, fmt.Errorf("invalid phone characters: %s", phone)
		}
	}
	return types.NewJID(phone, types.DefaultUserServer), nil
}
