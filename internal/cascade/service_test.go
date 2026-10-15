package cascade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"visnyk/internal/common"
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

type hungMessenger struct {
	name      Channel
	available map[string]bool
	release   chan struct{}
}

func (m *hungMessenger) Name() Channel { return m.name }
func (m *hungMessenger) IsAvailable(phone string) (bool, error) {
	return m.available[phone], nil
}
func (m *hungMessenger) Send(phone, _ string) error {
	// Simulates whatsmeow SendMessage without deadline: blocks until
	// the test releases it, long after the cascade must have moved on.
	<-m.release
	return nil
}

func TestHungChannelDoesNotBlockBatch(t *testing.T) {
	wa := &hungMessenger{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}, release: make(chan struct{})}
	tg := &mockMessenger{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0
	svc.contactTimeout = 200 * time.Millisecond

	start := time.Now()
	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi")
	elapsed := time.Since(start)
	close(wa.release) // let the hung goroutine finish in background

	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != "sent" {
		t.Errorf("want sent via telegram despite hung whatsapp, got %+v", results[0])
	}
	if results[0].Channel != ChannelTelegram {
		t.Errorf("want telegram-only channel, got %s", results[0].Channel)
	}
	if !contains(results[0].Error, "timeout") {
		t.Errorf("want timeout note in error, got %q", results[0].Error)
	}
	if elapsed > 5*time.Second {
		t.Errorf("hung channel must not block batch, took %v", elapsed)
	}
}

func TestTelegramWorkerIsNotCappedByContactTimeout(t *testing.T) {
	tg := &mockMessenger{name: ChannelTelegram}
	svc := New(nil, tg, nil)
	svc.contactTimeout = 10 * time.Millisecond
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	tgSend := func(ctx context.Context, _, _ string) (bool, string, time.Time) {
		select {
		case <-time.After(50 * time.Millisecond):
			return true, "", time.Now()
		case <-ctx.Done():
			return false, "telegram send canceled", time.Time{}
		}
	}

	result := svc.sendOneCtxResolved(context.Background(), Contact{NormalizedPhone: "+380991234567"}, "Hi", nil, tgSend)
	if result.Status != "sent" || result.Channel != ChannelTelegram {
		t.Fatalf("Telegram wait longer than WA contact timeout must complete, got %+v", result)
	}
}

func TestTelegramFloodWaitRetriesPastContactTimeout(t *testing.T) {
	tg := &mockMessenger{name: ChannelTelegram}
	svc := New(nil, tg, nil)
	svc.contactTimeout = 10 * time.Millisecond
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	var calls int
	result := svc.sendOneCtxResolved(context.Background(), Contact{NormalizedPhone: "+380991234567"}, "Hi", nil, func(ctx context.Context, _, _ string) (bool, string, time.Time) {
		err := svc.withFloodRetry(ctx, ChannelTelegram, func() error {
			calls++
			if calls == 1 {
				return newFloodError()
			}
			return nil
		})
		if err != nil {
			return false, err.Error(), time.Time{}
		}
		return true, "", time.Now()
	})
	if result.Status != "sent" || result.Channel != ChannelTelegram || calls != 2 {
		t.Fatalf("flood wait must finish and retry after the WA timeout, calls=%d result=%+v", calls, result)
	}
}

func TestPartialMediaDeliveryIsNotRetried(t *testing.T) {
	const phone = "+380991234567"
	tg := &mediaMock{
		name:      ChannelTelegram,
		available: map[string]bool{phone: true},
		mediaErr:  &PartialDeliveryError{Err: errors.New("file sent; follow-up text failed")},
	}
	svc := New(nil, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	att := &Attachment{FileName: "picture.jpg", MIME: "image/jpeg", Size: 1, Data: []byte{1}}

	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: phone}}, "caption", att)
	if len(results) != 1 || results[0].Status != "sent" || results[0].Channel != ChannelTelegram {
		t.Fatalf("file delivery must be reported as sent after follow-up failure, got %+v", results)
	}
	if !strings.Contains(results[0].Error, "follow-up text failed") {
		t.Errorf("partial delivery note missing: %q", results[0].Error)
	}
	if len(tg.mediaCalls) != 1 {
		t.Errorf("partial media delivery must not be retried, got %d sends", len(tg.mediaCalls))
	}
}

func TestMediaFloodWaitDoesNotRetryBeforeWaiting(t *testing.T) {
	svc := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	sent, errStr, _ := svc.sendMediaWithFloodRetry(ctx, ChannelTelegram, func() error {
		calls++
		return newFloodError()
	})
	if sent {
		t.Fatalf("canceled flood wait must not report delivery")
	}
	if calls != 1 {
		t.Errorf("must not retry before honoring FLOOD_WAIT, got %d attempts", calls)
	}
	if !strings.Contains(errStr, "context canceled") {
		t.Errorf("want cancellation reported while waiting, got %q", errStr)
	}
}

func TestFloodRetryWaitsBeforeSecondAttempt(t *testing.T) {
	svc := New(nil, nil, nil)
	var events []string
	svc.sleepFloodFn = func(_ context.Context, _ Channel, wait time.Duration) error {
		events = append(events, "wait")
		if wait != 0 {
			t.Errorf("FLOOD_WAIT_0 duration = %v, want 0", wait)
		}
		return nil
	}
	var calls int
	err := svc.withFloodRetry(context.Background(), ChannelTelegram, func() error {
		calls++
		events = append(events, fmt.Sprintf("attempt-%d", calls))
		if calls == 1 {
			return newFloodError()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withFloodRetry() error = %v", err)
	}
	if got, want := strings.Join(events, ","), "attempt-1,wait,attempt-2"; got != want {
		t.Errorf("event order = %q, want %q", got, want)
	}
}

func TestFloodRetryHonorsSecondServerWait(t *testing.T) {
	svc := New(nil, nil, nil)
	var events []string
	svc.sleepFloodFn = func(_ context.Context, _ Channel, _ time.Duration) error {
		events = append(events, "wait")
		return nil
	}
	calls := 0
	err := svc.withFloodRetry(context.Background(), ChannelTelegram, func() error {
		calls++
		events = append(events, fmt.Sprintf("attempt-%d", calls))
		return newFloodError()
	})
	if err == nil {
		t.Fatal("second FLOOD_WAIT must return an error after its wait")
	}
	if got, want := strings.Join(events, ","), "attempt-1,wait,attempt-2,wait"; got != want {
		t.Errorf("event order = %q, want %q", got, want)
	}
}

func TestFloodRetryHonorsUpdatedSecondWaitDuration(t *testing.T) {
	svc := New(nil, nil, nil)
	var waits []time.Duration
	svc.sleepFloodFn = func(_ context.Context, _ Channel, wait time.Duration) error {
		waits = append(waits, wait)
		return nil
	}
	calls := 0
	err := svc.withFloodRetry(context.Background(), ChannelTelegram, func() error {
		calls++
		if calls == 1 {
			return tgerr.New(420, "FLOOD_WAIT_2")
		}
		return tgerr.New(420, "FLOOD_WAIT_120")
	})
	if err == nil {
		t.Fatal("second FLOOD_WAIT must return an error")
	}
	if len(waits) != 2 || waits[0] != 2*time.Second || waits[1] != 120*time.Second {
		t.Errorf("waits = %v, want [2s 2m]", waits)
	}
}

func TestFloodCooldownUsesLongerSecondWait(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.consecFlood = 2
	var waits []time.Duration
	svc.sleepFloodFn = func(_ context.Context, _ Channel, wait time.Duration) error {
		waits = append(waits, wait)
		return nil
	}
	calls := 0
	err := svc.withFloodRetry(context.Background(), ChannelTelegram, func() error {
		calls++
		switch calls {
		case 1:
			return tgerr.New(420, "FLOOD_WAIT_2")
		case 2:
			return tgerr.New(420, "FLOOD_WAIT_600")
		default:
			return nil
		}
	})
	if err != nil {
		t.Fatalf("withFloodRetry() error = %v", err)
	}
	if len(waits) != 2 || waits[0] != 2*time.Second || waits[1] != 600*time.Second {
		t.Errorf("waits = %v, want [2s 10m]", waits)
	}
}

func TestFloodCooldownRetryMarksFinalAuthErrorDead(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.consecFlood = 2
	var events []string
	svc.sleepFloodFn = func(_ context.Context, _ Channel, wait time.Duration) error {
		events = append(events, "wait:"+wait.String())
		return nil
	}
	calls := 0
	err := svc.withFloodRetry(context.Background(), ChannelTelegram, func() error {
		calls++
		events = append(events, fmt.Sprintf("attempt-%d", calls))
		if calls < 3 {
			return newFloodError()
		}
		return tgerr.New(401, "AUTH_KEY_UNREGISTERED")
	})
	if !common.IsTelegramAuthError(err) {
		t.Fatalf("want final auth error, got %v", err)
	}
	if !svc.channelDead(ChannelTelegram) {
		t.Error("final auth error after flood cooldown must mark Telegram dead")
	}
	if calls != 3 || len(events) != 5 {
		t.Errorf("want two flood waits and three attempts, calls=%d events=%v", calls, events)
	}
}

func TestFloodAfterCooldownStillHonorsServerWait(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.consecFlood = 2
	var waits []time.Duration
	svc.sleepFloodFn = func(_ context.Context, _ Channel, wait time.Duration) error {
		waits = append(waits, wait)
		return nil
	}
	calls := 0
	err := svc.withFloodRetry(context.Background(), ChannelTelegram, func() error {
		calls++
		return newFloodError()
	})
	if err == nil {
		t.Fatal("final FLOOD_WAIT after cooldown must remain an error")
	}
	if calls != 3 {
		t.Errorf("want exactly three attempts including cooldown retry, got %d", calls)
	}
	if len(waits) != 3 || waits[1] < 5*time.Minute {
		t.Errorf("want initial, 5-minute cooldown, and final server waits, got %v", waits)
	}
}

func TestSuccessfulMediaSendResetsTelegramFloodCounter(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.noteFlood(ChannelTelegram, true)
	if svc.consecFlood != 1 {
		t.Fatal("test setup did not record a Telegram flood")
	}
	sent, errStr, _ := svc.sendMediaWithFloodRetry(context.Background(), ChannelTelegram, func() error { return nil })
	if !sent || errStr != "" {
		t.Fatalf("want successful media send, got sent=%v err=%q", sent, errStr)
	}
	if svc.consecFlood != 0 {
		t.Errorf("successful media send must reset consecutive flood count, got %d", svc.consecFlood)
	}
}

// batchResolverMock implements Messenger + BatchResolver + DirectSender to
// verify the batch broadcast path: BatchImport once, SendResolved per found
// contact, and zero ResolveAndSend (resolvePhone) calls.
type batchResolverMock struct {
	mu                   sync.Mutex
	name                 Channel
	found                map[string]bool
	importCalls          int
	beginBatchCalls      int
	endBatchCalls        int
	deleteCalls          int
	deleteCompletedCalls int
	deleteContacts       []ImportedContact
	sendBeforeDelete     bool
	deleteStarted        chan struct{}
	deleteRelease        chan struct{}
	deleteErr            error
	importErr            error
	importStarted        chan struct{}
	importRelease        chan struct{}
	resolveCalls         int
	sent                 []string
}

func (m *batchResolverMock) Name() Channel { return m.name }
func (m *batchResolverMock) IsAvailable(phone string) (bool, error) {
	return false, nil
}
func (m *batchResolverMock) Send(phone, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, phone)
	return nil
}
func (m *batchResolverMock) ResolveAndSend(phone, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolveCalls++
	m.sent = append(m.sent, phone)
	return nil
}
func (m *batchResolverMock) BeginBatch(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beginBatchCalls++
	return nil
}
func (m *batchResolverMock) EndBatch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endBatchCalls++
}
func (m *batchResolverMock) BatchImport(_ context.Context, phones []string) (map[string]bool, []ImportedContact, error) {
	m.mu.Lock()
	m.importCalls++
	found := make(map[string]bool, len(phones))
	var toDelete []ImportedContact
	for _, p := range phones {
		// Mirror the real Telegram service: the found map is keyed by
		// canonical E.164, whatever form the caller passed in.
		key := common.CleanPhoneE164(p)
		if m.found[key] {
			found[key] = true
			toDelete = append(toDelete, ImportedContact{UserID: int64(len(toDelete) + 1), AccessHash: 1})
		}
	}
	started, release, importErr := m.importStarted, m.importRelease, m.importErr
	m.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		<-release
	}
	return found, toDelete, importErr
}
func (m *batchResolverMock) BatchDelete(_ context.Context, contacts []ImportedContact) error {
	m.mu.Lock()
	m.deleteCalls++
	m.deleteContacts = append(m.deleteContacts, contacts...)
	started, release := m.deleteStarted, m.deleteRelease
	m.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		<-release
	}
	m.mu.Lock()
	m.deleteCompletedCalls++
	m.mu.Unlock()
	return m.deleteErr
}
func (m *batchResolverMock) SendResolved(_ context.Context, phone, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.deleteContacts) > 0 && m.deleteCompletedCalls == 0 {
		m.sendBeforeDelete = true
	}
	key := common.CleanPhoneE164(phone)
	if !m.found[key] {
		return errors.New("user not found for " + key)
	}
	m.sent = append(m.sent, key)
	return nil
}

// TestBroadcastBatchLookupUsesCanonicalKey feeds cascade a raw-ish
// NormalizedPhone (mixed spacing) and a mock whose found map is keyed by
// canonical E.164, as the real Telegram service does. If cascade did not
// canonicalize before the lookup, Telegram would be silently skipped.
func TestBroadcastBatchLookupUsesCanonicalKey(t *testing.T) {
	const raw = "380 99 123-45-67"
	const canonical = "+380991234567"
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{canonical: true}}
	tg := &batchResolverMock{name: ChannelTelegram, found: map[string]bool{canonical: true}}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0

	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: raw}}, "Hi")
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != "sent" {
		t.Fatalf("want sent, got %+v", res)
	}
	if !contains(string(res.Channel), "telegram") {
		t.Errorf("want telegram delivered despite non-canonical input, got %s (err=%q)", res.Channel, res.Error)
	}
}

func TestBroadcastBatchResolverNoResolvePhone(t *testing.T) {
	phones := []string{
		"+380991234500", "+380991234501", "+380991234502", "+380991234503", "+380991234504",
		"+380991234505", "+380991234506", "+380991234507", "+380991234508", "+380991234509",
	}
	waAvailable := make(map[string]bool, len(phones))
	tgFound := make(map[string]bool, len(phones))
	contacts := make([]Contact, 0, len(phones))
	for _, p := range phones {
		waAvailable[p] = true
		tgFound[p] = true
		contacts = append(contacts, Contact{NormalizedPhone: p})
	}
	wa := &mockMessenger{name: ChannelWhatsApp, available: waAvailable}
	tg := &batchResolverMock{name: ChannelTelegram, found: tgFound}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	results := svc.SendBatch(context.Background(), contacts, "Hi")
	if len(results) != 10 {
		t.Fatalf("want 10 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "sent" {
			t.Errorf("want sent, got %+v", r)
		}
		if !contains(string(r.Channel), "whatsapp") || !contains(string(r.Channel), "telegram") {
			t.Errorf("want whatsapp+telegram, got %s", r.Channel)
		}
	}
	if tg.importCalls != 1 {
		t.Errorf("want 1 BatchImport call, got %d", tg.importCalls)
	}
	if tg.beginBatchCalls != 1 {
		t.Errorf("want 1 BeginBatch call, got %d", tg.beginBatchCalls)
	}
	if tg.resolveCalls != 0 {
		t.Errorf("batch path must not call ResolveAndSend (resolvePhone), got %d", tg.resolveCalls)
	}
	if tg.sendBeforeDelete {
		t.Error("Telegram sends must start only after newly imported contacts are removed")
	}
	if len(tg.sent) != 10 {
		t.Errorf("want 10 SendResolved sends, got %d", len(tg.sent))
	}
}

func TestBroadcastBatchResolverNotFoundSkipsTelegram(t *testing.T) {
	phones := []string{
		"+380991234500", "+380991234501", "+380991234502", "+380991234503", "+380991234504",
		"+380991234505", "+380991234506", "+380991234507", "+380991234508", "+380991234509",
	}
	waAvailable := make(map[string]bool, len(phones))
	tgFound := map[string]bool{phones[0]: true} // only the first is on Telegram
	contacts := make([]Contact, 0, len(phones))
	for _, p := range phones {
		waAvailable[p] = true
		contacts = append(contacts, Contact{NormalizedPhone: p})
	}
	wa := &mockMessenger{name: ChannelWhatsApp, available: waAvailable}
	tg := &batchResolverMock{name: ChannelTelegram, found: tgFound}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	results := svc.SendBatch(context.Background(), contacts, "Hi")
	if len(results) != 10 {
		t.Fatalf("want 10 results, got %d", len(results))
	}
	// First contact delivered to both; the rest are WhatsApp-only.
	if !contains(string(results[0].Channel), "whatsapp") || !contains(string(results[0].Channel), "telegram") {
		t.Errorf("want whatsapp+telegram for found contact, got %s", results[0].Channel)
	}
	for i := 1; i < 10; i++ {
		if results[i].Channel != ChannelWhatsApp {
			t.Errorf("want whatsapp-only for not-found contact %d, got %s", i, results[i].Channel)
		}
		if results[i].Status != "sent" {
			t.Errorf("want sent (whatsapp) for contact %d, got %+v", i, results[i])
		}
		// The skipped Telegram channel must be visible, not a silent drop.
		if !strings.Contains(results[i].Error, "not found in Telegram") {
			t.Errorf("want Telegram-not-found note for contact %d, got %q", i, results[i].Error)
		}
	}
	if tg.resolveCalls != 0 {
		t.Errorf("batch path must not call ResolveAndSend, got %d", tg.resolveCalls)
	}
	if len(tg.sent) != 1 {
		t.Errorf("want 1 SendResolved send (only found), got %d", len(tg.sent))
	}
}

// TestBroadcastBatchPathUsedForSingleContact pins the contract that a
// broadcast to one number behaves exactly like a broadcast to many: the
// batch-resolve path runs (one BatchImport, no per-contact resolvePhone) and
// the message goes to both WhatsApp and Telegram. Batch size must not decide
// which channels are attempted.
func TestBroadcastBatchPathUsedForSingleContact(t *testing.T) {
	const phone = "+380991234567"
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{phone: true}}
	tg := &batchResolverMock{name: ChannelTelegram, found: map[string]bool{phone: true}}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0

	results := svc.SendBatch(context.Background(), []Contact{{Name: "Ivan", NormalizedPhone: phone}}, "Hi")
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != "sent" {
		t.Fatalf("want sent, got %+v", res)
	}
	if !contains(string(res.Channel), "whatsapp") || !contains(string(res.Channel), "telegram") {
		t.Errorf("want whatsapp+telegram, got %s", res.Channel)
	}
	if tg.importCalls != 1 || tg.beginBatchCalls != 1 || tg.endBatchCalls != 1 {
		t.Errorf("want batch path lifecycle (1 import/begin/end), got import=%d begin=%d end=%d", tg.importCalls, tg.beginBatchCalls, tg.endBatchCalls)
	}
	if tg.resolveCalls != 0 {
		t.Errorf("batch path must not call ResolveAndSend, got %d", tg.resolveCalls)
	}
}

// TestBroadcastThreeContactsBothChannels covers the small-batch case that
// used to take a different code path.
func TestBroadcastThreeContactsBothChannels(t *testing.T) {
	phones := []string{"+380991234500", "+380991234501", "+380991234502"}
	waAvailable := make(map[string]bool, len(phones))
	tgFound := make(map[string]bool, len(phones))
	contacts := make([]Contact, 0, len(phones))
	for _, p := range phones {
		waAvailable[p] = true
		tgFound[p] = true
		contacts = append(contacts, Contact{NormalizedPhone: p})
	}
	wa := &mockMessenger{name: ChannelWhatsApp, available: waAvailable}
	tg := &batchResolverMock{name: ChannelTelegram, found: tgFound}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0

	results := svc.SendBatch(context.Background(), contacts, "Hi")
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Status != "sent" {
			t.Errorf("contact %d: want sent, got %+v", i, r)
		}
		if !contains(string(r.Channel), "whatsapp") || !contains(string(r.Channel), "telegram") {
			t.Errorf("contact %d: want whatsapp+telegram, got %s", i, r.Channel)
		}
	}
	if tg.importCalls != 1 {
		t.Errorf("want a single BatchImport call, got %d", tg.importCalls)
	}
}

// TestBroadcastWhatsAppNotFoundIsReported checks the reciprocal case: the
// number exists only on Telegram, and the skipped WhatsApp channel is named in
// the result instead of being dropped silently.
func TestBroadcastWhatsAppNotFoundIsReported(t *testing.T) {
	const phone = "+380991234567"
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{}}
	tg := &batchResolverMock{name: ChannelTelegram, found: map[string]bool{phone: true}}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0

	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: phone}}, "Hi")
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != "sent" {
		t.Fatalf("want sent via Telegram, got %+v", res)
	}
	if res.Channel != ChannelTelegram {
		t.Errorf("want telegram only, got %s", res.Channel)
	}
	if !strings.Contains(res.Error, "not found in WhatsApp") {
		t.Errorf("want WhatsApp-not-found note, got %q", res.Error)
	}
}

func TestDirectTelegramUsesBatchResolverForSingleContact(t *testing.T) {
	const phone = "+380991234567"
	tg := &batchResolverMock{name: ChannelTelegram, found: map[string]bool{phone: true}}
	svc := New(nil, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0

	results := svc.SendBatchDirect(context.Background(), []Contact{{NormalizedPhone: phone}}, "message", ChannelTelegram)
	if len(results) != 1 || results[0].Status != "sent" || results[0].Channel != ChannelTelegram {
		t.Fatalf("want Telegram-only direct send, got %+v", results)
	}
	if tg.importCalls != 1 || tg.beginBatchCalls != 1 || tg.resolveCalls != 0 || len(tg.sent) != 1 {
		t.Errorf("want batch resolve/send only, import=%d begin=%d resolve=%d sent=%d", tg.importCalls, tg.beginBatchCalls, tg.resolveCalls, len(tg.sent))
	}
	if tg.deleteCalls != 1 || tg.deleteCompletedCalls != 1 || tg.sendBeforeDelete {
		t.Errorf("direct Telegram must delete temporary contacts before send, delete calls=%d complete=%d sent early=%v", tg.deleteCalls, tg.deleteCompletedCalls, tg.sendBeforeDelete)
	}
}

func TestTelegramPacingCancellationIsReported(t *testing.T) {
	tg := &batchResolverMock{name: ChannelTelegram}
	svc := New(nil, tg, nil)
	svc.tgMinDelay, svc.tgMaxDelay = time.Second, time.Second
	svc.paceMu.Lock()
	svc.lastSend[ChannelTelegram] = time.Now()
	svc.paceMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	sent, errStr, _ := svc.sendResolvedViaChannel(ctx, tg, "+380991234567", "Hi")
	if sent || !strings.Contains(errStr, "context canceled") {
		t.Fatalf("pacing cancellation must be visible, sent=%v error=%q", sent, errStr)
	}
}
