package cascade

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
)

func newFloodError() error { return tgerr.New(420, "FLOOD_WAIT_0") }

var errPrivacy = errors.New("send: rpc error code 403: PRIVACY_PREMIUM_REQUIRED")

type mockMessenger struct {
	mu        sync.Mutex
	name      Channel
	available map[string]bool
	sent      []string
	failSend  bool
}

func (m *mockMessenger) Name() Channel { return m.name }
func (m *mockMessenger) IsAvailable(phone string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.available[phone], nil
}
func (m *mockMessenger) Send(phone, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, phone)
	if m.failSend {
		return context.Canceled
	}
	return nil
}

func TestCascadePriority_WAFirst(t *testing.T) {
	// Broadcast: when both WA and TG available, should send to both (not just first)
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	tg := &mockMessenger{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, tg, nil)
	// speed up test — no delay
	svc.minDelay = 0
	svc.maxDelay = 0

	contacts := []Contact{{Name: "Ivan", NormalizedPhone: "+380991234567"}}
	results := svc.SendBatch(context.Background(), contacts, "Hello %s")
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	// Channel should be combined "whatsapp,telegram"
	if results[0].Channel != Channel("whatsapp,telegram") && results[0].Channel != Channel("telegram,whatsapp") {
		t.Errorf("want combined whatsapp,telegram got %s", results[0].Channel)
	}
	if len(wa.sent) != 1 {
		t.Errorf("whatsapp should be called once, got %v", wa.sent)
	}
	if len(tg.sent) != 1 {
		t.Errorf("telegram should be called once (broadcast), got %v", tg.sent)
	}
	if results[0].Status != "sent" {
		t.Errorf("want sent status, got %s", results[0].Status)
	}
}

func TestCascadeBroadcastAll(t *testing.T) {
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	tg := &mockMessenger{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	vb := &mockMessenger{name: ChannelViber, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, tg, vb)
	svc.minDelay = 0
	svc.maxDelay = 0
	contacts := []Contact{{NormalizedPhone: "+380991234567"}}
	results := svc.SendBatch(context.Background(), contacts, "Hi")
	if len(results) != 1 {
		t.Fatalf("want 1 aggregated result, got %d", len(results))
	}
	ch := string(results[0].Channel)
	if !(contains(ch, "whatsapp") && contains(ch, "telegram") && contains(ch, "viber")) {
		t.Errorf("want all three channels, got %s", ch)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func TestCascadeFallbackToTelegram(t *testing.T) {
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": false}}
	tg := &mockMessenger{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0

	contacts := []Contact{{Name: "Olena", NormalizedPhone: "+380991234567"}}
	results := svc.SendBatch(context.Background(), contacts, "Hi %s")
	if results[0].Channel != ChannelTelegram {
		t.Errorf("want telegram fallback, got %s", results[0].Channel)
	}
}

func TestCascadeNoneAvailable(t *testing.T) {
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{}}
	tg := &mockMessenger{name: ChannelTelegram, available: map[string]bool{}}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0

	contacts := []Contact{{Name: "Petro", NormalizedPhone: "+380991234567"}}
	results := svc.SendBatch(context.Background(), contacts, "Hi %s")
	if results[0].Channel != ChannelNone {
		t.Errorf("want none, got %s", results[0].Channel)
	}
	if results[0].Status != "failed" {
		t.Errorf("want failed status, got %s", results[0].Status)
	}
}

func TestCascadeContextCancel(t *testing.T) {
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, nil, nil)
	svc.minDelay = 0
	svc.maxDelay = 0

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	contacts := []Contact{
		{Name: "A", NormalizedPhone: "+380991234567"},
		{Name: "B", NormalizedPhone: "+380991234568"},
	}
	results := svc.SendBatch(ctx, contacts, "Hi %s")
	if len(results) != 0 {
		t.Errorf("cancelled context should return 0 results, got %d", len(results))
	}
}

// directMock implements DirectSender: single resolve+send call.
type directMock struct {
	mu         sync.Mutex
	name       Channel
	calls      int
	checkCalls int
	failFirst  error
	failAlways error
	sent       []string
}

func (m *directMock) Name() Channel { return m.name }
func (m *directMock) IsAvailable(phone string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkCalls++
	return true, nil
}
func (m *directMock) Send(phone, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, phone)
	return nil
}
func (m *directMock) ResolveAndSend(phone, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.failAlways != nil {
		return m.failAlways
	}
	if m.failFirst != nil && m.calls == 1 {
		return m.failFirst
	}
	m.sent = append(m.sent, phone)
	return nil
}

func TestDirectSenderFastPath(t *testing.T) {
	tg := &directMock{name: ChannelTelegram}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi")
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("want sent, got %+v", results)
	}
	if tg.calls != 1 {
		t.Errorf("want 1 ResolveAndSend call, got %d", tg.calls)
	}
	if tg.checkCalls != 0 {
		t.Errorf("fast path must not call IsAvailable, got %d calls", tg.checkCalls)
	}
}

func TestDirectSenderFloodRetry(t *testing.T) {
	tg := &directMock{name: ChannelTelegram, failFirst: newFloodError()}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi")
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("want sent after flood retry, got %+v", results)
	}
	if tg.calls != 2 {
		t.Errorf("want 2 calls (flood + retry), got %d", tg.calls)
	}
}

func TestDirectSenderSkippableNoRetry(t *testing.T) {
	tg := &directMock{name: ChannelTelegram, failAlways: errPrivacy}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi")
	if len(results) != 1 || results[0].Status != "failed" {
		t.Fatalf("want failed, got %+v", results)
	}
	if tg.calls != 1 {
		t.Errorf("skippable error must not be retried, got %d calls", tg.calls)
	}
	if !contains(results[0].Error, "skipped") {
		t.Errorf("want 'skipped' in error, got %q", results[0].Error)
	}
}

func TestTelegramPaceCap30s(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.tgMaxDelay = 2 * time.Minute
	_, max := svc.tgPaceBounds()
	if max != TelegramPaceMax {
		t.Errorf("want TG max capped at %v, got %v", TelegramPaceMax, max)
	}
}

func TestParallelChannelsMerge(t *testing.T) {
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	tg := &directMock{name: ChannelTelegram}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi")
	if len(results) != 1 {
		t.Fatalf("want 1 aggregated result, got %d", len(results))
	}
	ch := string(results[0].Channel)
	if !(contains(ch, "whatsapp") && contains(ch, "telegram")) {
		t.Errorf("want merged whatsapp+telegram, got %s", ch)
	}
}

func TestBroadcastNoDoubleDelay(t *testing.T) {
	// Pacing must come from paceChannel only: with 2s WA spacing and
	// 3 contacts, the batch takes ~4s. With the old extra inter-contact
	// sleep it would take ~8s.
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{
		"+380991234567": true, "+380991234568": true, "+380991234569": true,
	}}
	tg := &directMock{name: ChannelTelegram}
	svc := New(wa, tg, nil)
	svc.minDelay = 2 * time.Second
	svc.maxDelay = 2 * time.Second
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	contacts := []Contact{
		{NormalizedPhone: "+380991234567"},
		{NormalizedPhone: "+380991234568"},
		{NormalizedPhone: "+380991234569"},
	}
	start := time.Now()
	results := svc.SendBatch(context.Background(), contacts, "Hi")
	elapsed := time.Since(start)
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "sent" {
			t.Errorf("want sent, got %+v", r)
		}
	}
	if elapsed < 3*time.Second {
		t.Errorf("per-channel pacing must still apply, batch finished in %v", elapsed)
	}
	if elapsed > 7*time.Second {
		t.Errorf("no double delay allowed, batch took %v", elapsed)
	}
}

func TestDirectFastPathFloodRetry(t *testing.T) {
	tg := &directMock{name: ChannelTelegram, failFirst: newFloodError()}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	results := svc.SendBatchDirect(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi", ChannelTelegram)
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("want sent after flood retry, got %+v", results)
	}
	if tg.calls != 2 {
		t.Errorf("want 2 ResolveAndSend calls (flood + retry), got %d", tg.calls)
	}
	if tg.checkCalls != 0 {
		t.Errorf("direct fast path must not call IsAvailable, got %d", tg.checkCalls)
	}
}

func TestDeadChannelSkipped(t *testing.T) {
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{
		"+380991234567": true, "+380991234568": true, "+380991234569": true,
	}}
	tg := &directMock{name: ChannelTelegram, failAlways: tgerr.New(401, "AUTH_KEY_UNREGISTERED")}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	contacts := []Contact{
		{NormalizedPhone: "+380991234567"},
		{NormalizedPhone: "+380991234568"},
		{NormalizedPhone: "+380991234569"},
	}
	results := svc.SendBatch(context.Background(), contacts, "Hi")
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	// WhatsApp keeps delivering despite the dead Telegram key.
	for _, r := range results {
		if r.Status != "sent" || !contains(string(r.Channel), "whatsapp") {
			t.Errorf("want WA-sent for all, got %+v", r)
		}
	}
	// Only the first contact burns a Telegram API call; the rest skip instantly.
	if tg.calls != 1 {
		t.Errorf("want 1 Telegram call before circuit breaker trips, got %d", tg.calls)
	}
	for _, r := range results[1:] {
		if !contains(r.Error, SessionLostError) {
			t.Errorf("want %q in error, got %q", SessionLostError, r.Error)
		}
	}
}

func TestDirectDeadChannelAbortsFast(t *testing.T) {
	tg := &directMock{name: ChannelTelegram, failAlways: tgerr.New(401, "AUTH_KEY_UNREGISTERED")}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	contacts := []Contact{
		{NormalizedPhone: "+380991234567"},
		{NormalizedPhone: "+380991234568"},
		{NormalizedPhone: "+380991234569"},
	}
	start := time.Now()
	results := svc.SendBatchDirect(context.Background(), contacts, "Hi", ChannelTelegram)
	elapsed := time.Since(start)
	if len(results) != 3 {
		t.Fatalf("want 3 failed results (one per contact), got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "failed" {
			t.Errorf("want failed, got %+v", r)
		}
	}
	if tg.calls != 1 {
		t.Errorf("want 1 Telegram call, got %d", tg.calls)
	}
	if elapsed > 5*time.Second {
		t.Errorf("dead channel must abort fast without pacing sleeps, took %v", elapsed)
	}
}
