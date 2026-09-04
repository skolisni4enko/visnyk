package telegram

import (
	"context"
	"encoding/json"
	"fmt"
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
}

func absPath(p string) string { return common.AbsPath(p) }

var defaultSessionPath = absPath("telegram-store/session.json")
var configPath = absPath("telegram-store/config.json")

// Paths from installed location (UserConfigDir). Lazy init to allow VISNYK_DATA_DIR override in tests.
func installedSessionPath() string {
	// prefer UserConfigDir if available — for installed app
	if d, err := os.UserConfigDir(); err == nil && d != "" {
		p := d + "/visnyk/telegram/session.json"
		// Normalize via filepath
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	return defaultSessionPath
}

func installedConfigPath() string {
	if d, err := os.UserConfigDir(); err == nil && d != "" {
		p := d + "/visnyk/telegram/config.json"
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	return configPath
}

func resolveSessionPath() string {
	if p := os.Getenv("VISNYK_DATA_DIR"); p != "" {
		return filepath.Join(p, "telegram", "session.json")
	}
	// If legacy exists in cwd, use legacy for migration
	if _, err := os.Stat(defaultSessionPath); err == nil {
		if _, err2 := os.Stat(installedSessionPath()); err2 != nil {
			return defaultSessionPath
		}
	}
	return installedSessionPath()
}

func resolveConfigPath() string {
	if p := os.Getenv("VISNYK_DATA_DIR"); p != "" {
		return filepath.Join(p, "telegram", "config.json")
	}
	if _, err := os.Stat(configPath); err == nil {
		if _, err2 := os.Stat(installedConfigPath()); err2 != nil {
			return configPath
		}
	}
	return installedConfigPath()
}

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
					s.lastErr = "невірний пароль — спробуйте ще раз: " + err.Error()
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
								s.lastErr = "невірний пароль — спробуйте ще раз: " + err2.Error()
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
						if s.lastErr == "" || !strings.Contains(s.lastErr, "невірний пароль") {
							s.lastErr = "2FA password required (QR)"
							s.qrErr = "2FA password required — введіть хмарний пароль нижче"
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
								s.lastErr = "невірний пароль — спробуйте ще раз: " + err.Error()
								s.qrErr = "невірний пароль — спробуйте ще раз"
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

// IsAvailable checks via import
func (s *Service) IsAvailable(phone string) (bool, error) {
	return s.IsOnTelegram(phone)
}

// IsOnTelegram checks via ContactsImportContacts
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
	contacts := []tg.InputPhoneContact{
		{Phone: phone, FirstName: "Test", LastName: ""},
	}
	res, err := client.API().ContactsImportContacts(ctx, contacts)
	if err != nil {
		return false, fmt.Errorf("ImportContacts: %w", err)
	}
	for _, u := range res.Users {
		if _, ok := u.(*tg.User); ok {
			return true, nil
		}
	}
	return false, nil
}

// Send sends text to phone
func (s *Service) Send(phone, msgText string) error {
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
	contacts := []tg.InputPhoneContact{{Phone: phone, FirstName: "Test", LastName: ""}}
	res, err := client.API().ContactsImportContacts(ctx, contacts)
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	if len(res.Users) == 0 {
		return fmt.Errorf("user not found for %s", phone)
	}
	var user *tg.User
	for _, u := range res.Users {
		if usr, ok := u.(*tg.User); ok {
			user = usr
			break
		}
	}
	if user == nil {
		return fmt.Errorf("user not found")
	}
	inputPeer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
	sender := tgmessage.NewSender(client.API())
	if format.IsHTML(msgText) {
		// Normalize HTML to Telegram-compatible subset for identical rendering with WhatsApp
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
