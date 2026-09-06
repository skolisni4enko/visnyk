package cascade

import (
	"context"
	"math/rand"
	"time"
)

// Service is the cascade orchestrator: WA → TG → Viber.
type Service struct {
	whatsapp Messenger
	telegram Messenger
	viber    Messenger
	// delay between sends, 8-15s + jitter to avoid bans
	minDelay time.Duration
	maxDelay time.Duration
}

// New creates a cascade service with WA > TG > Viber priority.
func New(wa, tg, vb Messenger) *Service {
	return &Service{
		whatsapp: wa,
		telegram: tg,
		viber:    vb,
		minDelay: 8 * time.Second,
		maxDelay: 15 * time.Second,
	}
}

// SendBatch sends to each contact via all available channels (broadcast).
// It respects ctx cancellation and sleeps with jitter between contacts.
func (s *Service) SendBatch(ctx context.Context, contacts []Contact, template string) []SendResult {
	return s.SendBatchWithProgress(ctx, contacts, template, nil)
}

// SendBatchDirect sends only via the specified channel (no cascade fallback).
// If contact is not available on that channel or send fails, result is failed with channel-specific error.
func (s *Service) SendBatchDirect(ctx context.Context, contacts []Contact, template string, ch Channel) []SendResult {
	return s.SendBatchDirectWithProgress(ctx, contacts, template, ch, nil)
}

// SendBatchDirectWithProgress is like SendBatchDirect but emits progress per contact.
func (s *Service) SendBatchDirectWithProgress(ctx context.Context, contacts []Contact, template string, ch Channel, onProgress func(Progress)) []SendResult {
	results := make([]SendResult, 0, len(contacts))
	total := len(contacts)
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
			onProgress(Progress{Index: i + 1, Total: total, Contact: c, Status: "checking", ETASeconds: eta})
		}

		res := s.sendOneDirect(c, template, ch)
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
			})
		}

		if i < len(contacts)-1 {
			delay := s.minDelay + time.Duration(rand.Int63n(int64(s.maxDelay-s.minDelay)))
			select {
			case <-ctx.Done():
				return results
			case <-time.After(delay):
			}
		}
	}
	return results
}

// SendBatchWithProgress is like SendBatch but calls onProgress after each contact and before delay.
// onProgress may be nil. It is called synchronously on the sender goroutine.
func (s *Service) SendBatchWithProgress(ctx context.Context, contacts []Contact, template string, onProgress func(Progress)) []SendResult {
	results := make([]SendResult, 0, len(contacts))
	total := len(contacts)
	for i, c := range contacts {
		select {
		case <-ctx.Done():
			return results
		default:
		}
		// ETA: avg 11.5s per remaining
		eta := 0
		if total > i+1 {
			eta = int(time.Duration(total-i-1) * 11 * time.Second / time.Second)
			if s.minDelay > 0 && s.maxDelay > 0 {
				avg := (s.minDelay + s.maxDelay) / 2
				eta = int(time.Duration(total-i-1) * avg / time.Second)
			}
		}
		if onProgress != nil {
			onProgress(Progress{Index: i + 1, Total: total, Contact: c, Status: "checking", ETASeconds: eta})
		}

		res := s.sendOne(ctx, c, template)
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
			})
		}

		if i < len(contacts)-1 {
			delay := s.minDelay + time.Duration(rand.Int63n(int64(s.maxDelay-s.minDelay)))
			select {
			case <-ctx.Done():
				return results
			case <-time.After(delay):
			}
		}
	}
	return results
}

func (s *Service) sendOne(_ context.Context, c Contact, tmpl string) SendResult {
	// Broadcast to all available messengers, not just first.
	// Returns aggregated result: Channel is comma-separated list of successful channels,
	// Status is "sent" if at least one succeeded, otherwise "failed".
	msg := tmpl // abstract message — no name/phone substitution
	channels := []Messenger{s.whatsapp, s.telegram, s.viber}
	var sentChannels []string
	var errs []string
	var lastSentAt time.Time
	for _, m := range channels {
		if m == nil {
			continue
		}
		ok, err := m.IsAvailable(c.NormalizedPhone)
		if err != nil {
			errs = append(errs, channelLabel(m.Name())+": перевірка не вдалася: "+err.Error())
			continue
		}
		if !ok {
			// not available — not an error for broadcast, just skip
			continue
		}
		if err := m.Send(c.NormalizedPhone, msg); err != nil {
			errs = append(errs, channelLabel(m.Name())+": не вдалося відправити: "+err.Error())
			continue
		}
		sentChannels = append(sentChannels, string(m.Name()))
		lastSentAt = time.Now()
	}
	if len(sentChannels) == 0 {
		errStr := "жоден месенджер не доступний"
		if len(errs) > 0 {
			errStr = joinErrors(errs)
		}
		return SendResult{Contact: c, Channel: ChannelNone, Status: "failed", Error: errStr, SentAt: time.Now()}
	}
	combined := Channel(joinChannels(sentChannels))
	errStr := ""
	if len(errs) > 0 {
		errStr = joinErrors(errs)
	}
	return SendResult{Contact: c, Channel: combined, Status: "sent", Error: errStr, SentAt: lastSentAt}
}

func joinChannels(ch []string) string {
	// Use comma as separator for DB and UI; UI will split and render multiple badges
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

func (s *Service) sendOneDirect(c Contact, tmpl string, ch Channel) SendResult {
	msg := tmpl
	m := s.messengerFor(ch)
	if m == nil {
		return SendResult{Contact: c, Channel: ch, Status: "failed", Error: "месенджер " + channelLabel(ch) + " не ініціалізовано", SentAt: time.Now()}
	}
	ok, err := m.IsAvailable(c.NormalizedPhone)
	if err != nil {
		return SendResult{Contact: c, Channel: ch, Status: "failed", Error: "перевірка " + channelLabel(ch) + " не вдалася: " + err.Error(), SentAt: time.Now()}
	}
	if !ok {
		return SendResult{Contact: c, Channel: ch, Status: "failed", Error: "контакт відсутній у " + channelLabel(ch), SentAt: time.Now()}
	}
	if err := m.Send(c.NormalizedPhone, msg); err != nil {
		return SendResult{Contact: c, Channel: ch, Status: "failed", Error: "не вдалося відправити в " + channelLabel(ch) + ": " + err.Error(), SentAt: time.Now()}
	}
	return SendResult{Contact: c, Channel: ch, Status: "sent", SentAt: time.Now()}
}
