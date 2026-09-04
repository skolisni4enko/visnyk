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

// SendBatch sends to each contact via first available channel.
// It respects ctx cancellation and sleeps with jitter between sends.
func (s *Service) SendBatch(ctx context.Context, contacts []Contact, template string) []SendResult {
	return s.SendBatchWithProgress(ctx, contacts, template, nil)
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
	msg := tmpl // абстрактное сообщение — без подстановки имени/телефона
	channels := []Messenger{s.whatsapp, s.telegram, s.viber}
	for _, m := range channels {
		if m == nil {
			continue
		}
		ok, err := m.IsAvailable(c.NormalizedPhone)
		if err != nil || !ok {
			continue
		}
		if err := m.Send(c.NormalizedPhone, msg); err != nil {
			return SendResult{Contact: c, Channel: m.Name(), Status: "failed", Error: err.Error(), SentAt: time.Now()}
		}
		return SendResult{Contact: c, Channel: m.Name(), Status: "sent", SentAt: time.Now()}
	}
	return SendResult{Contact: c, Channel: ChannelNone, Status: "failed", Error: "no messenger available", SentAt: time.Now()}
}
