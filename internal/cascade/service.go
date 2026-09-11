package cascade

import (
	"context"
	"math/rand"
	"sort"
	"sync"
	"time"

	"visnyk/internal/common"
)

// TelegramPaceMax caps the regular inter-message delay for Telegram.
// Flood-wait sleeps (ordered by the server) are always honored in full.
const TelegramPaceMax = 30 * time.Second

// Service is the cascade orchestrator: WA → TG → Viber.
type Service struct {
	whatsapp Messenger
	telegram Messenger
	viber    Messenger
	// delay between sends, 8-15s + jitter to avoid bans
	minDelay time.Duration
	maxDelay time.Duration
	// telegram has its own pacing (capped at TelegramPaceMax)
	tgMinDelay time.Duration
	tgMaxDelay time.Duration

	// per-channel pacing: WA and TG sends run on independent schedules,
	// so a Telegram flood pause never blocks WhatsApp delivery.
	paceMu   sync.Mutex
	lastSend map[Channel]time.Time

	// consecutive Telegram floods: 3 in a row triggers a 5min cooldown.
	floodMu     sync.Mutex
	consecFlood int

	// dead channels (circuit breaker): set on fatal auth errors
	// (e.g. 401 AUTH_KEY_UNREGISTERED) so remaining contacts skip the
	// dead channel instantly instead of hammering it. Reset per batch.
	deadMu sync.Mutex
	dead   map[Channel]bool
}

// New creates a cascade service with WA > TG > Viber priority.
func New(wa, tg, vb Messenger) *Service {
	return &Service{
		whatsapp: wa,
		telegram: tg,
		viber:    vb,
		minDelay: 8 * time.Second,
		maxDelay: 15 * time.Second,
		// telegram pacing honors the 30s cap agreed with the user
		tgMinDelay: 8 * time.Second,
		tgMaxDelay: 15 * time.Second,
		lastSend:   make(map[Channel]time.Time),
		dead:       make(map[Channel]bool),
	}
}

// SessionLostError is reported for contacts skipped after their channel died.
const SessionLostError = "session lost, please re-login via QR/code"

// resetDead clears the circuit breaker at batch start.
func (s *Service) resetDead() {
	s.deadMu.Lock()
	defer s.deadMu.Unlock()
	s.dead = make(map[Channel]bool)
}

// markDead trips the circuit breaker for a channel (fatal auth error).
func (s *Service) markDead(ch Channel) {
	s.deadMu.Lock()
	defer s.deadMu.Unlock()
	if s.dead == nil {
		s.dead = make(map[Channel]bool)
	}
	s.dead[ch] = true
}

// channelDead reports whether the channel died earlier in this batch.
func (s *Service) channelDead(ch Channel) bool {
	s.deadMu.Lock()
	defer s.deadMu.Unlock()
	return s.dead[ch]
}

// SendBatch sends to each contact via all available channels (broadcast).
// It respects ctx cancellation. Pacing is per-channel (see paceChannel),
// so there is no extra sleep between contacts here.
func (s *Service) SendBatch(ctx context.Context, contacts []Contact, template string) []SendResult {
	return s.SendBatchWithAttachment(ctx, contacts, template, nil)
}

// SendBatchWithAttachment is like SendBatch but delivers one file with
// the template as caption to every contact (nil = text only).
func (s *Service) SendBatchWithAttachment(ctx context.Context, contacts []Contact, template string, att *Attachment) []SendResult {
	return s.SendBatchWithProgressAndBatchWithAttachment(ctx, contacts, template, "", att, nil)
}

// SendBatchWithBatch is batch-aware wrapper.
func (s *Service) SendBatchWithBatch(ctx context.Context, contacts []Contact, template, batchID string) []SendResult {
	return s.SendBatchWithProgressAndBatchWithAttachment(ctx, contacts, template, batchID, nil, nil)
}

// SendBatchDirect sends only via the specified channel (no cascade fallback).
// If contact is not available on that channel or send fails, result is failed with channel-specific error.
func (s *Service) SendBatchDirect(ctx context.Context, contacts []Contact, template string, ch Channel) []SendResult {
	return s.SendBatchDirectWithAttachment(ctx, contacts, template, ch, nil)
}

// SendBatchDirectWithAttachment is like SendBatchDirect but with one file (nil = text only).
func (s *Service) SendBatchDirectWithAttachment(ctx context.Context, contacts []Contact, template string, ch Channel, att *Attachment) []SendResult {
	return s.SendBatchDirectWithProgressAndBatchWithAttachment(ctx, contacts, template, ch, "", att, nil)
}

// SendBatchDirectWithBatch is batch-aware wrapper.
func (s *Service) SendBatchDirectWithBatch(ctx context.Context, contacts []Contact, template string, ch Channel, batchID string) []SendResult {
	return s.SendBatchDirectWithProgressAndBatchWithAttachment(ctx, contacts, template, ch, batchID, nil, nil)
}

// SendBatchDirectWithProgress is like SendBatchDirect but emits progress per contact.
func (s *Service) SendBatchDirectWithProgress(ctx context.Context, contacts []Contact, template string, ch Channel, onProgress func(Progress)) []SendResult {
	return s.SendBatchDirectWithProgressAndBatchWithAttachment(ctx, contacts, template, ch, "", nil, onProgress)
}

// SendBatchDirectWithProgressAndBatch is batch-aware variant.
func (s *Service) SendBatchDirectWithProgressAndBatch(ctx context.Context, contacts []Contact, template string, ch Channel, batchID string, onProgress func(Progress)) []SendResult {
	return s.SendBatchDirectWithProgressAndBatchWithAttachment(ctx, contacts, template, ch, batchID, nil, onProgress)
}

// SendBatchDirectWithProgressAndBatchWithAttachment is the full direct variant (att may be nil).
func (s *Service) SendBatchDirectWithProgressAndBatchWithAttachment(ctx context.Context, contacts []Contact, template string, ch Channel, batchID string, att *Attachment, onProgress func(Progress)) []SendResult {
	// Normalize once: WA/TG workers share att in parallel and must not mutate it.
	att.Normalize()
	// wrap progress to tag batchID
	wrappedProgress := onProgress
	if onProgress != nil && batchID != "" {
		wrappedProgress = func(p Progress) {
			p.BatchID = batchID
			onProgress(p)
		}
	}
	// Hybrid batch path for Telegram: batch import (20 per chunk, 5s pacing),
	// per-contact paced send via paceChannel (8-15s capped at 30s), then
	// batch delete (50 per chunk). Avoids per-contact "Test" pollution
	// and respects flood limits. Skipped when the batch carries a file:
	// media goes through the per-contact loop so captions stay attached.
	if ch == ChannelTelegram && att == nil && len(contacts) >= 10 {
		if bi, ok := s.telegram.(BatchImporter); ok {
			res := s.sendBatchDirectBatchHybrid(ctx, contacts, template, ch, att, wrappedProgress, bi)
			for i := range res {
				res[i].BatchID = batchID
			}
			return res
		}
		if bs, ok := s.telegram.(BatchSender); ok {
			res := s.sendBatchDirectBatch(ctx, contacts, template, ch, wrappedProgress, bs)
			for i := range res {
				res[i].BatchID = batchID
			}
			return res
		}
	}
	results := make([]SendResult, 0, len(contacts))
	total := len(contacts)
	s.resetDead()
	for i, c := range contacts {
		select {
		case <-ctx.Done():
			return results
		default:
		}
		eta := 0
		if total > i+1 {
			eta = int(time.Duration(total-i-1) * 11 * time.Second / time.Second)
			if s.minDelay > 0 && s.maxDelay > 0 {
				avg := (s.minDelay + s.maxDelay) / 2
				eta = int(time.Duration(total-i-1) * avg / time.Second)
			}
		}
		if wrappedProgress != nil {
			wrappedProgress(Progress{Index: i + 1, Total: total, Contact: c, Status: "checking", ETASeconds: eta, BatchID: batchID})
		}
		res := s.sendOneDirect(ctx, c, template, ch, att)
		res.BatchID = batchID
		results = append(results, res)
		if wrappedProgress != nil {
			st := res.Status
			if st == "" {
				st = "failed"
			}
			wrappedProgress(Progress{
				Index:      i + 1,
				Total:      total,
				Contact:    c,
				Channel:    res.Channel,
				Status:     st,
				Error:      res.Error,
				SentAt:     res.SentAt,
				ETASeconds: eta,
				BatchID:    batchID,
			})
		}
		if i < len(contacts)-1 && !s.channelDead(ch) {
			delay := s.interContactDelay()
			select {
			case <-ctx.Done():
				return results
			case <-time.After(delay):
			}
		}
	}
	return results
}

// sendBatchDirectBatch is the batch path for Telegram direct sends.
// It imports all contacts in chunks (20 per chunk, 5s pacing), sends to
// all resolved users, and deletes temporary contacts in batches (50 per
// delete chunk). This avoids per-contact "Test" pollution.
func (s *Service) sendBatchDirectBatch(ctx context.Context, contacts []Contact, template string, ch Channel, onProgress func(Progress), bs BatchSender) []SendResult {
	total := len(contacts)
	s.resetDead()
	// Emit checking for all contacts upfront (UI shows progress)
	if onProgress != nil {
		for i, c := range contacts {
			eta := int(time.Duration(total-i) * 8 * time.Second / time.Second)
			onProgress(Progress{Index: i + 1, Total: total, Contact: c, Status: "checking", ETASeconds: eta})
		}
	}
	if s.channelDead(ch) {
		results := make([]SendResult, 0, len(contacts))
		for _, c := range contacts {
			results = append(results, SendResult{Contact: c, Channel: ch, Status: "failed", Error: string(ch) + ": " + SessionLostError, SentAt: time.Now()})
		}
		return results
	}
	phones := make([]string, 0, len(contacts))
	for _, c := range contacts {
		phones = append(phones, c.NormalizedPhone)
	}
	perPhoneErr, err := bs.BatchSend(ctx, phones, template)
	if err != nil {
		if common.IsTelegramAuthError(err) {
			s.markDead(ch)
			errStr := string(ch) + ": " + SessionLostError + " (" + err.Error() + ")"
			results := make([]SendResult, 0, len(contacts))
			for i, c := range contacts {
				eta := int(time.Duration(total-i-1) * 8 * time.Second / time.Second)
				res := SendResult{Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: time.Now()}
				results = append(results, res)
				if onProgress != nil {
					onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: res.SentAt, ETASeconds: eta})
				}
			}
			return results
		}
		// global batch error (e.g., context canceled) — fail all
		results := make([]SendResult, 0, len(contacts))
		for i, c := range contacts {
			eta := int(time.Duration(total-i-1) * 8 * time.Second / time.Second)
			errStr := "batch failed: " + err.Error()
			res := SendResult{Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: time.Now()}
			results = append(results, res)
			if onProgress != nil {
				onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: res.SentAt, ETASeconds: eta})
			}
		}
		return results
	}
	results := make([]SendResult, 0, len(contacts))
	for i, c := range contacts {
		eta := int(time.Duration(total-i-1) * 8 * time.Second / time.Second)
		perr := perPhoneErr[c.NormalizedPhone]
		var res SendResult
		if perr != nil {
			// per-phone error already contains "skipped: " or "failed to send: " prefix from BatchSend
			res = SendResult{Contact: c, Channel: ch, Status: "failed", Error: perr.Error(), SentAt: time.Now()}
		} else {
			res = SendResult{Contact: c, Channel: ch, Status: "sent", SentAt: time.Now()}
		}
		results = append(results, res)
		if onProgress != nil {
			st := res.Status
			onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: st, Error: res.Error, SentAt: res.SentAt, ETASeconds: eta})
		}
	}
	return results
}

// sendBatchDirectBatchHybrid is the hybrid variant: batch import, per-contact
// paced send, batch delete. Keeps the import benefit (no per-contact
// ImportContacts) but sends each message with per-channel pacing
// (paceChannel 8-15s capped at 30s) to avoid PEER_FLOOD.
func (s *Service) sendBatchDirectBatchHybrid(ctx context.Context, contacts []Contact, template string, ch Channel, att *Attachment, onProgress func(Progress), bi BatchImporter) []SendResult {
	total := len(contacts)
	s.resetDead()
	if onProgress != nil {
		for i, c := range contacts {
			eta := int(time.Duration(total-i) * 8 * time.Second / time.Second)
			onProgress(Progress{Index: i + 1, Total: total, Contact: c, Status: "checking", ETASeconds: eta})
		}
	}
	if s.channelDead(ch) {
		results := make([]SendResult, 0, len(contacts))
		for _, c := range contacts {
			results = append(results, SendResult{Contact: c, Channel: ch, Status: "failed", Error: string(ch) + ": " + SessionLostError, SentAt: time.Now()})
		}
		return results
	}
	phones := make([]string, 0, len(contacts))
	for _, c := range contacts {
		phones = append(phones, c.NormalizedPhone)
	}
	// Batch import all contacts
	found, toDelete, err := bi.BatchImport(ctx, phones)
	if err != nil {
		if common.IsTelegramAuthError(err) {
			s.markDead(ch)
			errStr := string(ch) + ": " + SessionLostError + " (" + err.Error() + ")"
			results := make([]SendResult, 0, len(contacts))
			for i, c := range contacts {
				eta := int(time.Duration(total-i-1) * 8 * time.Second / time.Second)
				res := SendResult{Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: time.Now()}
				results = append(results, res)
				if onProgress != nil {
					onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: res.SentAt, ETASeconds: eta})
				}
			}
			return results
		}
		// batch import failed globally
		results := make([]SendResult, 0, len(contacts))
		for i, c := range contacts {
			eta := int(time.Duration(total-i-1) * 8 * time.Second / time.Second)
			errStr := "batch import failed: " + err.Error()
			res := SendResult{Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: time.Now()}
			results = append(results, res)
			if onProgress != nil {
				onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: res.SentAt, ETASeconds: eta})
			}
		}
		return results
	}
	// Per-contact paced send
	m := s.messengerFor(ch)
	if m == nil {
		results := make([]SendResult, 0, len(contacts))
		for _, c := range contacts {
			results = append(results, SendResult{Contact: c, Channel: ch, Status: "failed", Error: "messenger " + string(ch) + " not initialized", SentAt: time.Now()})
		}
		// cleanup batch import
		_ = bi.BatchDelete(context.Background(), toDelete)
		return results
	}
	results := make([]SendResult, 0, len(contacts))
	for i, c := range contacts {
		select {
		case <-ctx.Done():
			// batch delete for already imported contacts
			_ = bi.BatchDelete(context.Background(), toDelete)
			return results
		default:
		}
		eta := int(time.Duration(total-i-1) * 8 * time.Second / time.Second)
		if !found[c.NormalizedPhone] {
			res := SendResult{Contact: c, Channel: ch, Status: "failed", Error: "user not found for " + c.NormalizedPhone, SentAt: time.Now()}
			results = append(results, res)
			if onProgress != nil {
				onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: "failed", Error: res.Error, SentAt: res.SentAt, ETASeconds: eta})
			}
			continue
		}
		if s.channelDead(ch) {
			res := SendResult{Contact: c, Channel: ch, Status: "failed", Error: string(ch) + ": " + SessionLostError, SentAt: time.Now()}
			results = append(results, res)
			if onProgress != nil {
				onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: "failed", Error: res.Error, SentAt: res.SentAt, ETASeconds: eta})
			}
			continue
		}
		sent, errStr, sentAt := s.sendViaChannel(ctx, m, c.NormalizedPhone, template, att)
		var res SendResult
		if !sent {
			if errStr == "" {
				errStr = "contact not found in " + string(ch)
			}
			res = SendResult{Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: time.Now()}
		} else {
			res = SendResult{Contact: c, Channel: ch, Status: "sent", SentAt: sentAt}
			if errStr != "" {
				res.Error = errStr
			}
		}
		results = append(results, res)
		if onProgress != nil {
			st := res.Status
			onProgress(Progress{Index: i + 1, Total: total, Contact: c, Channel: ch, Status: st, Error: res.Error, SentAt: res.SentAt, ETASeconds: eta})
		}
	}
	// Batch delete imported contacts in background
	go func() {
		_ = bi.BatchDelete(context.Background(), toDelete)
	}()
	return results
}

// SendBatchWithProgress is like SendBatch but calls onProgress after each contact and before delay.
// onProgress may be nil. It is called synchronously on the sender goroutine.
// No inter-contact sleep here: pacing is enforced per-channel by paceChannel
// inside sendViaChannel, otherwise contacts would pay the delay twice.
func (s *Service) SendBatchWithProgress(ctx context.Context, contacts []Contact, template string, onProgress func(Progress)) []SendResult {
	return s.SendBatchWithProgressAndBatchWithAttachment(ctx, contacts, template, "", nil, onProgress)
}

// SendBatchWithProgressAndBatch is batch-aware variant that tags results/progress with batchID.
func (s *Service) SendBatchWithProgressAndBatch(ctx context.Context, contacts []Contact, template, batchID string, onProgress func(Progress)) []SendResult {
	return s.SendBatchWithProgressAndBatchWithAttachment(ctx, contacts, template, batchID, nil, onProgress)
}

// SendBatchWithProgressAndBatchWithAttachment is the full broadcast variant (att may be nil).
func (s *Service) SendBatchWithProgressAndBatchWithAttachment(ctx context.Context, contacts []Contact, template, batchID string, att *Attachment, onProgress func(Progress)) []SendResult {
	// Normalize once: WA/TG workers share att in parallel and must not mutate it.
	att.Normalize()
	results := make([]SendResult, 0, len(contacts))
	total := len(contacts)
	s.resetDead()
	for i, c := range contacts {
		select {
		case <-ctx.Done():
			return results
		default:
		}
		eta := 0
		if total > i+1 {
			eta = int(time.Duration(total-i-1) * 11 * time.Second / time.Second)
			if s.minDelay > 0 && s.maxDelay > 0 {
				avg := (s.minDelay + s.maxDelay) / 2
				eta = int(time.Duration(total-i-1) * avg / time.Second)
			}
		}
		if onProgress != nil {
			onProgress(Progress{Index: i + 1, Total: total, Contact: c, Status: "checking", ETASeconds: eta, BatchID: batchID})
		}
		res := s.sendOneCtx(ctx, c, template, att)
		res.BatchID = batchID
		results = append(results, res)
		if onProgress != nil {
			st := res.Status
			if st == "" {
				st = "failed"
			}
			onProgress(Progress{
				Index:      i + 1,
				Total:      total,
				Contact:    c,
				Channel:    res.Channel,
				Status:     st,
				Error:      res.Error,
				SentAt:     res.SentAt,
				ETASeconds: eta,
				BatchID:    batchID,
			})
		}
	}
	return results
}

func (s *Service) sendOne(ctx context.Context, c Contact, tmpl string) SendResult {
	return s.sendOneCtx(ctx, c, tmpl, nil)
}

func (s *Service) sendOneCtx(ctx context.Context, c Contact, tmpl string, att *Attachment) SendResult {
	// Broadcast to all available messengers on independent schedules:
	// WhatsApp and Telegram sends run in parallel goroutines ("different
	// threads"), each with its own pacing, and results are merged.
	// A Telegram flood pause therefore never blocks WhatsApp delivery.
	msg := tmpl // abstract message — no name/phone substitution
	channels := []Messenger{s.whatsapp, s.telegram, s.viber}
	var mu sync.Mutex
	var sentChannels []string
	var errs []string
	var lastSentAt time.Time
	var wg sync.WaitGroup
	for _, m := range channels {
		if m == nil {
			continue
		}
		wg.Add(1)
		go func(m Messenger) {
			defer wg.Done()
			sent, errStr, sentAt := s.sendViaChannel(ctx, m, c.NormalizedPhone, msg, att)
			mu.Lock()
			defer mu.Unlock()
			if sent {
				sentChannels = append(sentChannels, string(m.Name()))
				lastSentAt = sentAt
			}
			if errStr != "" {
				errs = append(errs, errStr)
			}
		}(m)
	}
	wg.Wait()
	if len(sentChannels) == 0 {
		errStr := "no messenger available"
		if len(errs) > 0 {
			errStr = joinErrors(errs)
		}
		return SendResult{Contact: c, Channel: ChannelNone, Status: "failed", Error: errStr, SentAt: time.Now()}
	}
	combined := Channel(joinChannels(sortedCopy(sentChannels)))
	errStr := ""
	if len(errs) > 0 {
		errStr = joinErrors(errs)
	}
	return SendResult{Contact: c, Channel: combined, Status: "sent", Error: errStr, SentAt: lastSentAt}
}

// interContactDelay returns the pause between contacts, safe for zero bounds.
func (s *Service) interContactDelay() time.Duration {
	if s.maxDelay > s.minDelay {
		return s.minDelay + time.Duration(rand.Int63n(int64(s.maxDelay-s.minDelay)))
	}
	if s.minDelay < 0 {
		return 0
	}
	return s.minDelay
}

// tgPaceBounds returns Telegram pacing bounds with the 30s cap applied.
func (s *Service) tgPaceBounds() (time.Duration, time.Duration) {
	min, max := s.tgMinDelay, s.tgMaxDelay
	if max > TelegramPaceMax {
		max = TelegramPaceMax
	}
	if min > max {
		min = max
	}
	return min, max
}

// paceChannel sleeps until this channel's own inter-message delay has passed
// since its last send. Each channel is paced independently. Telegram delays
// are capped at TelegramPaceMax (30s).
func (s *Service) paceChannel(ctx context.Context, ch Channel) error {
	min, max := s.minDelay, s.maxDelay
	if ch == ChannelTelegram {
		min, max = s.tgPaceBounds()
	}
	var delay time.Duration
	if max > min {
		delay = min + time.Duration(rand.Int63n(int64(max-min)))
	} else {
		delay = min
	}
	s.paceMu.Lock()
	wait := delay - time.Since(s.lastSend[ch])
	if wait <= 0 {
		s.lastSend[ch] = time.Now()
		s.paceMu.Unlock()
		return nil
	}
	s.paceMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
	}
	s.paceMu.Lock()
	s.lastSend[ch] = time.Now()
	s.paceMu.Unlock()
	return nil
}

// sendViaChannel delivers via one messenger: pace, then MediaSender (when
// the batch carries a file), DirectSender fast path (single resolve+send)
// or classic IsAvailable+Send. Flood waits are honored in full and retried
// once; skippable errors are not retried.
// A channel killed earlier in this batch (fatal auth error) is skipped
// instantly — no pacing, no API calls.
func (s *Service) sendViaChannel(ctx context.Context, m Messenger, phone, msg string, att *Attachment) (bool, string, time.Time) {
	if s.channelDead(m.Name()) {
		return false, channelLabel(m.Name()) + ": " + SessionLostError, time.Time{}
	}
	if err := s.paceChannel(ctx, m.Name()); err != nil {
		return false, "", time.Time{}
	}
	if att != nil {
		if ms, ok := m.(MediaSender); ok {
			if err := s.withFloodRetry(ctx, m.Name(), func() error {
				return ms.SendMedia(phone, msg, att)
			}); err != nil {
				return false, channelLabel(m.Name()) + ": " + sendErrText(m.Name(), err), time.Time{}
			}
			return true, "", time.Now()
		}
		// Messenger cannot send files: deliver text so the contact still
		// gets the message, and note the skipped attachment.
		if ds, ok := m.(DirectSender); ok {
			if err := s.withFloodRetry(ctx, m.Name(), func() error {
				return ds.ResolveAndSend(phone, msg)
			}); err != nil {
				return false, channelLabel(m.Name()) + ": " + sendErrText(m.Name(), err), time.Time{}
			}
			return true, channelLabel(m.Name()) + ": attachment skipped (files not supported here), text sent", time.Now()
		}
	}
	if ds, ok := m.(DirectSender); ok {
		if err := s.withFloodRetry(ctx, m.Name(), func() error {
			return ds.ResolveAndSend(phone, msg)
		}); err != nil {
			return false, channelLabel(m.Name()) + ": " + sendErrText(m.Name(), err), time.Time{}
		}
		return true, "", time.Now()
	}
	ok, err := m.IsAvailable(phone)
	if err != nil {
		if common.IsTelegramAuthError(err) {
			s.markDead(m.Name())
			return false, channelLabel(m.Name()) + ": " + SessionLostError + " (" + err.Error() + ")", time.Time{}
		}
		if d, isFlood := common.FloodWaitDuration(err); isFlood {
			s.noteFlood(m.Name(), true)
			if ferr := s.sleepFlood(ctx, m.Name(), d); ferr != nil {
				return false, "", time.Time{}
			}
			ok, err = m.IsAvailable(phone)
			if err != nil {
				if _, stillFlood := common.FloodWaitDuration(err); stillFlood {
					s.noteFlood(m.Name(), true)
				} else {
					s.noteFlood(m.Name(), false)
				}
				return false, channelLabel(m.Name()) + ": check failed: " + err.Error(), time.Time{}
			}
			s.noteFlood(m.Name(), false)
		} else {
			return false, channelLabel(m.Name()) + ": check failed: " + err.Error(), time.Time{}
		}
	}
	if !ok {
		// not available — not an error for broadcast, just skip
		return false, "", time.Time{}
	}
	if err := s.withFloodRetry(ctx, m.Name(), func() error {
		return m.Send(phone, msg)
	}); err != nil {
		return false, channelLabel(m.Name()) + ": " + sendErrText(m.Name(), err), time.Time{}
	}
	if att != nil {
		// Messenger cannot send files: text was delivered, flag the drop.
		return true, channelLabel(m.Name()) + ": attachment skipped (files not supported here), text sent", time.Now()
	}
	return true, "", time.Now()
}

// withFloodRetry runs op, sleeping the full server-ordered flood wait and
// retrying once. 3 consecutive Telegram floods trigger a 5min cooldown so a
// flood wave pauses delivery instead of snowballing into a key ban.
func (s *Service) withFloodRetry(ctx context.Context, ch Channel, op func() error) error {
	err := op()
	if err == nil {
		s.noteFlood(ch, false)
		return nil
	}
	if common.IsTelegramSkippable(err) {
		s.noteFlood(ch, false)
		return err
	}
	d, isFlood := common.FloodWaitDuration(err)
	if !isFlood {
		if common.IsTelegramAuthError(err) {
			s.noteFlood(ch, false)
			s.markDead(ch)
		}
		return err
	}
	if err := s.sleepFlood(ctx, ch, d); err != nil {
		return err
	}
	err = op()
	if err == nil {
		s.noteFlood(ch, false)
		return nil
	}
	if _, stillFlood := common.FloodWaitDuration(err); stillFlood {
		s.noteFlood(ch, true)
		if s.floodCooldownNeeded(ch) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Minute):
			}
			s.resetFlood(ch)
			return op()
		}
		return err
	}
	if common.IsTelegramAuthError(err) {
		s.markDead(ch)
	}
	s.noteFlood(ch, false)
	return err
}

// sleepFlood sleeps the full server-ordered wait plus a small jitter.
// ctx cancellation aborts the wait (batch cancel).
func (s *Service) sleepFlood(ctx context.Context, ch Channel, d time.Duration) error {
	wait := d + 2*time.Second + time.Duration(rand.Int63n(int64(3*time.Second)))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

func (s *Service) noteFlood(ch Channel, flooded bool) {
	if ch != ChannelTelegram {
		return
	}
	s.floodMu.Lock()
	defer s.floodMu.Unlock()
	if flooded {
		s.consecFlood++
	} else {
		s.consecFlood = 0
	}
}

func (s *Service) floodCooldownNeeded(ch Channel) bool {
	if ch != ChannelTelegram {
		return false
	}
	s.floodMu.Lock()
	defer s.floodMu.Unlock()
	return s.consecFlood >= 3
}

func (s *Service) resetFlood(ch Channel) {
	if ch != ChannelTelegram {
		return
	}
	s.floodMu.Lock()
	defer s.floodMu.Unlock()
	s.consecFlood = 0
}

// sendErrText distinguishes "not available / privacy" from delivery errors.
func sendErrText(ch Channel, err error) string {
	_ = ch
	if common.IsTelegramSkippable(err) {
		return "skipped: " + err.Error()
	}
	return "failed to send: " + err.Error()
}

// sortedCopy returns a sorted copy so merged channel lists are deterministic
// (parallel workers finish in random order).
func sortedCopy(ch []string) []string {
	out := append([]string(nil), ch...)
	sort.Strings(out)
	return out
}

func joinChannels(ch []string) string { // Use comma as separator for DB and UI; UI will split and render multiple badges
	out := ""
	for i, c := range ch {
		if i > 0 {
			out += ","
		}
		out += c
	}
	return out
}

func joinErrors(errs []string) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out += "; "
		}
		out += e
	}
	return out
}

func (s *Service) messengerFor(ch Channel) Messenger {
	switch ch {
	case ChannelWhatsApp:
		return s.whatsapp
	case ChannelTelegram:
		return s.telegram
	case ChannelViber:
		return s.viber
	default:
		return nil
	}
}

func channelLabel(ch Channel) string {
	switch ch {
	case ChannelWhatsApp:
		return "WhatsApp"
	case ChannelTelegram:
		return "Telegram"
	case ChannelViber:
		return "Viber"
	default:
		return string(ch)
	}
}

func (s *Service) sendOneDirect(ctx context.Context, c Contact, tmpl string, ch Channel, att *Attachment) SendResult {
	m := s.messengerFor(ch)
	if m == nil {
		return SendResult{Contact: c, Channel: ch, Status: "failed", Error: "messenger " + channelLabel(ch) + " not initialized", SentAt: time.Now()}
	}
	if s.channelDead(ch) {
		return SendResult{Contact: c, Channel: ch, Status: "failed", Error: channelLabel(ch) + ": " + SessionLostError, SentAt: time.Now()}
	}
	sent, errStr, sentAt := s.sendViaChannel(ctx, m, c.NormalizedPhone, tmpl, att)
	if !sent {
		if errStr == "" {
			errStr = "contact not found in " + channelLabel(ch)
		}
		return SendResult{Contact: c, Channel: ch, Status: "failed", Error: errStr, SentAt: time.Now()}
	}
	if errStr != "" {
		return SendResult{Contact: c, Channel: ch, Status: "sent", Error: errStr, SentAt: sentAt}
	}
	return SendResult{Contact: c, Channel: ch, Status: "sent", SentAt: sentAt}
}
