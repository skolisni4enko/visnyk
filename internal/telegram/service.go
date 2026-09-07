package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"visnyk/internal/crypto"
	"visnyk/internal/paths"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	tgmessage "github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"visnyk/internal/cascade"
	"visnyk/internal/common"
	"visnyk/internal/format"
)

// Service implements cascade.Messenger for Telegram via gotd/td.
type Service struct {
	mu sync.Mutex

	appID       int
	appHash     string
	phone       string
	sessionPath string
	client      *telegram.Client
	codeChan    chan string
	pwdChan     chan string
	pwdNeeded   bool
	cancel      context.CancelFunc
	connected   bool
	loggedIn    bool

	// QR login
	qrToken string
	qrPNG   []byte
	qrErr   string

	lastCodeType string
	lastErr      string

	// ResolvePhone rate limiting: Telegram docs require at most
	// 1 contacts.resolvePhone call per 3 seconds (client-side).
	resolveMu   sync.Mutex
	lastResolve time.Time
}

func resolveSessionPath() string {
	if p := os.Getenv("VISNYK_DATA_DIR"); p != "" {
		return filepath.Join(p, "telegram", "session.json")
	}
	return paths.TelegramSessionPath()
}

func resolveConfigPath() string {
	if p := os.Getenv("VISNYK_DATA_DIR"); p != "" {
		return filepath.Join(p, "telegram", "config.json")
	}
	return paths.TelegramConfigPath()
}

// kept for backward compat where old code referenced these vars directly
var defaultSessionPath = paths.TelegramSessionPath()
var configPath = paths.TelegramConfigPath()

func absPath(p string) string { return common.AbsPath(p) }

// tgConfig persists api_id/hash + phone so user enters once and can switch account.
// phone is stored to display current account and is removed on Logout/account change.
type tgConfig struct {
	AppID   int    `json:"app_id"`
	AppHash string `json:"app_hash"`
	Phone   string `json:"phone,omitempty"`
}

func loadConfig() (int, string) {
	id, hash, _ := loadFullConfig()
	return id, hash
}

func getCryptoForConfig() (*crypto.Manager, string) {
	// DataDir is parent of telegram dir
	cfgPath := resolveConfigPath()
	dataDir := filepath.Dir(filepath.Dir(cfgPath))
	// fallback to paths.DataDir()
	if dataDir == "." || dataDir == "/" {
		dataDir = paths.DataDir()
	}
	cm, err := crypto.New(dataDir)
	if err != nil {
		return nil, cfgPath
	}
	return cm, cfgPath
}

func decryptHashIfNeeded(hash string, cm *crypto.Manager) string {
	if hash == "" || cm == nil {
		return hash
	}
	dec, err := cm.Decrypt(hash)
	if err != nil {
		return hash
	}
	// if decrypted equals original and original was plain, keep plain for migration
	if dec == hash {
		// try to detect if hash was encrypted (base64 and decrypt succeeded but same) — return as is
		return hash
	}
	// Heuristic: if decrypt succeeded and result looks like hex (api_hash is hex), use it
	// If hash was plain, Decrypt returns plain itself (via fallback), so we get plain
	return dec
}

func loadFullConfig() (int, string, string) {
	cfgPath := resolveConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return 0, "", ""
	}
	var c tgConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return 0, "", ""
	}
	// Decrypt api_hash if encrypted
	if c.AppHash != "" {
		if cm, _ := getCryptoForConfig(); cm != nil {
			if dec, err := cm.Decrypt(c.AppHash); err == nil && dec != "" {
				c.AppHash = dec
			}
		}
	}
	// Phone is stored encrypted if present (optional)
	if c.Phone != "" {
		if cm, _ := getCryptoForConfig(); cm != nil {
			if dec, err := cm.Decrypt(c.Phone); err == nil && dec != "" {
				// If decrypt returns different, use it; else keep plain (migration)
				if dec != c.Phone {
					c.Phone = dec
				}
			}
		}
	}
	return c.AppID, c.AppHash, c.Phone
}

func saveConfig(appID int, appHash string) {
	saveFullConfig(appID, appHash, "")
}

func saveFullConfig(appID int, appHash, phone string) {
	cfgPath := resolveConfigPath()
	_ = common.EnsureDir(cfgPath)
	// preserve existing phone if not provided
	if phone == "" {
		_, _, existingPhone := loadFullConfig()
		if existingPhone != "" {
			phone = existingPhone
		}
	}
	encHash := appHash
	encPhone := phone
	if cm, _ := getCryptoForConfig(); cm != nil {
		if e, err := cm.Encrypt(appHash); err == nil && e != "" {
			encHash = e
		}
		if phone != "" {
			if e, err := cm.Encrypt(phone); err == nil && e != "" {
				encPhone = e
			}
		}
	}
	data, _ := json.Marshal(tgConfig{AppID: appID, AppHash: encHash, Phone: encPhone})
	_ = os.WriteFile(cfgPath, data, 0600)
}

func savePhone(phone string) {
	id, hash, _ := loadFullConfig()
	if id == 0 && hash == "" {
		return
	}
	cfgPath := resolveConfigPath()
	_ = common.EnsureDir(cfgPath)
	encHash := hash
	encPhone := phone
	if cm, _ := getCryptoForConfig(); cm != nil {
		if e, err := cm.Encrypt(hash); err == nil && e != "" {
			encHash = e
		}
		if e, err := cm.Encrypt(phone); err == nil && e != "" {
			encPhone = e
		}
	}
	data, _ := json.Marshal(tgConfig{AppID: id, AppHash: encHash, Phone: encPhone})
	_ = os.WriteFile(cfgPath, data, 0600)
}

// ClearConfig deletes config file (api_id/hash/phone) — for full account switch
func ClearConfig() error {
	cfgPath := resolveConfigPath()
	if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// tgAuth implements auth.UserAuthenticator with code + 2FA password via channels
type tgAuth struct {
	phone string
	svc   *Service
}

func (a tgAuth) Phone(ctx context.Context) (string, error) { return a.phone, nil }

func (a tgAuth) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	typeStr := fmt.Sprintf("%T", sentCode.Type)
	a.svc.mu.Lock()
	a.svc.lastCodeType = typeStr
	a.svc.mu.Unlock()
	fmt.Printf("[telegram] code requested type=%v (%s) next=%v phoneCodeHash=%s\n", sentCode.Type, typeStr, sentCode.NextType, sentCode.PhoneCodeHash)
	select {
	case code := <-a.svc.codeChan:
		return strings.TrimSpace(code), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Minute):
		return "", fmt.Errorf("code timeout")
	}
}

func (a tgAuth) Password(ctx context.Context) (string, error) {
	a.svc.mu.Lock()
	a.svc.pwdNeeded = true
	a.svc.mu.Unlock()
	fmt.Printf("[telegram] 2FA password requested — waiting for user input\n")
	select {
	case pwd := <-a.svc.pwdChan:
		a.svc.mu.Lock()
		a.svc.pwdNeeded = false
		a.svc.mu.Unlock()
		return strings.TrimSpace(pwd), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		a.svc.mu.Lock()
		a.svc.pwdNeeded = false
		a.svc.mu.Unlock()
		return "", fmt.Errorf("password timeout")
	}
}

func (a tgAuth) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a tgAuth) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up not implemented")
}

// New creates Telegram service.
func New(appID int, appHash string) (*Service, error) {
	return NewWithSession(appID, appHash, resolveSessionPath())
}

// NewWithSession creates service with custom session path.
func NewWithSession(appID int, appHash, sessionPath string) (*Service, error) {
	// try load saved config if not provided
	if appID == 0 || appHash == "" {
		if id, hash := loadConfig(); id != 0 && hash != "" {
			appID = id
			appHash = hash
		}
	}
	if sessionPath == "" || sessionPath == defaultSessionPath || sessionPath == "telegram-store/session.json" {
		sessionPath = resolveSessionPath()
	}
	if sessionPath == "" {
		sessionPath = resolveSessionPath()
	}
	return &Service{
		appID:       appID,
		appHash:     appHash,
		sessionPath: sessionPath,
		codeChan:    make(chan string, 1),
		pwdChan:     make(chan string, 1),
	}, nil
}

// Configure sets api credentials and persists.
func (s *Service) Configure(appID int, appHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appID = appID
	s.appHash = appHash
	saveConfig(appID, appHash)
}

// GetConfig returns saved api credentials (for UI prefill)
func (s *Service) GetConfig() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appID != 0 && s.appHash != "" {
		fmt.Printf("[telegram] GetConfig from memory id=%d\n", s.appID)
		return s.appID, s.appHash
	}
	id, hash := loadConfig()
	fmt.Printf("[telegram] GetConfig from file id=%d hash_len=%d err=%v cwd=%s path=%s\n", id, len(hash), nil, func() string { wd, _ := os.Getwd(); return wd }(), resolveConfigPath())
	return id, hash
}

// GetPhone returns saved phone (persisted in config.json)
func (s *Service) GetPhone() string {
	s.mu.Lock()
	if s.phone != "" {
		p := s.phone
		s.mu.Unlock()
		return p
	}
	s.mu.Unlock()
	_, _, phone := loadFullConfig()
	return phone
}

// GetFullConfig returns all persisted fields (for UI prefill + display)
func (s *Service) GetFullConfig() (int, string, string) {
	s.mu.Lock()
	if s.appID != 0 && s.appHash != "" {
		p := s.phone
		id, hash := s.appID, s.appHash
		s.mu.Unlock()
		if p == "" {
			_, _, filePhone := loadFullConfig()
			p = filePhone
		}
		return id, hash, p
	}
	s.mu.Unlock()
	return loadFullConfig()
}

// ClearStoredConfig deletes api_id/hash/phone from disk (for key change)
func (s *Service) ClearStoredConfig() error {
	s.mu.Lock()
	s.appID = 0
	s.appHash = ""
	s.phone = ""
	s.mu.Unlock()
	return ClearConfig()
}

// Connect starts client and auth. Phone must be +380...
func (s *Service) Connect(ctx context.Context, phone string) error {
	s.mu.Lock()
	if s.client != nil && s.connected {
		s.mu.Unlock()
		return nil
	}
	if s.appID == 0 || s.appHash == "" {
		s.mu.Unlock()
		return fmt.Errorf("api_id/api_hash not set: get from https://my.telegram.org")
	}
	phone = normalizePhone(phone)
	if phone == "" {
		s.mu.Unlock()
		return fmt.Errorf("empty phone")
	}
	// If previous Run still waiting for code/QR, cancel it first — avoid two concurrent Runs on same session file
	if s.client != nil && !s.connected {
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		// small pause to let old Run exit and release session file lock
		time.Sleep(300 * time.Millisecond)
	}
	s.phone = phone
	s.codeChan = make(chan string, 1)
	if s.pwdChan == nil {
		s.pwdChan = make(chan string, 1)
	} else {
		// clear stale pwd
		select {
		case <-s.pwdChan:
		default:
		}
	}
	s.pwdNeeded = false
	s.lastErr = ""
	s.mu.Unlock()

	// persist phone for account display and cleanup on change
	savePhone(phone)

	if err := common.EnsureDir(s.sessionPath); err != nil {
		return fmt.Errorf("telegram mkdir: %w", err)
	}

	opts := telegram.Options{
		SessionStorage: &session.FileStorage{Path: s.sessionPath},
	}
	client := telegram.NewClient(s.appID, s.appHash, opts)
	// IMPORTANT: Run context must NOT be child of short-lived caller ctx (e.g. 30s timeout in ui/app.go)
	// otherwise `defer cancel()` in caller kills Run after 500ms and codeAuth gets `context canceled`.
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.client = client
	s.cancel = cancel
	s.mu.Unlock()
	_ = ctx // caller ctx only for validation, not for Run lifetime

	go func() {
		err := client.Run(runCtx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if status.Authorized {
				s.mu.Lock()
				s.loggedIn = true
				s.connected = true
				s.mu.Unlock()
				fmt.Printf("[telegram] already authorized\n")
				<-runCtx.Done()
				return nil
			}
			flow := auth.NewFlow(
				tgAuth{phone: phone, svc: s},
				auth.SendCodeOptions{},
			)
			if err := flow.Run(ctx, client.Auth()); err != nil {
				// If password invalid, allow retry instead of exiting Run
				lower := strings.ToLower(err.Error())
				if strings.Contains(lower, "invalid password") || strings.Contains(lower, "password") && strings.Contains(err.Error(), "401") {
					s.mu.Lock()
					s.lastErr = "invalid password — try again: " + err.Error()
					s.pwdNeeded = true
					s.mu.Unlock()
					fmt.Printf("[telegram] auth failed (need retry): %v\n", err)
					for {
						fmt.Printf("[telegram] waiting for retry password (invalid)\n")
						var pwd string
						select {
						case pwd = <-s.pwdChan:
						case <-ctx.Done():
							return ctx.Err()
						case <-runCtx.Done():
							return runCtx.Err()
						case <-time.After(5 * time.Minute):
							s.mu.Lock()
							s.pwdNeeded = false
							s.lastErr = "password timeout"
							s.mu.Unlock()
							return fmt.Errorf("password timeout")
						}
						s.mu.Lock()
						s.pwdNeeded = false
						s.mu.Unlock()
						pwd = strings.TrimSpace(pwd)
						if _, err2 := client.Auth().Password(ctx, pwd); err2 == nil {
							s.mu.Lock()
							s.loggedIn = true
							s.connected = true
							s.lastErr = ""
							s.mu.Unlock()
							fmt.Printf("[telegram] auth success after retry phone=%s\n", phone)
							<-runCtx.Done()
							return nil
						} else {
							lower2 := strings.ToLower(err2.Error())
							if strings.Contains(lower2, "invalid password") || strings.Contains(err2.Error(), "401") || strings.Contains(err2.Error(), "PASSWORD") {
								s.mu.Lock()
								s.lastErr = "invalid password — try again: " + err2.Error()
								s.pwdNeeded = true
								s.mu.Unlock()
								fmt.Printf("[telegram] retry password failed (invalid): %v\n", err2)
								continue
							}
							s.mu.Lock()
							s.lastErr = err2.Error()
							s.mu.Unlock()
							fmt.Printf("[telegram] retry password failed: %v\n", err2)
							return err2
						}
					}
				}
				s.mu.Lock()
				s.lastErr = err.Error()
				s.mu.Unlock()
				fmt.Printf("[telegram] auth failed: %v\n", err)
				return err
			}
			s.mu.Lock()
			s.loggedIn = true
			s.connected = true
			s.mu.Unlock()
			fmt.Printf("[telegram] auth success phone=%s\n", phone)
			<-runCtx.Done()
			return nil
		})
		if err != nil {
			fmt.Printf("[telegram] Run exited: %v\n", err)
			s.mu.Lock()
			s.connected = false
			s.mu.Unlock()
		}
	}()

	time.Sleep(500 * time.Millisecond)
	return nil
}

func (s *Service) codeAuth(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	typeStr := fmt.Sprintf("%T", sentCode.Type)
	s.mu.Lock()
	s.lastCodeType = typeStr
	s.mu.Unlock()
	fmt.Printf("[telegram] code requested type=%v (%s) next=%v phoneCodeHash=%s\n", sentCode.Type, typeStr, sentCode.NextType, sentCode.PhoneCodeHash)
	select {
	case code := <-s.codeChan:
		return strings.TrimSpace(code), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Minute):
		return "", fmt.Errorf("code timeout")
	}
}

// GetLastCodeType returns where code was sent (for UI hint)
func (s *Service) GetLastCodeType() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCodeType
}

// IsPasswordNeeded reports if 2FA password is required
func (s *Service) IsPasswordNeeded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pwdNeeded
}

// ProvidePassword sends 2FA password
func (s *Service) ProvidePassword(pwd string) error {
	pwd = strings.TrimSpace(pwd)
	if pwd == "" {
		return fmt.Errorf("empty password")
	}
	select {
	case s.pwdChan <- pwd:
		return nil
	default:
		return fmt.Errorf("password channel busy")
	}
}

// GetLastError returns last auth error (for UI)
func (s *Service) GetLastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// ProvideCode sends SMS code
func (s *Service) ProvideCode(code string) error {
	select {
	case s.codeChan <- strings.TrimSpace(code):
		return nil
	default:
		return fmt.Errorf("code channel busy")
	}
}

// IsLoggedIn reports auth
func (s *Service) IsLoggedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loggedIn
}

// IsConnected reports transport
func (s *Service) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// ConnectQR starts QR login flow. apiID/apiHash must be configured.
func (s *Service) ConnectQR(ctx context.Context) error {
	s.mu.Lock()
	if s.client != nil && s.connected {
		s.mu.Unlock()
		return nil
	}
	if s.appID == 0 || s.appHash == "" {
		s.mu.Unlock()
		return fmt.Errorf("api_id/api_hash not set")
	}
	if s.client != nil && !s.connected {
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	s.mu.Unlock()

	if err := common.EnsureDir(s.sessionPath); err != nil {
		return fmt.Errorf("telegram mkdir: %w", err)
	}

	dispatcher := tg.NewUpdateDispatcher()
	loggedInCh := qrlogin.OnLoginToken(dispatcher)

	opts := telegram.Options{
		SessionStorage: &session.FileStorage{Path: s.sessionPath},
		UpdateHandler:  dispatcher,
	}
	client := telegram.NewClient(s.appID, s.appHash, opts)
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.client = client
	s.cancel = cancel
	s.qrToken = ""
	s.qrPNG = nil
	s.qrErr = ""
	s.mu.Unlock()
	_ = ctx

	go func() {
		err := client.Run(runCtx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if status.Authorized {
				s.mu.Lock()
				s.loggedIn = true
				s.connected = true
				s.mu.Unlock()
				fmt.Printf("[telegram] already authorized (QR)\n")
				<-runCtx.Done()
				return nil
			}
			qr := client.QR()
			_, err = qr.Auth(ctx, loggedInCh, func(ctx context.Context, token qrlogin.Token) error {
				url := token.URL()
				png, _ := common.EncodeQR(url)
				s.mu.Lock()
				s.qrToken = url
				s.qrPNG = png
				s.qrErr = ""
				s.mu.Unlock()
				fmt.Printf("[telegram] QR generated %s expires %v\n", url, token.Expires())
				return nil
			})
			if err != nil {
				// If 2FA password needed after QR scan, handle it with retry loop
				if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") || strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") {
					for {
						s.mu.Lock()
						s.pwdNeeded = true
						if s.lastErr == "" || !strings.Contains(s.lastErr, "invalid password") {
							s.lastErr = "2FA password required (QR)"
							s.qrErr = "2FA password required — enter cloud password below"
						}
						s.mu.Unlock()
						fmt.Printf("[telegram] QR 2FA password requested — waiting for user input\n")
						var pwd string
						select {
						case pwd = <-s.pwdChan:
						case <-ctx.Done():
							return ctx.Err()
						case <-runCtx.Done():
							return runCtx.Err()
						case <-time.After(5 * time.Minute):
							s.mu.Lock()
							s.pwdNeeded = false
							s.qrErr = "password timeout"
							s.mu.Unlock()
							return fmt.Errorf("password timeout")
						}
						s.mu.Lock()
						s.pwdNeeded = false
						s.mu.Unlock()
						pwd = strings.TrimSpace(pwd)
						if _, err := client.Auth().Password(ctx, pwd); err != nil {
							lower := strings.ToLower(err.Error())
							if strings.Contains(lower, "invalid password") || strings.Contains(err.Error(), "PASSWORD") || strings.Contains(err.Error(), "401") {
								s.mu.Lock()
								s.lastErr = "invalid password — try again: " + err.Error()
								s.qrErr = "invalid password — try again"
								s.pwdNeeded = true
								s.mu.Unlock()
								fmt.Printf("[telegram] QR password failed (invalid, retry): %v\n", err)
								continue
							}
							s.mu.Lock()
							s.lastErr = err.Error()
							s.qrErr = "password failed: " + err.Error()
							s.mu.Unlock()
							fmt.Printf("[telegram] QR password failed: %v\n", err)
							return err
						}
						fmt.Printf("[telegram] QR password success\n")
						break
					}
				} else {
					s.mu.Lock()
					s.qrErr = err.Error()
					s.lastErr = err.Error()
					s.mu.Unlock()
					fmt.Printf("[telegram] QR auth failed: %v\n", err)
					return err
				}
			}
			s.mu.Lock()
			s.loggedIn = true
			s.connected = true
			s.qrErr = ""
			s.lastErr = ""
			s.pwdNeeded = false
			s.mu.Unlock()
			fmt.Printf("[telegram] QR auth success\n")
			<-runCtx.Done()
			return nil
		})
		if err != nil {
			fmt.Printf("[telegram] QR Run exited: %v\n", err)
			s.mu.Lock()
			s.connected = false
			if s.qrErr == "" {
				s.qrErr = err.Error()
			}
			s.mu.Unlock()
		}
	}()

	time.Sleep(600 * time.Millisecond)
	return nil
}

// GetLatestQR returns Telegram QR URL and PNG for frontend
func (s *Service) GetLatestQR() (string, []byte, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.qrToken, s.qrPNG, s.qrErr
}

// Name returns channel
func (s *Service) Name() cascade.Channel { return cascade.ChannelTelegram }

// IsAvailable checks via resolve (no address-book pollution), falls back
// to a single import only when the number hides behind privacy settings.
func (s *Service) IsAvailable(phone string) (bool, error) {
	return s.IsOnTelegram(phone)
}

// waitResolveSlot enforces min 3s between contacts.resolvePhone calls.
func (s *Service) waitResolveSlot(ctx context.Context) error {
	s.resolveMu.Lock()
	wait := 3*time.Second - time.Since(s.lastResolve)
	if wait <= 0 {
		s.lastResolve = time.Now()
		s.resolveMu.Unlock()
		return nil
	}
	s.resolveMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
	}
	s.resolveMu.Lock()
	s.lastResolve = time.Now()
	s.resolveMu.Unlock()
	return nil
}

// resolveUser maps phone to Telegram user via contacts.resolvePhone.
// Unlike ImportContacts it never touches the address book, so no cleanup
// delete is needed afterwards. Returns common-classified errors.
func (s *Service) resolveUser(ctx context.Context, phone string) (*tg.User, error) {
	// Defense in depth: callers pass normalized phones, but CleanPhone is
	// idempotent so normalizing again is safe for direct callers.
	phone = normalizePhone(phone)
	if phone == "" {
		return nil, fmt.Errorf("empty phone")
	}
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return nil, fmt.Errorf("telegram not connected/logged in")
	}
	if err := s.waitResolveSlot(ctx); err != nil {
		return nil, err
	}
	peer, err := client.API().ContactsResolvePhone(ctx, phone)
	if err != nil {
		return nil, fmt.Errorf("ResolvePhone: %w", err)
	}
	for _, u := range peer.Users {
		if usr, ok := u.(*tg.User); ok {
			return usr, nil
		}
	}
	return nil, fmt.Errorf("user not found for %s", phone)
}

// importAndFind is the fallback for numbers hidden behind privacy settings
// (ResolvePhone returns PHONE_NOT_OCCUPIED). Single ImportContacts call;
// caller decides about cleanup.
func (s *Service) importAndFind(ctx context.Context, phone string) (*tg.User, *tg.ContactsImportedContacts, error) {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return nil, nil, fmt.Errorf("telegram not connected/logged in")
	}
	tmpName := phone
	if len(tmpName) > 20 {
		tmpName = tmpName[len(tmpName)-10:]
	}
	contacts := []tg.InputPhoneContact{
		{Phone: phone, FirstName: tmpName, LastName: ""},
	}
	res, err := client.API().ContactsImportContacts(ctx, contacts)
	if err != nil {
		return nil, nil, fmt.Errorf("ImportContacts: %w", err)
	}
	for _, u := range res.Users {
		if usr, ok := u.(*tg.User); ok {
			return usr, res, nil
		}
	}
	return nil, res, fmt.Errorf("user not found for %s", phone)
}

// IsOnTelegram checks via ContactsResolvePhone without polluting address book.
// Falls back to a single ImportContacts only for privacy-hidden numbers.
func (s *Service) IsOnTelegram(phone string) (bool, error) {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return false, fmt.Errorf("telegram not connected/logged in")
	}
	phone = normalizePhone(phone)
	if phone == "" {
		return false, fmt.Errorf("empty phone")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Resolve first: no address-book pollution, no cleanup delete needed.
	if _, err := s.resolveUser(ctx, phone); err == nil {
		return true, nil
	} else if !common.IsTelegramNotFound(err) && !isNotFoundText(err) {
		// Flood/auth/network errors must surface (and must NOT trigger
		// a fallback import — that would worsen a flood).
		return false, err
	}
	// Privacy-hidden number: single import fallback.
	usr, res, err := s.importAndFind(ctx, phone)
	if err != nil {
		if common.IsTelegramNotFound(err) || isNotFoundText(err) {
			return false, nil
		}
		return false, err
	}
	if usr == nil {
		return false, nil
	}
	// schedule cleanup of newly imported contacts (do not keep Test-like entries)
	if res != nil && len(res.Imported) > 0 {
		// delete only the contacts we just created — keeps book clean
		go s.cleanupImportedContacts(phone, res)
	}
	return true, nil
}

// isNotFoundText matches plain "user not found" (no RPC type to classify).
func isNotFoundText(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "user not found")
}

// cleanupImportedContacts deletes contacts that were just imported via ContactsImportContacts.
// It is called asynchronously after import to not block the check/send flow.
// Only deletes contacts that appear in res.Imported (newly created), preserving pre-existing ones.
func (s *Service) cleanupImportedContacts(phone string, res *tg.ContactsImportedContacts) {
	if res == nil || len(res.Imported) == 0 || len(res.Users) == 0 {
		return
	}
	// Build map of newly imported User IDs from Imported entries
	importedIDs := make(map[int64]bool, len(res.Imported))
	for _, ic := range res.Imported {
		importedIDs[ic.UserID] = true
	}
	var toDelete []tg.InputUserClass
	for _, u := range res.Users {
		if usr, ok := u.(*tg.User); ok {
			if importedIDs[usr.ID] {
				toDelete = append(toDelete, &tg.InputUser{UserID: usr.ID, AccessHash: usr.AccessHash})
			}
		}
	}
	if len(toDelete) == 0 {
		return
	}
	// Use background context with timeout for delete, independent from import ctx
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return
	}
	if _, err := client.API().ContactsDeleteContacts(ctx, toDelete); err != nil {
		fmt.Printf("[telegram] cleanup delete %s -> %v (users=%d)\n", phone, err, len(toDelete))
	} else {
		fmt.Printf("[telegram] cleanup delete %s ok (users=%d)\n", phone, len(toDelete))
	}
}

// Send sends text to phone. Resolve-first: no address-book pollution.
// Falls back to a single import only for privacy-hidden numbers.
func (s *Service) Send(phone, msgText string) error {
	return s.ResolveAndSend(phone, msgText)
}

// ResolveAndSend resolves the user once (no separate check call) and sends.
// Single hot-path call instead of IsAvailable+Send double import.
func (s *Service) ResolveAndSend(phone, msgText string) error {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return fmt.Errorf("telegram not connected/logged in")
	}
	phone = normalizePhone(phone)
	if phone == "" {
		return fmt.Errorf("empty phone")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var res *tg.ContactsImportedContacts
	user, err := s.resolveUser(ctx, phone)
	if err != nil {
		if !common.IsTelegramNotFound(err) && !isNotFoundText(err) {
			return err
		}
		// Privacy-hidden number: single import fallback.
		var ierr error
		user, res, ierr = s.importAndFind(ctx, phone)
		if ierr != nil {
			return fmt.Errorf("import: %w", ierr)
		}
	}
	if user == nil {
		return fmt.Errorf("user not found for %s", phone)
	}
	// remember if this user was newly imported — we will delete after send
	newlyImported := false
	if res != nil {
		for _, ic := range res.Imported {
			if ic.UserID == user.ID {
				newlyImported = true
				break
			}
		}
	}
	inputPeer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
	if err := s.sendToPeer(ctx, client, inputPeer, msgText); err != nil {
		return err
	}
	// cleanup: delete the temporary contact if we just created it (fallback path only)
	if newlyImported {
		uid, hash := user.ID, user.AccessHash
		go func() {
			cctx, ccancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer ccancel()
			s.mu.Lock()
			cl := s.client
			ok := s.connected && s.loggedIn
			s.mu.Unlock()
			if !ok || cl == nil {
				return
			}
			if _, derr := cl.API().ContactsDeleteContacts(cctx, []tg.InputUserClass{&tg.InputUser{UserID: uid, AccessHash: hash}}); derr != nil {
				fmt.Printf("[telegram] send cleanup delete %s -> %v\n", phone, derr)
			} else {
				fmt.Printf("[telegram] send cleanup delete %s ok\n", phone)
			}
		}()
	}
	return nil
}

// sendToPeer delivers styled/plain text to an already resolved peer.
func (s *Service) sendToPeer(ctx context.Context, client *telegram.Client, inputPeer *tg.InputPeerUser, msgText string) error {
	sender := tgmessage.NewSender(client.API())
	var err error
	if format.IsHTML(msgText) {
		htmlStr := format.HTMLToTelegram(msgText)
		_, err = sender.To(inputPeer).StyledText(ctx, html.String(nil, htmlStr))
	} else {
		_, err = sender.To(inputPeer).Text(ctx, msgText)
	}
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// BatchSend imports phones in chunks, sends message to each found user,
// and deletes temporary contacts in batches. This avoids per-contact "Test"
// pollution and reduces API calls from N*2 to ~N/20 imports.
// Phones must be E.164 (+380...). Returns per-phone error (nil = sent).
// All logs are English only.
func (s *Service) BatchSend(ctx context.Context, phones []string, msgText string) (map[string]error, error) {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return nil, fmt.Errorf("telegram not connected/logged in")
	}
	// Normalize and dedup
	normPhones := make([]string, 0, len(phones))
	seen := make(map[string]bool, len(phones))
	for _, p := range phones {
		n := normalizePhone(p)
		if n == "" {
			continue
		}
		if !seen[n] {
			seen[n] = true
			normPhones = append(normPhones, n)
		}
	}
	if len(normPhones) == 0 {
		return make(map[string]error), nil
	}

	const importChunk = 20
	const deleteChunk = 50

	phoneToUser := make(map[string]*tg.User, len(normPhones))
	var toDelete []tg.InputUserClass
	importedIDs := make(map[int64]bool)

	// --- Batch import in chunks ---
	for i := 0; i < len(normPhones); i += importChunk {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		end := i + importChunk
		if end > len(normPhones) {
			end = len(normPhones)
		}
		chunk := normPhones[i:end]
		contacts := make([]tg.InputPhoneContact, 0, len(chunk))
		for _, ph := range chunk {
			name := ph
			if len(name) > 20 {
				name = name[len(name)-10:]
			}
			contacts = append(contacts, tg.InputPhoneContact{Phone: ph, FirstName: name, LastName: ""})
		}
		// pacing between chunks (avoid flood)
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5*time.Second + time.Duration(rand.Int63n(2000))*time.Millisecond):
			}
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		res, err := client.API().ContactsImportContacts(cctx, contacts)
		cancel()
		if err != nil {
			if d, ok := common.FloodWaitDuration(err); ok {
				wait := d + 2*time.Second + time.Duration(rand.Int63n(int64(3*time.Second)))
				fmt.Printf("[telegram] BatchImport flood wait %v chunk %d-%d\n", wait, i, end)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(wait):
				}
				cctx2, cancel2 := context.WithTimeout(ctx, 20*time.Second)
				res, err = client.API().ContactsImportContacts(cctx2, contacts)
				cancel2()
			}
			if err != nil {
				if common.IsTelegramAuthError(err) {
					return nil, fmt.Errorf("batch import auth error: %w", err)
				}
				fmt.Printf("[telegram] BatchImport chunk %d-%d failed: %v\n", i, end, err)
				// mark all phones in chunk as failed (will be reported as not found)
				continue
			}
		}
		// Map users by phone for this chunk
		chunkUsers := make(map[string]*tg.User)
		for _, u := range res.Users {
			if usr, ok := u.(*tg.User); ok {
				ph := normalizePhone(usr.Phone)
				if ph != "" {
					chunkUsers[ph] = usr
				} else {
					// fallback: assign to first unmatched phone in chunk
					for _, cp := range chunk {
						if _, exists := chunkUsers[cp]; !exists && phoneToUser[cp] == nil {
							// check if this user corresponds to cp via Imported mapping
							for _, ic := range res.Imported {
								if ic.UserID == usr.ID {
									chunkUsers[cp] = usr
									break
								}
							}
							if chunkUsers[cp] != nil {
								break
							}
						}
					}
					// last resort: append to any unmatched
					if len(chunkUsers) < len(chunk) {
						for _, cp := range chunk {
							if _, ok := chunkUsers[cp]; !ok && phoneToUser[cp] == nil {
								chunkUsers[cp] = usr
								break
							}
						}
					}
				}
			}
		}
		for ph, usr := range chunkUsers {
			phoneToUser[ph] = usr
		}
		for _, ic := range res.Imported {
			if !importedIDs[ic.UserID] {
				importedIDs[ic.UserID] = true
				// find AccessHash from Users
				for _, u := range res.Users {
					if usr, ok := u.(*tg.User); ok && usr.ID == ic.UserID {
						toDelete = append(toDelete, &tg.InputUser{UserID: usr.ID, AccessHash: usr.AccessHash})
						break
					}
				}
			}
		}
		fmt.Printf("[telegram] BatchImport chunk %d-%d ok users=%d imported=%d\n", i, end, len(res.Users), len(res.Imported))
	}

	// --- Send to each resolved user ---
	results := make(map[string]error, len(normPhones))
	for _, ph := range normPhones {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}
		usr, ok := phoneToUser[ph]
		if !ok || usr == nil {
			results[ph] = fmt.Errorf("user not found for %s", ph)
			continue
		}
		peer := &tg.InputPeerUser{UserID: usr.ID, AccessHash: usr.AccessHash}
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.sendToPeer(sctx, client, peer, msgText)
		cancel()
		if err != nil {
			if d, ok := common.FloodWaitDuration(err); ok {
				wait := d + 2*time.Second + time.Duration(rand.Int63n(int64(3*time.Second)))
				fmt.Printf("[telegram] BatchSend flood wait %v for %s\n", wait, ph)
				select {
				case <-ctx.Done():
					results[ph] = ctx.Err()
					continue
				case <-time.After(wait):
				}
				sctx2, cancel2 := context.WithTimeout(ctx, 15*time.Second)
				err = s.sendToPeer(sctx2, client, peer, msgText)
				cancel2()
			}
			if err != nil {
				if common.IsTelegramSkippable(err) {
					results[ph] = fmt.Errorf("skipped: %w", err)
				} else {
					results[ph] = fmt.Errorf("failed to send: %w", err)
				}
				continue
			}
		}
		results[ph] = nil
		// per-message pacing for batch sends (8-15s, capped at 30s)
		select {
		case <-ctx.Done():
			break
		case <-time.After(8*time.Second + time.Duration(rand.Int63n(int64(7*time.Second)))):
		}
	}

	// --- Batch delete imported contacts ---
	if len(toDelete) > 0 {
		go func(ids []tg.InputUserClass) {
			for i := 0; i < len(ids); i += deleteChunk {
				end := i + deleteChunk
				if end > len(ids) {
					end = len(ids)
				}
				chunk := ids[i:end]
				cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				s.mu.Lock()
				cl := s.client
				ok := s.connected && s.loggedIn
				s.mu.Unlock()
				if !ok || cl == nil {
					cancel()
					return
				}
				if _, err := cl.API().ContactsDeleteContacts(cctx, chunk); err != nil {
					fmt.Printf("[telegram] BatchDelete chunk %d-%d failed: %v\n", i, end, err)
				} else {
					fmt.Printf("[telegram] BatchDelete chunk %d-%d ok users=%d\n", i, end, len(chunk))
				}
				cancel()
				if end < len(ids) {
					time.Sleep(1 * time.Second)
				}
			}
		}(toDelete)
	}

	return results, nil
}

// BatchImport imports phones in chunks (20 per chunk, 5s pacing between
// chunks) and returns a map of found phones and a list of imported contacts
// for batch delete. All logs are English only.
func (s *Service) BatchImport(ctx context.Context, phones []string) (map[string]bool, []cascade.ImportedContact, error) {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return nil, nil, fmt.Errorf("telegram not connected/logged in")
	}
	normPhones := make([]string, 0, len(phones))
	seen := make(map[string]bool, len(phones))
	for _, p := range phones {
		n := normalizePhone(p)
		if n == "" {
			continue
		}
		if !seen[n] {
			seen[n] = true
			normPhones = append(normPhones, n)
		}
	}
	if len(normPhones) == 0 {
		return make(map[string]bool), nil, nil
	}
	const importChunk = 20
	found := make(map[string]bool, len(normPhones))
	var toDelete []cascade.ImportedContact
	importedIDs := make(map[int64]bool)
	for i := 0; i < len(normPhones); i += importChunk {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		end := i + importChunk
		if end > len(normPhones) {
			end = len(normPhones)
		}
		chunk := normPhones[i:end]
		contacts := make([]tg.InputPhoneContact, 0, len(chunk))
		for _, ph := range chunk {
			name := ph
			if len(name) > 20 {
				name = name[len(name)-10:]
			}
			contacts = append(contacts, tg.InputPhoneContact{Phone: ph, FirstName: name, LastName: ""})
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(5*time.Second + time.Duration(rand.Int63n(2000))*time.Millisecond):
			}
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		res, err := client.API().ContactsImportContacts(cctx, contacts)
		cancel()
		if err != nil {
			if d, ok := common.FloodWaitDuration(err); ok {
				wait := d + 2*time.Second + time.Duration(rand.Int63n(int64(3*time.Second)))
				fmt.Printf("[telegram] BatchImport flood wait %v chunk %d-%d\n", wait, i, end)
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(wait):
				}
				cctx2, cancel2 := context.WithTimeout(ctx, 20*time.Second)
				res, err = client.API().ContactsImportContacts(cctx2, contacts)
				cancel2()
			}
			if err != nil {
				if common.IsTelegramAuthError(err) {
					return nil, nil, fmt.Errorf("batch import auth error: %w", err)
				}
				fmt.Printf("[telegram] BatchImport chunk %d-%d failed: %v\n", i, end, err)
				continue
			}
		}
		// Mark found phones
		for _, u := range res.Users {
			if usr, ok := u.(*tg.User); ok {
				ph := normalizePhone(usr.Phone)
				if ph != "" && seen[ph] {
					found[ph] = true
				}
			}
		}
		// Fallback: if Phone empty, assume chunk phones that are imported are found
		if len(found) < len(chunk) {
			// Use res.Imported to mark those that were newly imported as found
			for _, ic := range res.Imported {
				for _, u := range res.Users {
					if usr, ok := u.(*tg.User); ok && usr.ID == ic.UserID {
						// find corresponding chunk phone not yet marked
						for _, cp := range chunk {
							if !found[cp] {
								// heuristic: mark first unmatched as found for this user
								found[cp] = true
								break
							}
						}
					}
				}
			}
		}
		for _, ic := range res.Imported {
			if !importedIDs[ic.UserID] {
				importedIDs[ic.UserID] = true
				for _, u := range res.Users {
					if usr, ok := u.(*tg.User); ok && usr.ID == ic.UserID {
						toDelete = append(toDelete, cascade.ImportedContact{UserID: usr.ID, AccessHash: usr.AccessHash})
						break
					}
				}
			}
		}
		fmt.Printf("[telegram] BatchImport chunk %d-%d ok users=%d imported=%d\n", i, end, len(res.Users), len(res.Imported))
	}
	return found, toDelete, nil
}

// BatchDelete deletes imported contacts in batches (50 per chunk).
func (s *Service) BatchDelete(ctx context.Context, toDelete []cascade.ImportedContact) error {
	if len(toDelete) == 0 {
		return nil
	}
	const deleteChunk = 50
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return fmt.Errorf("telegram not connected/logged in")
	}
	for i := 0; i < len(toDelete); i += deleteChunk {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		end := i + deleteChunk
		if end > len(toDelete) {
			end = len(toDelete)
		}
		chunk := toDelete[i:end]
		inputs := make([]tg.InputUserClass, 0, len(chunk))
		for _, ic := range chunk {
			inputs = append(inputs, &tg.InputUser{UserID: ic.UserID, AccessHash: ic.AccessHash})
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := client.API().ContactsDeleteContacts(cctx, inputs)
		cancel()
		if err != nil {
			fmt.Printf("[telegram] BatchDelete chunk %d-%d failed: %v\n", i, end, err)
		} else {
			fmt.Printf("[telegram] BatchDelete chunk %d-%d ok users=%d\n", i, end, len(chunk))
		}
		if end < len(toDelete) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}
	}
	return nil
}

// CheckSession validates the session against the server (cheap Auth.Status,
// no contacts.* calls — safe to poll in background). On fatal auth errors
// it flips connected/loggedIn to false so the UI shows "Не підключено".
func (s *Service) CheckSession(ctx context.Context) error {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return fmt.Errorf("telegram not connected/logged in")
	}
	status, err := client.Auth().Status(ctx)
	if err != nil {
		if common.IsTelegramAuthError(err) {
			s.mu.Lock()
			s.connected = false
			s.loggedIn = false
			s.lastErr = err.Error()
			s.mu.Unlock()
		}
		return fmt.Errorf("session check: %w", err)
	}
	if !status.Authorized {
		s.mu.Lock()
		s.connected = false
		s.loggedIn = false
		s.mu.Unlock()
		return fmt.Errorf("session not authorized")
	}
	return nil
}

// Disconnect stops client but keeps session
func (s *Service) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.connected = false
	s.pwdNeeded = false
}

// Logout deletes session and persisted phone — for phone/account change
func (s *Service) Logout(ctx context.Context) error {
	s.Disconnect()
	time.Sleep(300 * time.Millisecond)
	for _, p := range []string{s.sessionPath, s.sessionPath + ".lock"} {
		_ = os.Remove(p)
	}
	s.mu.Lock()
	s.loggedIn = false
	s.phone = ""
	s.pwdNeeded = false
	s.lastErr = ""
	s.lastCodeType = ""
	s.qrToken = ""
	s.qrPNG = nil
	s.qrErr = ""
	// clear channels
	select {
	case <-s.codeChan:
	default:
	}
	select {
	case <-s.pwdChan:
	default:
	}
	// also remove phone from persisted config, keep api_id/hash for reuse
	keepID := s.appID
	keepHash := s.appHash
	s.mu.Unlock()
	if keepID != 0 && keepHash != "" {
		_ = common.EnsureDir(configPath)
		data, _ := json.Marshal(tgConfig{AppID: keepID, AppHash: keepHash})
		_ = os.WriteFile(configPath, data, 0600)
	} else {
		_ = os.Remove(configPath)
	}
	fmt.Printf("[telegram] logged out (phone cleared, api keys kept)\n")
	return nil
}

// LogoutFull deletes session AND api_id/hash — for full telegram account/key change
func (s *Service) LogoutFull(ctx context.Context) error {
	_ = s.Logout(ctx)
	return ClearConfig()
}

// Restore tries to resume existing session without phone/QR (for auto-login on restart)
// If session exists and is authorized, it keeps connection alive like Connect does.
func (s *Service) Restore(ctx context.Context) error {
	s.mu.Lock()
	if s.client != nil && s.connected && s.loggedIn {
		s.mu.Unlock()
		return nil
	}
	if s.appID == 0 || s.appHash == "" {
		s.mu.Unlock()
		return fmt.Errorf("api_id/api_hash not set")
	}
	s.mu.Unlock()

	if _, err := os.Stat(s.sessionPath); err != nil {
		return fmt.Errorf("no session file")
	}
	if err := common.EnsureDir(s.sessionPath); err != nil {
		return fmt.Errorf("telegram mkdir: %w", err)
	}
	opts := telegram.Options{
		SessionStorage: &session.FileStorage{Path: s.sessionPath},
	}
	client := telegram.NewClient(s.appID, s.appHash, opts)
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.client = client
	s.cancel = cancel
	s.mu.Unlock()
	_ = ctx

	// Check auth in background; if not authorized, exit and let caller handle
	go func() {
		err := client.Run(runCtx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if !status.Authorized {
				return fmt.Errorf("session not authorized")
			}
			s.mu.Lock()
			s.loggedIn = true
			s.connected = true
			s.mu.Unlock()
			fmt.Printf("[telegram] restored session — already authorized\n")
			<-runCtx.Done()
			return nil
		})
		if err != nil {
			fmt.Printf("[telegram] restore Run exited: %v\n", err)
			s.mu.Lock()
			s.connected = false
			// don't clear loggedIn yet — may still be false
			s.mu.Unlock()
			// cleanup client so next Connect can retry
			s.mu.Lock()
			if s.cancel != nil {
				// keep cancel for Disconnect, but clear client if not authorized?
			}
			s.mu.Unlock()
		}
	}()
	time.Sleep(800 * time.Millisecond)
	s.mu.Lock()
	ok := s.loggedIn && s.connected
	s.mu.Unlock()
	if !ok {
		// restore failed — cleanup to allow fresh login
		s.Disconnect()
		return fmt.Errorf("session not authorized or expired")
	}
	return nil
}

// SessionPath returns path
func (s *Service) SessionPath() string { return s.sessionPath }

func normalizePhone(phone string) string { return common.CleanPhone(phone) }
