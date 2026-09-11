package cascade

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// mediaMock implements MediaSender: records SendMedia calls.
type mediaMock struct {
	mu         sync.Mutex
	name       Channel
	available  map[string]bool
	mediaCalls []string
	captions   []string
	textSent   []string
}

func (m *mediaMock) Name() Channel { return m.name }

func (m *mediaMock) IsAvailable(phone string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.available[phone], nil
}

func (m *mediaMock) Send(phone, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.textSent = append(m.textSent, phone)
	return nil
}

func (m *mediaMock) SendMedia(phone, caption string, att *Attachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mediaCalls = append(m.mediaCalls, phone)
	m.captions = append(m.captions, caption)
	if att == nil || len(att.Data) == 0 {
		return context.Canceled
	}
	return nil
}

func TestBroadcastWithAttachmentUsesMedia(t *testing.T) {
	wa := &mediaMock{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	tg := &mediaMock{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	att := &Attachment{FileName: "p.jpg", MIME: "image/jpeg", Size: 10, Kind: AttachmentImage, Data: []byte{1, 2, 3}}
	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi", att)
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("want sent, got %+v", results)
	}
	if len(wa.mediaCalls) != 1 || len(tg.mediaCalls) != 1 {
		t.Errorf("both channels must get SendMedia, wa=%v tg=%v", wa.mediaCalls, tg.mediaCalls)
	}
	if tg.captions[0] != "Hi" {
		t.Errorf("caption must carry the template, got %q", tg.captions[0])
	}
	if len(wa.textSent) != 0 {
		t.Errorf("plain Send must not be used when MediaSender exists, got %v", wa.textSent)
	}
}

func TestBroadcastAttachmentFallbackToText(t *testing.T) {
	// Plain messenger without MediaSender: text still delivered, drop noted.
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, nil, nil)
	svc.minDelay = 0
	svc.maxDelay = 0

	att := &Attachment{FileName: "d.pdf", MIME: "application/pdf", Size: 10, Kind: AttachmentDocument, Data: []byte{1}}
	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi", att)
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("fallback must still send text, got %+v", results)
	}
	if !strings.Contains(results[0].Error, "attachment skipped") {
		t.Errorf("fallback must note the skipped file, got %q", results[0].Error)
	}
	if len(wa.sent) != 1 {
		t.Errorf("plain Send must be called once, got %v", wa.sent)
	}
}

func TestDirectWithAttachment(t *testing.T) {
	tg := &mediaMock{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	att := &Attachment{FileName: "v.mp4", MIME: "video/mp4", Size: 10, Kind: AttachmentVideo, Data: []byte{9}}
	results := svc.SendBatchDirectWithAttachment(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Cap", ChannelTelegram, att)
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("want sent, got %+v", results)
	}
	if len(tg.mediaCalls) != 1 {
		t.Errorf("direct send must use SendMedia, got %v", tg.mediaCalls)
	}
}

func TestDirectSenderWithAttachmentFallsBackToText(t *testing.T) {
	// DirectSender without MediaSender: single resolve+send, drop noted.
	tg := &directMock{name: ChannelTelegram}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	att := &Attachment{FileName: "d.pdf", MIME: "application/pdf", Size: 10, Kind: AttachmentDocument, Data: []byte{1}}
	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi", att)
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("fallback must still send, got %+v", results)
	}
	if tg.calls != 1 {
		t.Errorf("want 1 ResolveAndSend call, got %d", tg.calls)
	}
	if !strings.Contains(results[0].Error, "attachment skipped") {
		t.Errorf("fallback must note the skipped file, got %q", results[0].Error)
	}
}

func TestSendMediaErrorFailsContact(t *testing.T) {
	// SendMedia failing (empty Data) must fail the contact, not hang.
	tg := &mediaMock{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	svc := New(nil, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	att := &Attachment{FileName: "p.jpg", MIME: "image/jpeg", Size: 10, Kind: AttachmentImage}
	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi", att)
	if len(results) != 1 || results[0].Status != "failed" {
		t.Fatalf("want failed, got %+v", results)
	}
	if !strings.Contains(results[0].Error, "failed to send") {
		t.Errorf("want delivery error, got %q", results[0].Error)
	}
}

func TestAttachmentNormalizedOnceNoRace(t *testing.T) {
	// Kind empty on purpose: parallel WA+TG workers must not race on it.
	wa := &mediaMock{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	tg := &mediaMock{name: ChannelTelegram, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, tg, nil)
	svc.minDelay = 0
	svc.maxDelay = 0
	svc.tgMinDelay = 0
	svc.tgMaxDelay = 0

	att := &Attachment{FileName: "p.jpg", MIME: "image/jpeg", Size: 3, Data: []byte{1, 2, 3}}
	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi", att)
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("want sent, got %+v", results)
	}
	if att.Kind != AttachmentImage {
		t.Errorf("entry point must normalize kind, got %q", att.Kind)
	}
}

func TestNilAttachmentBehavesLikeText(t *testing.T) {
	wa := &mediaMock{name: ChannelWhatsApp, available: map[string]bool{"+380991234567": true}}
	svc := New(wa, nil, nil)
	svc.minDelay = 0
	svc.maxDelay = 0

	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: "+380991234567"}}, "Hi", nil)
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("want sent, got %+v", results)
	}
	if len(wa.mediaCalls) != 0 || len(wa.textSent) != 1 {
		t.Errorf("nil attachment must use plain Send, media=%v text=%v", wa.mediaCalls, wa.textSent)
	}
}
