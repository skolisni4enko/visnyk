package ui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"visnyk/internal/cascade"
	"visnyk/internal/contacts"
	"visnyk/internal/logger"
	"visnyk/internal/paths"
	"visnyk/internal/storage"
	"visnyk/internal/telegram"
	"visnyk/internal/whatsapp"
)

// Version injected from main.
var AppVersion = "0.3.0"

// App is the Wails-bound application. All methods are exposed to the frontend.
type App struct {
	ctx         context.Context
	whatsappSvc *whatsapp.Service
	telegramSvc *telegram.Service
	cascadeSvc  *cascade.Service
	store       *storage.Store
	fileLogger  *logger.FileLogger

	cascadeMu      sync.Mutex
	cascadeCancel  context.CancelFunc
	cascadeRunning bool

	// health monitor: background session validation with cached status,
	// so GetConnectionsStatus never blocks the UI thread on network.
	healthMu       sync.Mutex
	healthCache    ConnectionsStatus
	healthAt       time.Time
	healthInFlight bool
	healthStop     chan struct{}
	healthStopOnce sync.Once
}

// healthInterval is how often the background monitor revalidates sessions.
const healthInterval = 20 * time.Second

// NewApp creates the Wails app with WhatsApp and Telegram services.
// dbPath may be empty for default path (uses paths.DataDir).
func NewApp(dbPath string) (*App, error) {
	_ = paths.EnsureDataDirs()
	// Central storage (visnyk.db) with encrypted settings
	st, err := storage.Open(paths.DataDir())
	if err != nil {
		// non-fatal: continue without storage (degraded)
		fmt.Printf("[storage] open failed: %v\n", err)
		st = nil
	}
	// File logger (app.log) — best effort, non-fatal if fails
	var fl *logger.FileLogger
	if lg, err := logger.New(paths.LogsPath()); err == nil {
		fl = lg
		_ = fl.Log("INFO", "app", fmt.Sprintf("Visnyk v%s started, dataDir=%s", AppVersion, paths.DataDir()))
	} else {
		fmt.Printf("[logger] open failed: %v\n", err)
	}
	// WhatsApp path defaults to UserConfigDir if dbPath empty
	waSvc, err := whatsapp.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("whatsapp init: %w", err)
	}
	tgSvc, err := telegram.New(0, "")
	if err != nil {
		return nil, fmt.Errorf("telegram init: %w", err)
	}

	cascadeSvc := cascade.New(waSvc, tgSvc, nil)

	return &App{
		whatsappSvc: waSvc,
		telegramSvc: tgSvc,
		cascadeSvc:  cascadeSvc,
		store:       st,
		fileLogger:  fl,
	}, nil
}

// Startup is called by Wails when the app starts.
// It auto-connects previous sessions if exists (so restart restores login).
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	go func() {
		time.Sleep(600 * time.Millisecond)
		if a.whatsappSvc != nil {
			bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := a.whatsappSvc.Connect(bgCtx); err != nil {
				fmt.Printf("[startup] whatsapp auto-connect failed: %v\n", err)
			} else {
				fmt.Printf("[startup] whatsapp auto-connect done IsLoggedIn=%v\n", a.whatsappSvc.IsLoggedIn())
			}
		}
		if a.telegramSvc != nil {
			fmt.Printf("[startup] telegram session check: %s\n", a.telegramSvc.SessionPath())
			if err := a.telegramSvc.Restore(context.Background()); err != nil {
				fmt.Printf("[startup] telegram restore failed (need login): %v\n", err)
			} else {
				fmt.Printf("[startup] telegram auto-connect done IsLoggedIn=%v\n", a.telegramSvc.IsLoggedIn())
			}
		}
	}()
	a.startHealthMonitor()
}

// startHealthMonitor launches background session validation: an initial
// check after restore settles, then a ticker. Results are cached —
// GetConnectionsStatus serves the cache instantly without network.
func (a *App) startHealthMonitor() {
	a.healthMu.Lock()
	if a.healthStop != nil {
		a.healthMu.Unlock()
		return
	}
	a.healthStop = make(chan struct{})
	stop := a.healthStop
	a.healthMu.Unlock()

	go func() {
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(healthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
				a.refreshHealth()
			case <-ticker.C:
				a.refreshHealth()
			}
		}
	}()
}

// stopHealthMonitor halts the background ticker (idempotent).
func (a *App) stopHealthMonitor() {
	a.healthStopOnce.Do(func() {
		a.healthMu.Lock()
		defer a.healthMu.Unlock()
		if a.healthStop != nil {
			close(a.healthStop)
			a.healthStop = nil
		}
	})
}

// Shutdown is called by Wails when the app exits.
func (a *App) Shutdown(_ context.Context) {
	a.stopHealthMonitor()
	// cancel cascade if running
	a.cascadeMu.Lock()
	if a.cascadeCancel != nil {
		a.cascadeCancel()
		a.cascadeCancel = nil
		a.cascadeRunning = false
	}
	a.cascadeMu.Unlock()
	if a.whatsappSvc != nil {
		a.whatsappSvc.Disconnect()
	}
	if a.telegramSvc != nil {
		a.telegramSvc.Disconnect()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
	if a.fileLogger != nil {
		_ = a.fileLogger.Close()
	}
}

// ConnectWhatsApp starts WhatsApp connection. If no session, QR will be emitted.
func (a *App) ConnectWhatsApp() string {
	if a.whatsappSvc == nil {
		return "whatsapp service not initialized"
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.whatsappSvc.Connect(ctx); err != nil {
		return fmt.Sprintf("connect failed: %v", err)
	}
	return "connecting"
}

// GetQRCodePNG returns base64-encoded QR PNG if available, empty otherwise.
// Uses latest QR (fixes 20s rotation issue) — polling friendly.
func (a *App) GetQRCodePNG() string {
	_, png, _ := a.whatsappSvc.GetLatestQR()
	if len(png) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// GetQRCode returns raw QR string if available.
func (a *App) GetQRCode() string {
	code, _, _ := a.whatsappSvc.GetLatestQR()
	return code
}

// GetQRStatus returns latest QR error if any (timeout, etc.).
func (a *App) GetQRStatus() string {
	_, _, errStr := a.whatsappSvc.GetLatestQR()
	return errStr
}

// RequestPairCode generates an 8-digit code for phone pairing.
// Phone must be international e.g. +380991234567. Use after Connect.
func (a *App) RequestPairCode(phone string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Use background ctx with timeout to avoid hanging UI; ignore App ctx which may be nil
	if a.ctx != nil {
		// combine: use App ctx as parent if available
		var cancel2 context.CancelFunc
		ctx, cancel2 = context.WithTimeout(a.ctx, 15*time.Second)
		defer cancel2()
	}
	code, err := a.whatsappSvc.RequestPairCode(ctx, phone)
	if err != nil {
		return "", err.Error()
	}
	return code, ""
}

// IsWhatsAppConnected reports transport connectivity.
func (a *App) IsWhatsAppConnected() bool {
	if a.whatsappSvc == nil {
		return false
	}
	return a.whatsappSvc.IsConnected()
}

// IsWhatsAppLoggedIn reports pairing success.
func (a *App) IsWhatsAppLoggedIn() bool {
	if a.whatsappSvc == nil {
		return false
	}
	return a.whatsappSvc.IsLoggedIn()
}

// DisconnectWhatsApp disconnects without deleting session.
func (a *App) DisconnectWhatsApp() string {
	if a.whatsappSvc != nil {
		a.whatsappSvc.Disconnect()
	}
	return "disconnected"
}

// LogoutWhatsApp fully logs out and deletes session (requires re-pair).
func (a *App) LogoutWhatsApp() string {
	if a.whatsappSvc == nil {
		return "not initialized"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.whatsappSvc.Logout(ctx); err != nil {
		return err.Error()
	}
	return "logged out"
}

// CheckWhatsApp checks if phone is on WhatsApp. Phone must be E.164 e.g. +380991234567.
func (a *App) CheckWhatsApp(phone string) (bool, string) {
	ok, err := a.whatsappSvc.IsAvailable(phone)
	if err != nil {
		return false, err.Error()
	}
	return ok, ""
}

// SendWhatsApp sends a direct WhatsApp message (bypass cascade).
func (a *App) SendWhatsApp(phone, message string) string {
	if err := a.whatsappSvc.Send(phone, message); err != nil {
		return err.Error()
	}
	return ""
}

func generateBatchID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
}

// SendCascadeBatch sends via cascade WA → TG → Viber (currently WA only).
// contacts are slice of {Name,PhoneRaw,NormalizedPhone} passed from frontend.
// att may be nil (text only) — one file for the whole batch otherwise.
func (a *App) SendCascadeBatch(contacts []cascade.Contact, template string, att *cascade.Attachment) []cascade.SendResult {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loaded, errStr := a.loadAttachment(att)
	if errStr != "" {
		a.logFile("WARN", "cascade", fmt.Sprintf("attachment rejected: %s", errStr))
		return []cascade.SendResult{}
	}
	preview := withFilePrefix(template, loaded)
	batchID := generateBatchID()
	// file + db log start
	a.logFile("INFO", "cascade", fmt.Sprintf("batch start (sync) batch=%s total=%d template_len=%d", batchID, len(contacts), len(template)))
	if a.store != nil {
		_ = a.store.CreateBatch(storage.Batch{ID: batchID, Channel: "cascade", MessagePreview: preview, CreatedAt: time.Now(), Total: len(contacts)})
	}
	res := a.cascadeSvc.SendBatchWithProgressAndBatchWithAttachment(ctx, contacts, template, batchID, loaded, nil)
	if a.store != nil {
		for _, r := range res {
			_ = a.store.AddHistory(storage.HistoryEntry{
				Phone:          r.Contact.PhoneRaw,
				Normalized:     r.Contact.NormalizedPhone,
				Name:           r.Contact.Name,
				Channel:        string(r.Channel),
				Status:         r.Status,
				Error:          r.Error,
				SentAt:         r.SentAt,
				MessagePreview: preview,
				BatchID:        r.BatchID,
			})
			_ = a.store.Log("INFO", "cascade", fmt.Sprintf("send %s via %s status=%s err=%s batch=%s", r.Contact.NormalizedPhone, r.Channel, r.Status, r.Error, r.BatchID))
		}
	}
	for _, r := range res {
		a.logFile("INFO", "cascade", fmt.Sprintf("send %s (%s) via %s status=%s err=%s batch=%s", r.Contact.PhoneRaw, r.Contact.NormalizedPhone, r.Channel, r.Status, r.Error, r.BatchID))
	}
	a.logFile("INFO", "cascade", fmt.Sprintf("batch done (sync) batch=%s sent=%d total=%d", batchID, countSent(res), len(res)))
	return res
}

// StartCascadeBatch starts async cascade with progress events. Returns "" on success or error string.
// Frontend listens to "cascade:progress" and "cascade:done" events.
func (a *App) StartCascadeBatch(contacts []cascade.Contact, template string, att *cascade.Attachment) string {
	return a.startBatchInternal(contacts, template, "", cascade.ChannelNone, att)
}

// StartWhatsAppBatch sends only via WhatsApp (no cascade fallback).
func (a *App) StartWhatsAppBatch(contacts []cascade.Contact, template string, att *cascade.Attachment) string {
	return a.startBatchInternal(contacts, template, "whatsapp", cascade.ChannelWhatsApp, att)
}

// StartTelegramBatch sends only via Telegram (no cascade fallback).
func (a *App) StartTelegramBatch(contacts []cascade.Contact, template string, att *cascade.Attachment) string {
	return a.startBatchInternal(contacts, template, "telegram", cascade.ChannelTelegram, att)
}

// SendWhatsAppBatch sync direct WA batch (for tests / non-async usage).
func (a *App) SendWhatsAppBatch(contacts []cascade.Contact, template string, att *cascade.Attachment) []cascade.SendResult {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loaded, errStr := a.loadAttachment(att)
	if errStr != "" {
		a.logFile("WARN", "whatsapp-direct", fmt.Sprintf("attachment rejected: %s", errStr))
		return []cascade.SendResult{}
	}
	batchID := generateBatchID()
	a.logFile("INFO", "whatsapp-direct", fmt.Sprintf("batch start (sync) batch=%s total=%d", batchID, len(contacts)))
	if a.store != nil {
		_ = a.store.CreateBatch(storage.Batch{ID: batchID, Channel: "whatsapp", MessagePreview: withFilePrefix(template, loaded), CreatedAt: time.Now(), Total: len(contacts)})
	}
	res := a.cascadeSvc.SendBatchDirectWithProgressAndBatchWithAttachment(ctx, contacts, template, cascade.ChannelWhatsApp, batchID, loaded, nil)
	a.persistDirectResults(res, withFilePrefix(template, loaded), "whatsapp-direct")
	return res
}

// SendTelegramBatch sync direct TG batch.
func (a *App) SendTelegramBatch(contacts []cascade.Contact, template string, att *cascade.Attachment) []cascade.SendResult {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loaded, errStr := a.loadAttachment(att)
	if errStr != "" {
		a.logFile("WARN", "telegram-direct", fmt.Sprintf("attachment rejected: %s", errStr))
		return []cascade.SendResult{}
	}
	batchID := generateBatchID()
	a.logFile("INFO", "telegram-direct", fmt.Sprintf("batch start (sync) batch=%s total=%d", batchID, len(contacts)))
	if a.store != nil {
		_ = a.store.CreateBatch(storage.Batch{ID: batchID, Channel: "telegram", MessagePreview: withFilePrefix(template, loaded), CreatedAt: time.Now(), Total: len(contacts)})
	}
	res := a.cascadeSvc.SendBatchDirectWithProgressAndBatchWithAttachment(ctx, contacts, template, cascade.ChannelTelegram, batchID, loaded, nil)
	a.persistDirectResults(res, withFilePrefix(template, loaded), "telegram-direct")
	return res
}

func (a *App) persistDirectResults(res []cascade.SendResult, template, source string) {
	if a.store != nil {
		for _, r := range res {
			_ = a.store.AddHistory(storage.HistoryEntry{
				Phone:          r.Contact.PhoneRaw,
				Normalized:     r.Contact.NormalizedPhone,
				Name:           r.Contact.Name,
				Channel:        string(r.Channel),
				Status:         r.Status,
				Error:          r.Error,
				SentAt:         r.SentAt,
				MessagePreview: template,
				BatchID:        r.BatchID,
			})
			_ = a.store.Log("INFO", source, fmt.Sprintf("send %s via %s status=%s err=%s batch=%s", r.Contact.NormalizedPhone, r.Channel, r.Status, r.Error, r.BatchID))
		}
	}
	for _, r := range res {
		a.logFile("INFO", source, fmt.Sprintf("send %s (%s) via %s status=%s err=%s batch=%s", r.Contact.PhoneRaw, r.Contact.NormalizedPhone, r.Channel, r.Status, r.Error, r.BatchID))
	}
	batch := ""
	if len(res) > 0 {
		batch = res[0].BatchID
	}
	a.logFile("INFO", source, fmt.Sprintf("batch done (sync) batch=%s sent=%d total=%d", batch, countSent(res), len(res)))
}

func (a *App) startBatchInternal(contacts []cascade.Contact, template string, logSource string, ch cascade.Channel, att *cascade.Attachment) string {
	isDirect := ch != cascade.ChannelNone && ch != ""
	source := logSource
	batchID := generateBatchID()
	batchChannel := string(ch)
	if batchChannel == "" || batchChannel == string(cascade.ChannelNone) {
		batchChannel = "cascade"
	}
	batchCreatedAt := time.Now()
	loaded, errStr := a.loadAttachment(att)
	if errStr != "" {
		a.logFile("WARN", source, fmt.Sprintf("attachment rejected batch=%s: %s", batchID, errStr))
		return "файл відхилено: " + errStr
	}
	preview := withFilePrefix(template, loaded)
	if loaded != nil {
		a.logFile("INFO", source, fmt.Sprintf("batch attachment batch=%s file=%q mime=%s size=%d", batchID, loaded.FileName, loaded.MIME, loaded.Size))
	}
	sendFn := func(ctx context.Context, onProgress func(cascade.Progress)) []cascade.SendResult {
		return a.cascadeSvc.SendBatchWithProgressAndBatchWithAttachment(ctx, contacts, template, batchID, loaded, onProgress)
	}
	if isDirect {
		if ch == cascade.ChannelWhatsApp {
			if a.whatsappSvc == nil || !a.whatsappSvc.IsConnected() || !a.whatsappSvc.IsLoggedIn() {
				return "підключи WhatsApp перед відправкою в WhatsApp"
			}
		}
		if ch == cascade.ChannelTelegram {
			if a.telegramSvc == nil || !a.telegramSvc.IsConnected() || !a.telegramSvc.IsLoggedIn() {
				return "підключи Telegram перед відправкою в Telegram"
			}
		}
		sendFn = func(ctx context.Context, onProgress func(cascade.Progress)) []cascade.SendResult {
			return a.cascadeSvc.SendBatchDirectWithProgressAndBatchWithAttachment(ctx, contacts, template, ch, batchID, loaded, onProgress)
		}
	} else {
		source = "cascade"
	}
	a.cascadeMu.Lock()
	if a.cascadeRunning {
		a.cascadeMu.Unlock()
		return "розсилка вже виконується"
	}
	if len(contacts) == 0 {
		a.cascadeMu.Unlock()
		return "немає контактів"
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if a.ctx != nil {
		ctx, cancel = context.WithCancel(a.ctx)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	a.cascadeCancel = cancel
	a.cascadeRunning = true
	a.cascadeMu.Unlock()

	a.logFile("INFO", source, fmt.Sprintf("batch start async batch=%s total=%d", batchID, len(contacts)))
	if a.store != nil {
		_ = a.store.Log("INFO", source, fmt.Sprintf("batch start async batch=%s total=%d", batchID, len(contacts)))
		_ = a.store.CreateBatch(storage.Batch{ID: batchID, Channel: batchChannel, MessagePreview: preview, CreatedAt: batchCreatedAt, Total: len(contacts)})
	}
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "cascade:start", map[string]interface{}{"total": len(contacts), "channel": string(ch), "mode": source, "batchId": batchID})
	}

	go func() {
		defer func() {
			a.cascadeMu.Lock()
			a.cascadeRunning = false
			a.cascadeCancel = nil
			a.cascadeMu.Unlock()
		}()

		var allResults []cascade.SendResult
		onProgress := func(p cascade.Progress) {
			if a.ctx != nil {
				wailsRuntime.EventsEmit(a.ctx, "cascade:progress", p)
			}
			if p.Status == "checking" {
				a.logFile("INFO", source, fmt.Sprintf("checking %d/%d %s (%s) batch=%s", p.Index, p.Total, p.Contact.NormalizedPhone, p.Contact.Name, p.BatchID))
			} else {
				a.logFile("INFO", source, fmt.Sprintf("progress %d/%d %s via %s status=%s err=%s eta=%ds batch=%s", p.Index, p.Total, p.Contact.NormalizedPhone, p.Channel, p.Status, p.Error, p.ETASeconds, p.BatchID))
				if a.store != nil {
					_ = a.store.AddHistory(storage.HistoryEntry{
						Phone:          p.Contact.PhoneRaw,
						Normalized:     p.Contact.NormalizedPhone,
						Name:           p.Contact.Name,
						Channel:        string(p.Channel),
						Status:         p.Status,
						Error:          p.Error,
						SentAt:         p.SentAt,
						MessagePreview: preview,
						BatchID:        p.BatchID,
					})
					_ = a.store.Log("INFO", source, fmt.Sprintf("send %s via %s status=%s batch=%s", p.Contact.NormalizedPhone, p.Channel, p.Status, p.BatchID))
				}
			}
		}

		allResults = sendFn(ctx, onProgress)

		cancelled := ctx.Err() != nil
		if cancelled {
			a.logFile("WARN", source, fmt.Sprintf("batch cancelled batch=%s at %d/%d", batchID, len(allResults), len(contacts)))
			if a.store != nil {
				_ = a.store.Log("WARN", source, fmt.Sprintf("batch cancelled batch=%s at %d/%d", batchID, len(allResults), len(contacts)))
			}
		} else {
			a.logFile("INFO", source, fmt.Sprintf("batch done async batch=%s sent=%d failed=%d total=%d", batchID, countSent(allResults), len(allResults)-countSent(allResults), len(allResults)))
			if a.store != nil {
				_ = a.store.Log("INFO", source, fmt.Sprintf("batch done async batch=%s total=%d", batchID, len(allResults)))
			}
		}
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "cascade:done", map[string]interface{}{
				"results":   allResults,
				"cancelled": cancelled,
				"total":     len(contacts),
				"channel":   string(ch),
				"mode":      source,
				"batchId":   batchID,
			})
		}
	}()

	return ""
}

// CancelCascadeBatch cancels running batch. Returns status.
func (a *App) CancelCascadeBatch() string {
	a.cascadeMu.Lock()
	defer a.cascadeMu.Unlock()
	if !a.cascadeRunning || a.cascadeCancel == nil {
		return "немає активної розсилки"
	}
	a.cascadeCancel()
	a.logFile("WARN", "cascade", "cancel requested by user")
	if a.store != nil {
		_ = a.store.Log("WARN", "cascade", "cancel requested by user")
	}
	return "скасування..."
}

// IsCascadeRunning reports if batch is active.
func (a *App) IsCascadeRunning() bool {
	a.cascadeMu.Lock()
	defer a.cascadeMu.Unlock()
	return a.cascadeRunning
}

// helper log to file + stdout
func (a *App) logFile(level, source, msg string) {
	fmt.Printf("[%s] %s: %s\n", source, level, msg)
	if a.fileLogger != nil {
		_ = a.fileLogger.Log(level, source, msg)
	}
}

func countSent(res []cascade.SendResult) int {
	n := 0
	for _, r := range res {
		if r.Status == "sent" {
			n++
		}
	}
	return n
}

// --- File log / folder helpers ---

// GetLogFilePath returns app.log path.
func (a *App) GetLogFilePath() string {
	if a.fileLogger != nil {
		return a.fileLogger.Path()
	}
	return paths.LogsPath()
}

// GetLogTail returns last N lines from app.log.
func (a *App) GetLogTail(n int) []string {
	if a.fileLogger != nil {
		lines, _ := a.fileLogger.Tail(n)
		return lines
	}
	// fallback: read directly
	return nil
}

// OpenLogsFile opens log file in OS default editor/explorer.
func (a *App) OpenLogsFile() string {
	path := paths.LogsPath()
	// ensure exists
	if a.fileLogger != nil {
		path = a.fileLogger.Path()
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Sprintf("open logs failed: %v (path: %s)", err, path)
	}
	return ""
}

// OpenDataDir opens data directory in file manager.
func (a *App) OpenDataDir() string {
	dir := paths.DataDir()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Sprintf("open folder failed: %v (dir: %s)", err, dir)
	}
	return ""
}

// WhatsappDBPath returns sqlite path for debugging.
func (a *App) WhatsappDBPath() string {
	if a.whatsappSvc == nil {
		return ""
	}
	return a.whatsappSvc.DBPath()
}

// SwitchTelegramAccount — convenience for UI: logout (phone cleared) and ready for new Connect with new phone/keys
func (a *App) SwitchTelegramAccount(clearKeys bool) string {
	if clearKeys {
		return a.LogoutTelegramFull()
	}
	return a.LogoutTelegram()
}

// SwitchWhatsAppAccount — deletes whatsapp session for phone/account change
func (a *App) SwitchWhatsAppAccount() string {
	return a.LogoutWhatsApp()
}

// --- Telegram ---

// TgConfig for frontend prefill + display current account
type TgConfig struct {
	ApiID   string `json:"apiId"`
	ApiHash string `json:"apiHash"`
	Phone   string `json:"phone"`
}

// GetTelegramConfig returns saved api_id/hash/phone for UI prefill
func (a *App) GetTelegramConfig() TgConfig {
	if a.telegramSvc == nil {
		return TgConfig{}
	}
	id, hash, phone := a.telegramSvc.GetFullConfig()
	fmt.Printf("[telegram] GetTelegramConfig called id=%d hash_len=%d phone=%q\n", id, len(hash), phone)
	if id == 0 {
		return TgConfig{Phone: phone}
	}
	return TgConfig{ApiID: strconv.Itoa(id), ApiHash: hash, Phone: phone}
}

// GetTelegramPhone returns persisted phone for current account display
func (a *App) GetTelegramPhone() string {
	if a.telegramSvc == nil {
		return ""
	}
	return a.telegramSvc.GetPhone()
}

// ClearTelegramConfig deletes saved api_id/hash/phone — for key change
func (a *App) ClearTelegramConfig() string {
	if a.telegramSvc == nil {
		return "not initialized"
	}
	if err := a.telegramSvc.ClearStoredConfig(); err != nil {
		return err.Error()
	}
	return "cleared"
}

// GetWhatsAppPhone returns linked WhatsApp phone if logged in
func (a *App) GetWhatsAppPhone() string {
	if a.whatsappSvc == nil {
		return ""
	}
	return a.whatsappSvc.GetPhone()
}

// ConnectTelegram starts Telegram auth via phone code. apiID/apiHash optional if already saved.
func (a *App) ConnectTelegram(apiIDStr, apiHash, phone string) string {
	if a.telegramSvc == nil {
		return "telegram not initialized"
	}
	apiIDStr = strings.TrimSpace(apiIDStr)
	apiHash = strings.TrimSpace(apiHash)
	phone = strings.TrimSpace(phone)
	// fallback to saved config if not provided
	if apiIDStr == "" || apiHash == "" {
		if savedID, savedHash := a.telegramSvc.GetConfig(); savedID != 0 && savedHash != "" {
			apiIDStr = strconv.Itoa(savedID)
			apiHash = savedHash
		} else {
			return "api_id/api_hash required: get from https://my.telegram.org (once, then saved)"
		}
	}
	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		return fmt.Sprintf("invalid api_id: %v", err)
	}
	if phone == "" {
		return "phone required: +380..."
	}
	a.telegramSvc.Configure(apiID, apiHash)
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.telegramSvc.Connect(bgCtx, phone); err != nil {
		return fmt.Sprintf("connect failed: %v", err)
	}
	return "code sent — check Telegram"
}

// ConnectTelegramQR starts Telegram QR login. apiID/apiHash optional if saved.
func (a *App) ConnectTelegramQR(apiIDStr, apiHash string) string {
	if a.telegramSvc == nil {
		return "telegram not initialized"
	}
	apiIDStr = strings.TrimSpace(apiIDStr)
	apiHash = strings.TrimSpace(apiHash)
	if apiIDStr == "" || apiHash == "" {
		if savedID, savedHash := a.telegramSvc.GetConfig(); savedID != 0 && savedHash != "" {
			apiIDStr = strconv.Itoa(savedID)
			apiHash = savedHash
		} else {
			return "api_id/api_hash required: get once from https://my.telegram.org"
		}
	}
	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		return fmt.Sprintf("invalid api_id: %v", err)
	}
	a.telegramSvc.Configure(apiID, apiHash)
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.telegramSvc.ConnectQR(bgCtx); err != nil {
		return fmt.Sprintf("qr connect failed: %v", err)
	}
	return "generating QR..."
}

// GetTelegramQR returns base64 PNG for Telegram QR
func (a *App) GetTelegramQR() string {
	if a.telegramSvc == nil {
		return ""
	}
	_, png, _ := a.telegramSvc.GetLatestQR()
	if len(png) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// GetTelegramQRStatus returns error/status for Telegram QR
func (a *App) GetTelegramQRStatus() string {
	if a.telegramSvc == nil {
		return ""
	}
	_, _, errStr := a.telegramSvc.GetLatestQR()
	return errStr
}

// ProvideTelegramCode sends SMS code
func (a *App) ProvideTelegramCode(code string) string {
	if a.telegramSvc == nil {
		return "not initialized"
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "empty code"
	}
	if err := a.telegramSvc.ProvideCode(code); err != nil {
		return err.Error()
	}
	return "code accepted — wait for auth"
}

// GetTelegramLastCodeType returns where last code was sent (App/SMS/etc)
func (a *App) GetTelegramLastCodeType() string {
	if a.telegramSvc == nil {
		return ""
	}
	return a.telegramSvc.GetLastCodeType()
}

// IsTelegramPasswordNeeded reports if 2FA password required
func (a *App) IsTelegramPasswordNeeded() bool {
	if a.telegramSvc == nil {
		return false
	}
	return a.telegramSvc.IsPasswordNeeded()
}

// ProvideTelegramPassword sends 2FA password
func (a *App) ProvideTelegramPassword(pwd string) string {
	if a.telegramSvc == nil {
		return "not initialized"
	}
	pwd = strings.TrimSpace(pwd)
	if pwd == "" {
		return "empty password"
	}
	if err := a.telegramSvc.ProvidePassword(pwd); err != nil {
		return err.Error()
	}
	return "password accepted — wait for auth"
}

// GetTelegramLastError returns last auth error
func (a *App) GetTelegramLastError() string {
	if a.telegramSvc == nil {
		return ""
	}
	return a.telegramSvc.GetLastError()
}

// IsTelegramConnected reports transport
func (a *App) IsTelegramConnected() bool {
	if a.telegramSvc == nil {
		return false
	}
	return a.telegramSvc.IsConnected()
}

// IsTelegramLoggedIn reports auth
func (a *App) IsTelegramLoggedIn() bool {
	if a.telegramSvc == nil {
		return false
	}
	return a.telegramSvc.IsLoggedIn()
}

// CheckTelegram checks if phone is on Telegram
func (a *App) CheckTelegram(phone string) (bool, string) {
	if a.telegramSvc == nil {
		return false, "not initialized"
	}
	ok, err := a.telegramSvc.IsAvailable(phone)
	if err != nil {
		return false, err.Error()
	}
	return ok, ""
}

// SendTelegram sends direct message
func (a *App) SendTelegram(phone, msg string) string {
	if a.telegramSvc == nil {
		return "not initialized"
	}
	if err := a.telegramSvc.Send(phone, msg); err != nil {
		return err.Error()
	}
	return ""
}

// LogoutTelegram deletes session + persisted phone (keeps api_id/hash) — for phone/account change
func (a *App) LogoutTelegram() string {
	if a.telegramSvc == nil {
		return "not initialized"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.telegramSvc.Logout(ctx); err != nil {
		return err.Error()
	}
	return "logged out — phone cleared, api_id/hash kept"
}

// LogoutTelegramFull deletes session AND api_id/hash — for full telegram key change
func (a *App) LogoutTelegramFull() string {
	if a.telegramSvc == nil {
		return "not initialized"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.telegramSvc.LogoutFull(ctx); err != nil {
		return err.Error()
	}
	return "fully logged out — api_id/hash cleared"
}

// TelegramSessionPath returns session file path
func (a *App) TelegramSessionPath() string {
	if a.telegramSvc == nil {
		return ""
	}
	return a.telegramSvc.SessionPath()
}

// --- Bulk import (contacts) ---

// ParseContactsText parses pasted text (newline/comma/semicolon/tab separated).
func (a *App) ParseContactsText(raw string) contacts.ParseResult {
	return contacts.ParseText(raw)
}

// ParseContactsFile parses file content (base64). Filename is used to detect type.
func (a *App) ParseContactsFile(filename, base64Data string) (contacts.ParseResult, string) {
	if strings.TrimSpace(base64Data) == "" {
		return contacts.ParseResult{}, "empty file"
	}
	// base64Data may be data URL "data:...;base64,XXXX" — strip prefix
	if idx := strings.Index(base64Data, ","); idx != -1 && strings.Contains(base64Data[:idx], "base64") {
		base64Data = base64Data[idx+1:]
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		// try raw string as fallback (for tests)
		data = []byte(base64Data)
		if len(data) == 0 {
			return contacts.ParseResult{}, fmt.Sprintf("base64 decode: %v", err)
		}
	}
	// Also handle if frontend passed plain text without base64 (small txt)
	// Heuristic: if decode succeeded but data looks like base64 text we keep it
	res := contacts.ParseFile(filename, data)
	return res, ""
}

// PreviewContacts is alias for ParseContactsText for frontend convenience.
func (a *App) PreviewContacts(raw string) contacts.ParseResult {
	return contacts.ParseText(raw)
}

// --- Connection status (background healthcheck) ---

// ChannelStatus is one messenger row for the connections footer.
type ChannelStatus struct {
	Connected bool   `json:"connected"`
	LoggedIn  bool   `json:"loggedIn"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// ConnectionsStatus aggregates all messengers for a single UI poll.
type ConnectionsStatus struct {
	WhatsApp ChannelStatus `json:"whatsapp"`
	Telegram ChannelStatus `json:"telegram"`
	Viber    ChannelStatus `json:"viber"`
}

// GetConnectionsStatus returns the last background-validated status.
// Never blocks on network: the health monitor refreshes the cache every
// healthInterval, this just serves it (or cheap local flags on first run).
func (a *App) GetConnectionsStatus() ConnectionsStatus {
	a.healthMu.Lock()
	cached := a.healthCache
	at := a.healthAt
	needRefresh := at.IsZero() && !a.healthInFlight
	if needRefresh {
		a.healthInFlight = true
	}
	a.healthMu.Unlock()
	if needRefresh {
		go func() {
			a.refreshHealth()
			a.healthMu.Lock()
			a.healthInFlight = false
			a.healthMu.Unlock()
		}()
	}
	if at.IsZero() {
		return a.snapshotFlags()
	}
	return cached
}

// snapshotFlags builds a status from cheap local flags only (no network).
func (a *App) snapshotFlags() ConnectionsStatus {
	var out ConnectionsStatus
	if a.whatsappSvc != nil {
		c := a.whatsappSvc.IsConnected()
		l := a.whatsappSvc.IsLoggedIn()
		out.WhatsApp = ChannelStatus{Connected: c, LoggedIn: l, OK: c && l}
	}
	if a.telegramSvc != nil {
		c := a.telegramSvc.IsConnected()
		l := a.telegramSvc.IsLoggedIn()
		st := ChannelStatus{Connected: c, LoggedIn: l, OK: c && l}
		if lastErr := a.telegramSvc.GetLastError(); lastErr != "" && !st.OK {
			st.Error = lastErr
		}
		out.Telegram = st
	}
	out.Viber = ChannelStatus{Error: "скоро"}
	return out
}

// refreshHealth validates every session live (Telegram via server
// Auth.Status — detects kicked/revoked sessions like 401
// AUTH_KEY_UNREGISTERED; Viber is a stub) and caches the result.
// Runs in background; logs only on OK transitions, not every tick.
func (a *App) refreshHealth() {
	var out ConnectionsStatus
	if a.whatsappSvc != nil {
		c := a.whatsappSvc.IsConnected()
		l := a.whatsappSvc.IsLoggedIn()
		out.WhatsApp = ChannelStatus{Connected: c, LoggedIn: l, OK: c && l}
	}
	if a.telegramSvc != nil {
		c := a.telegramSvc.IsConnected()
		l := a.telegramSvc.IsLoggedIn()
		st := ChannelStatus{Connected: c, LoggedIn: l, OK: c && l}
		if c && l {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			err := a.telegramSvc.CheckSession(ctx)
			cancel()
			if err != nil {
				st = ChannelStatus{Error: err.Error()}
			}
		} else if lastErr := a.telegramSvc.GetLastError(); lastErr != "" {
			st.Error = lastErr
		}
		out.Telegram = st
	}
	out.Viber = ChannelStatus{Error: "скоро"}

	a.healthMu.Lock()
	prev := a.healthCache
	hadPrev := !a.healthAt.IsZero()
	a.healthCache = out
	a.healthAt = time.Now()
	a.healthMu.Unlock()

	if hadPrev {
		a.logHealthTransition("whatsapp", prev.WhatsApp.OK, out.WhatsApp.OK, out.WhatsApp.Error)
		a.logHealthTransition("telegram", prev.Telegram.OK, out.Telegram.OK, out.Telegram.Error)
	}
}

// logHealthTransition logs session loss/recovery once per transition.
func (a *App) logHealthTransition(name string, wasOK, nowOK bool, errStr string) {
	if wasOK == nowOK {
		return
	}
	if nowOK {
		a.logFile("INFO", "health", fmt.Sprintf("%s session recovered", name))
		if a.store != nil {
			_ = a.store.Log("INFO", "health", fmt.Sprintf("%s session recovered", name))
		}
		return
	}
	msg := fmt.Sprintf("%s session lost", name)
	if errStr != "" {
		msg += fmt.Sprintf(": %v", errStr)
	}
	a.logFile("WARN", "health", msg)
	if a.store != nil {
		_ = a.store.Log("WARN", "health", msg)
	}
}

// --- Storage / Logs / Version ---

// GetVersion returns app version (from wails.json)
func (a *App) GetVersion() string { return AppVersion }

// GetDataDir returns OS-specific data directory (for UI display)
func (a *App) GetDataDir() string { return paths.DataDir() }

// GetAppDBPath returns central SQLite path
func (a *App) GetAppDBPath() string {
	if a.store != nil {
		return a.store.Path()
	}
	return paths.AppDBPath()
}

// ClearAllData wipes history, logs, and optionally credentials; also clears messenger sessions.
// keepCredentials=false => full wipe (requires re-login). keepCredentials=true => keep api_id/hash.
func (a *App) ClearAllData(keepCredentials bool) string {
	// Clear central storage
	if a.store != nil {
		if err := a.store.ClearAll(keepCredentials); err != nil {
			return fmt.Sprintf("clear storage: %v", err)
		}
		_ = a.store.Log("INFO", "ui", fmt.Sprintf("ClearAllData keepCredentials=%v", keepCredentials))
	}
	// Clear messenger sessions
	if a.whatsappSvc != nil {
		_ = a.whatsappSvc.ClearDB()
	}
	if a.telegramSvc != nil {
		if keepCredentials {
			_ = a.telegramSvc.Logout(context.Background())
		} else {
			_ = a.telegramSvc.LogoutFull(context.Background())
		}
	}
	if keepCredentials {
		return "очищено: історія та логи видалені, сесії скинуті (ключі збережено)"
	}
	return "повністю очищено: історія, логи, сесії та ключі видалені"
}

// ClearHistoryOnly clears only history table.
func (a *App) ClearHistoryOnly() string {
	if a.store == nil {
		return "storage not initialized"
	}
	if err := a.store.ClearHistoryOnly(); err != nil {
		return err.Error()
	}
	return "історію очищено"
}

// DeleteHistory deletes single history entry by id.
func (a *App) DeleteHistory(id int64) string {
	if a.store == nil {
		return "storage not initialized"
	}
	if err := a.store.DeleteHistory(id); err != nil {
		return err.Error()
	}
	a.logFile("INFO", "history", fmt.Sprintf("delete history id=%d", id))
	if a.store != nil {
		_ = a.store.Log("INFO", "history", fmt.Sprintf("delete history id=%d", id))
	}
	return ""
}

// GetHistory returns last N history entries.
func (a *App) GetHistory(limit int) []storage.HistoryEntry {
	if a.store == nil {
		return nil
	}
	h, _ := a.store.ListHistory(limit)
	return h
}

// GetHistoryPaged returns history page (server pagination).
func (a *App) GetHistoryPaged(limit, offset int) []storage.HistoryEntry {
	if a.store == nil {
		return nil
	}
	h, _ := a.store.ListHistoryPaged(limit, offset)
	return h
}

// GetHistoryCount returns total history count.
func (a *App) GetHistoryCount() int {
	if a.store == nil {
		return 0
	}
	n, _ := a.store.CountHistory()
	return n
}

// GetHistoryFiltered returns filtered history page. search is optional free-text (phone, name, channel, status, error, message).
func (a *App) GetHistoryFiltered(limit, offset int, channel, status string) []storage.HistoryEntry {
	if a.store == nil {
		return nil
	}
	h, _ := a.store.ListHistoryFiltered(limit, offset, channel, status)
	return h
}

// GetHistoryFilteredSearch returns filtered history with search (best practice: LIKE with ESCAPE, debounced frontend).
func (a *App) GetHistoryFilteredSearch(limit, offset int, channel, status, search string) []storage.HistoryEntry {
	if a.store == nil {
		return nil
	}
	h, _ := a.store.ListHistoryFilteredSearch(limit, offset, channel, status, search)
	return h
}

// GetHistoryCountFiltered returns count for filters.
func (a *App) GetHistoryCountFiltered(channel, status string) int {
	if a.store == nil {
		return 0
	}
	n, _ := a.store.CountHistoryFiltered(channel, status)
	return n
}

// GetHistoryCountFilteredSearch returns count for filters with search.
func (a *App) GetHistoryCountFilteredSearch(channel, status, search string) int {
	if a.store == nil {
		return 0
	}
	n, _ := a.store.CountHistoryFilteredSearch(channel, status, search)
	return n
}

// GetBatches returns recent batches.
func (a *App) GetBatches(limit int) []storage.Batch {
	if a.store == nil {
		return nil
	}
	b, _ := a.store.ListBatches(limit)
	return b
}

// GetAppLogs returns last N logs.
func (a *App) GetAppLogs(limit int) []storage.LogEntry {
	if a.store == nil {
		return nil
	}
	l, _ := a.store.ListLogs(limit)
	return l
}

// ClipboardSetText sets system clipboard via Wails runtime (fallback for WebKit).
func (a *App) ClipboardSetText(text string) string {
	if a.ctx == nil {
		return "no ctx"
	}
	wailsRuntime.ClipboardSetText(a.ctx, text)
	return ""
}

// LogApp writes a log entry (exposed to frontend for JS errors).
func (a *App) LogApp(level, source, msg string) string {
	a.logFile(level, source, msg)
	if a.store != nil {
		if err := a.store.Log(level, source, msg); err != nil {
			return err.Error()
		}
	}
	return ""
}

// --- Contact availability check for preview table ---

// CheckResult is one row for UI table: where number exists.
type CheckResult struct {
	Contact      cascade.Contact `json:"contact"`
	OnWhatsApp   bool            `json:"onWhatsApp"`
	OnTelegram   bool            `json:"onTelegram"`
	OnWhatsAppOK bool            `json:"onWhatsAppChecked"`
	OnTelegramOK bool            `json:"onTelegramChecked"`
	Error        string          `json:"error"`
}

// CheckContacts checks each contact's NormalizedPhone via WA and TG.
// Skips check if messenger not connected/logged in (returns checked=false).
// Runs sequentially to avoid flood. Logs each check to file + DB (source=check).
func (a *App) CheckContacts(contacts []cascade.Contact) []CheckResult {
	if len(contacts) == 0 {
		return nil
	}
	a.logFile("INFO", "check", fmt.Sprintf("check start total=%d", len(contacts)))
	if a.store != nil {
		_ = a.store.Log("INFO", "check", fmt.Sprintf("check start total=%d", len(contacts)))
	}
	out := make([]CheckResult, 0, len(contacts))
	for _, c := range contacts {
		r := CheckResult{Contact: c}
		// WhatsApp
		if a.whatsappSvc != nil && a.whatsappSvc.IsConnected() && a.whatsappSvc.IsLoggedIn() {
			r.OnWhatsAppOK = true
			ok, err := a.whatsappSvc.IsAvailable(c.NormalizedPhone)
			if err != nil {
				r.Error = err.Error()
				a.logFile("WARN", "check", fmt.Sprintf("WA check %s (%s) err=%v", c.NormalizedPhone, c.Name, err))
				if a.store != nil {
					_ = a.store.Log("WARN", "check", fmt.Sprintf("WA check %s err=%v", c.NormalizedPhone, err))
				}
			} else {
				r.OnWhatsApp = ok
				a.logFile("INFO", "check", fmt.Sprintf("WA check %s (%s) => %v", c.NormalizedPhone, c.Name, ok))
				if a.store != nil {
					_ = a.store.Log("INFO", "check", fmt.Sprintf("WA check %s => %v", c.NormalizedPhone, ok))
				}
			}
		}
		// Telegram
		if a.telegramSvc != nil && a.telegramSvc.IsConnected() && a.telegramSvc.IsLoggedIn() {
			r.OnTelegramOK = true
			ok, err := a.telegramSvc.IsAvailable(c.NormalizedPhone)
			if err != nil {
				if r.Error != "" {
					r.Error += "; "
				}
				r.Error += "tg: " + err.Error()
				a.logFile("WARN", "check", fmt.Sprintf("TG check %s (%s) err=%v", c.NormalizedPhone, c.Name, err))
				if a.store != nil {
					_ = a.store.Log("WARN", "check", fmt.Sprintf("TG check %s err=%v", c.NormalizedPhone, err))
				}
			} else {
				r.OnTelegram = ok
				a.logFile("INFO", "check", fmt.Sprintf("TG check %s (%s) => %v", c.NormalizedPhone, c.Name, ok))
				if a.store != nil {
					_ = a.store.Log("INFO", "check", fmt.Sprintf("TG check %s => %v", c.NormalizedPhone, ok))
				}
			}
		}
		if !r.OnWhatsAppOK && !r.OnTelegramOK {
			r.Error = "підключи WhatsApp або Telegram для перевірки"
			a.logFile("WARN", "check", fmt.Sprintf("check %s (%s) skipped: no messenger connected", c.NormalizedPhone, c.Name))
			if a.store != nil {
				_ = a.store.Log("WARN", "check", fmt.Sprintf("check %s skipped: no messenger", c.NormalizedPhone))
			}
		} else {
			// summary per contact
			where := "нема"
			if r.OnWhatsApp {
				where = "WA"
			} else if r.OnTelegram {
				where = "TG"
			}
			a.logFile("INFO", "check", fmt.Sprintf("check %s (%s) result=%s WA=%v(%v) TG=%v(%v) err=%q", c.NormalizedPhone, c.Name, where, r.OnWhatsApp, r.OnWhatsAppOK, r.OnTelegram, r.OnTelegramOK, r.Error))
			if a.store != nil {
				_ = a.store.Log("INFO", "check", fmt.Sprintf("check %s result=%s WA=%v TG=%v", c.NormalizedPhone, where, r.OnWhatsApp, r.OnTelegram))
			}
		}
		out = append(out, r)
	}
	// summary
	waCnt, tgCnt, noneCnt := 0, 0, 0
	for _, r := range out {
		if r.OnWhatsApp {
			waCnt++
		} else if r.OnTelegram {
			tgCnt++
		} else {
			noneCnt++
		}
	}
	a.logFile("INFO", "check", fmt.Sprintf("check done total=%d WA=%d TG=%d none=%d", len(out), waCnt, tgCnt, noneCnt))
	if a.store != nil {
		_ = a.store.Log("INFO", "check", fmt.Sprintf("check done total=%d WA=%d TG=%d none=%d", len(out), waCnt, tgCnt, noneCnt))
	}
	return out
}

// SendCascadeBatchWithLog is alias for frontend compatibility.
func (a *App) SendCascadeBatchWithLog(contacts []cascade.Contact, template string, att *cascade.Attachment) []cascade.SendResult {
	return a.SendCascadeBatch(contacts, template, att)
}
