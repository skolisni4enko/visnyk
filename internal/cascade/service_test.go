package cascade

import (
	"context"
	"testing"
)

type mockMessenger struct {
	name      Channel
	available map[string]bool
	sent      []string
	failSend  bool
}

func (m *mockMessenger) Name() Channel { return m.name }
func (m *mockMessenger) IsAvailable(phone string) (bool, error) {
	return m.available[phone], nil
}
func (m *mockMessenger) Send(phone, _ string) error {
	m.sent = append(m.sent, phone)
	if m.failSend {
		return context.Canceled
	}
	return nil
}

func TestCascadePriority_WAFirst(t *testing.T) {
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
	if results[0].Channel != ChannelWhatsApp {
		t.Errorf("want whatsapp, got %s", results[0].Channel)
	}
	if len(tg.sent) != 0 {
		t.Errorf("telegram should not be called, got %v", tg.sent)
	}
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
