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
