package cascade

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
)

// mediaMock implements MediaSender: records SendMedia calls.
type mediaMock struct {
	mu         sync.Mutex
	name       Channel
	available  map[string]bool
	mediaCalls []string
	captions   []string
	textSent   []string
	mediaErr   error
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
	return m.mediaErr
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

type resolvedMediaMock struct {
	*batchResolverMock
	mediaPhones []string
	captions    []string
	attachments []*Attachment
	mediaErr    error
}

func (m *resolvedMediaMock) SendResolvedMedia(_ context.Context, phone, caption string, att *Attachment) error {
	m.batchResolverMock.mu.Lock()
	defer m.batchResolverMock.mu.Unlock()
	if len(m.batchResolverMock.deleteContacts) > 0 && m.batchResolverMock.deleteCompletedCalls == 0 {
		m.batchResolverMock.sendBeforeDelete = true
	}
	m.mediaPhones = append(m.mediaPhones, phone)
	m.captions = append(m.captions, caption)
	m.attachments = append(m.attachments, att)
	return m.mediaErr
}

func TestBroadcastMediaUsesBatchResolvedTelegramForAnyBatchSize(t *testing.T) {
	for _, phones := range [][]string{
		{"+380991234500"},
		{"+380991234500", "+380991234501", "+380991234502"},
	} {
		waAvailable := make(map[string]bool, len(phones))
		tgFound := make(map[string]bool, len(phones))
		contacts := make([]Contact, 0, len(phones))
		for _, phone := range phones {
			waAvailable[phone] = true
			tgFound[phone] = true
			contacts = append(contacts, Contact{NormalizedPhone: phone})
		}
		wa := &mediaMock{name: ChannelWhatsApp, available: waAvailable}
		tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{name: ChannelTelegram, found: tgFound}}
		svc := New(wa, tg, nil)
		svc.minDelay, svc.maxDelay = 0, 0
		svc.tgMinDelay, svc.tgMaxDelay = 0, 0
		att := &Attachment{FileName: "picture.jpg", MIME: "image/jpeg", Size: 3, Data: []byte{1, 2, 3}}

		results := svc.SendBatchWithAttachment(context.Background(), contacts, "caption", att)
		if len(results) != len(phones) {
			t.Fatalf("batch %d: want %d results, got %d", len(phones), len(phones), len(results))
		}
		for i, result := range results {
			if result.Status != "sent" || !contains(string(result.Channel), "whatsapp") || !contains(string(result.Channel), "telegram") {
				t.Errorf("batch %d contact %d: want sent via both channels, got %+v", len(phones), i, result)
			}
		}
		if tg.importCalls != 1 || tg.beginBatchCalls != 1 || tg.endBatchCalls != 1 {
			t.Errorf("batch %d: want one import/begin/end lifecycle, import=%d begin=%d end=%d", len(phones), tg.importCalls, tg.beginBatchCalls, tg.endBatchCalls)
		}
		if tg.resolveCalls != 0 {
			t.Errorf("batch %d: media send must not resolve each phone, got %d resolve calls", len(phones), tg.resolveCalls)
		}
		if tg.sendBeforeDelete {
			t.Errorf("batch %d: Telegram media started before temporary contacts were removed", len(phones))
		}
		if len(wa.mediaCalls) != len(phones) || len(wa.textSent) != 0 {
			t.Errorf("batch %d: want WhatsApp media for every contact and no text-only sends, media=%v text=%v", len(phones), wa.mediaCalls, wa.textSent)
		}
		if tg.deleteCalls != 1 || tg.deleteCompletedCalls != 1 || len(tg.deleteContacts) != len(phones) {
			t.Errorf("batch %d: want completed cleanup of %d temporary contacts, calls=%d completed=%d deleted=%d", len(phones), len(phones), tg.deleteCalls, tg.deleteCompletedCalls, len(tg.deleteContacts))
		}
		if len(tg.mediaPhones) != len(phones) {
			t.Errorf("batch %d: want %d resolved media sends, got %d", len(phones), len(phones), len(tg.mediaPhones))
		}
		for i, phone := range phones {
			if i >= len(tg.mediaPhones) {
				break
			}
			if tg.mediaPhones[i] != phone || tg.captions[i] != "caption" || tg.attachments[i] != att {
				t.Errorf("batch %d media %d: phone/caption/attachment mismatch", len(phones), i)
			}
		}
	}
}

func TestDirectTelegramMediaUsesBatchResolveForSingleContact(t *testing.T) {
	const phone = "+380991234567"
	tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{name: ChannelTelegram, found: map[string]bool{phone: true}}}
	svc := New(nil, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	att := &Attachment{FileName: "document.pdf", MIME: "application/pdf", Size: 3, Data: []byte{1, 2, 3}}

	results := svc.SendBatchDirectWithAttachment(context.Background(), []Contact{{NormalizedPhone: phone}}, "caption", ChannelTelegram, att)
	if len(results) != 1 || results[0].Status != "sent" || results[0].Channel != ChannelTelegram {
		t.Fatalf("want single Telegram media delivery, got %+v", results)
	}
	if tg.importCalls != 1 || tg.beginBatchCalls != 1 || tg.endBatchCalls != 1 || len(tg.mediaPhones) != 1 || tg.mediaPhones[0] != phone {
		t.Errorf("want one batch lifecycle and resolved media send, import=%d begin=%d end=%d media=%v", tg.importCalls, tg.beginBatchCalls, tg.endBatchCalls, tg.mediaPhones)
	}
	if tg.resolveCalls != 0 {
		t.Errorf("direct media must not resolve each phone, got %d calls", tg.resolveCalls)
	}
	if tg.sendBeforeDelete {
		t.Error("direct Telegram media started before temporary contact was removed")
	}
	if tg.deleteCalls != 1 || tg.deleteCompletedCalls != 1 || len(tg.deleteContacts) != 1 {
		t.Errorf("want temporary contact deleted before return, calls=%d completed=%d deleted=%d", tg.deleteCalls, tg.deleteCompletedCalls, len(tg.deleteContacts))
	}
}

func TestResolvedMediaPartialDeliveryIsNotRetried(t *testing.T) {
	const phone = "+380991234567"
	tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{
		name:  ChannelTelegram,
		found: map[string]bool{phone: true},
	}, mediaErr: &PartialDeliveryError{Err: context.DeadlineExceeded}}
	svc := New(nil, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	att := &Attachment{FileName: "picture.jpg", MIME: "image/jpeg", Size: 1, Data: []byte{1}}

	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: phone}}, "caption", att)
	if len(results) != 1 || results[0].Status != "sent" || results[0].Channel != ChannelTelegram {
		t.Fatalf("partial Telegram media delivery must report file sent, got %+v", results)
	}
	if len(tg.mediaPhones) != 1 {
		t.Errorf("partial resolved media must not be retried, got %d sends", len(tg.mediaPhones))
	}
}

func TestPartialMediaAuthErrorStopsTelegramForRestOfBatch(t *testing.T) {
	phones := []string{"+380991234500", "+380991234501"}
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{phones[0]: true, phones[1]: true}}
	tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{
		name:  ChannelTelegram,
		found: map[string]bool{phones[0]: true, phones[1]: true},
	}, mediaErr: &PartialDeliveryError{Err: tgerr.New(401, "AUTH_KEY_UNREGISTERED")}}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	contacts := []Contact{{NormalizedPhone: phones[0]}, {NormalizedPhone: phones[1]}}
	att := &Attachment{FileName: "picture.jpg", MIME: "image/jpeg", Size: 1, Data: []byte{1}}

	results := svc.SendBatchWithAttachment(context.Background(), contacts, "caption", att)
	if len(results) != 2 {
		t.Fatalf("want two contact results, got %+v", results)
	}
	if len(tg.mediaPhones) != 1 {
		t.Errorf("Telegram auth failure must stop later media sends, got %d attempts", len(tg.mediaPhones))
	}
	if results[1].Channel != ChannelWhatsApp || !strings.Contains(results[1].Error, SessionLostError) {
		t.Errorf("later contact should continue on WA and report lost Telegram session, got %+v", results[1])
	}
}

func TestBroadcastWaitsForTelegramContactCleanup(t *testing.T) {
	const phone = "+380991234567"
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{phone: true}}
	tg := &batchResolverMock{
		name:          ChannelTelegram,
		found:         map[string]bool{phone: true},
		deleteStarted: make(chan struct{}),
		deleteRelease: make(chan struct{}),
	}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	finished := make(chan []SendResult, 1)
	go func() {
		finished <- svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: phone}}, "message")
	}()

	select {
	case <-tg.deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("batch did not start Telegram contact cleanup")
	}
	select {
	case <-finished:
		t.Fatal("broadcast returned before Telegram temporary contacts were deleted")
	case <-time.After(20 * time.Millisecond):
	}
	close(tg.deleteRelease)
	select {
	case results := <-finished:
		if len(results) != 1 || results[0].Status != "sent" {
			t.Fatalf("want successful broadcast after cleanup, got %+v", results)
		}
		if tg.sendBeforeDelete {
			t.Error("Telegram send started before temporary contact cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("broadcast did not finish after contact cleanup completed")
	}
}

func TestMediaCancellationWaitsForTelegramCleanup(t *testing.T) {
	const phone = "+380991234567"
	tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{
		name:          ChannelTelegram,
		found:         map[string]bool{phone: true},
		importStarted: make(chan struct{}),
		importRelease: make(chan struct{}),
		deleteStarted: make(chan struct{}),
		deleteRelease: make(chan struct{}),
	}}
	svc := New(nil, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan []SendResult, 1)
	go func() {
		finished <- svc.SendBatchWithAttachment(ctx, []Contact{{NormalizedPhone: phone}}, "caption", &Attachment{
			FileName: "picture.jpg", MIME: "image/jpeg", Size: 1, Data: []byte{1},
		})
	}()

	select {
	case <-tg.importStarted:
	case <-time.After(time.Second):
		t.Fatal("media batch did not start contact import")
	}
	cancel()
	close(tg.importRelease)
	select {
	case <-tg.deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("canceled media batch did not start cleanup")
	}
	select {
	case <-finished:
		t.Fatal("media batch returned before temporary contact cleanup completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(tg.deleteRelease)
	select {
	case results := <-finished:
		if len(results) != 0 {
			t.Errorf("canceled media batch must not report sends, got %+v", results)
		}
	case <-time.After(time.Second):
		t.Fatal("media batch did not finish after cleanup completed")
	}
}

func TestMediaContactsAreDeletedBeforeMediaSend(t *testing.T) {
	const phone = "+380991234567"
	tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{
		name:          ChannelTelegram,
		found:         map[string]bool{phone: true},
		deleteStarted: make(chan struct{}),
		deleteRelease: make(chan struct{}),
	}, mediaErr: errors.New("media upload failed")}
	svc := New(nil, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	finished := make(chan []SendResult, 1)
	go func() {
		finished <- svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: phone}}, "caption", &Attachment{
			FileName: "picture.jpg", MIME: "image/jpeg", Size: 1, Data: []byte{1},
		})
	}()

	select {
	case <-tg.deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("failed media batch did not start temporary contact cleanup")
	}
	select {
	case <-finished:
		t.Fatal("failed media batch returned before cleanup completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(tg.deleteRelease)
	select {
	case results := <-finished:
		if len(results) != 1 || results[0].Status != "failed" {
			t.Fatalf("want failed media result after cleanup, got %+v", results)
		}
	case <-time.After(time.Second):
		t.Fatal("failed media batch did not finish after cleanup")
	}
	if tg.sendBeforeDelete {
		t.Error("media send started before Telegram temporary contact cleanup")
	}
}

func TestBroadcastWaitsForCleanupAfterImportError(t *testing.T) {
	const phone = "+380991234567"
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{phone: true}}
	tg := &batchResolverMock{
		name:          ChannelTelegram,
		found:         map[string]bool{phone: true},
		importErr:     context.DeadlineExceeded,
		deleteStarted: make(chan struct{}),
		deleteRelease: make(chan struct{}),
	}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	finished := make(chan []SendResult, 1)
	go func() {
		finished <- svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: phone}}, "message")
	}()

	select {
	case <-tg.deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("batch did not clean up contacts after import error")
	}
	select {
	case <-finished:
		t.Fatal("broadcast returned before cleanup after import error")
	case <-time.After(20 * time.Millisecond):
	}
	close(tg.deleteRelease)
	select {
	case results := <-finished:
		if len(results) != 1 || results[0].Status != "sent" || !strings.Contains(results[0].Error, "batch import failed") {
			t.Fatalf("want WA success and Telegram import failure note, got %+v", results)
		}
	case <-time.After(time.Second):
		t.Fatal("broadcast did not finish after cleanup")
	}
}

func TestBroadcastCleansTemporaryContactsWhenCanceled(t *testing.T) {
	const phone = "+380991234567"
	tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{
		name:      ChannelTelegram,
		found:     map[string]bool{phone: true},
		deleteErr: context.DeadlineExceeded,
	}}
	svc := New(nil, tg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := svc.SendBatchWithAttachment(ctx, []Contact{{NormalizedPhone: phone}}, "caption", &Attachment{
		FileName: "picture.jpg", MIME: "image/jpeg", Size: 1, Data: []byte{1},
	})
	if len(results) != 1 || results[0].Status != "failed" || !strings.Contains(results[0].Error, "temporary contacts cleanup failed") {
		t.Errorf("canceled batch must report cleanup failure, got %+v", results)
	}
	if tg.importCalls != 1 || tg.deleteCalls != 1 || len(tg.deleteContacts) != 1 {
		t.Errorf("canceled batch must clean newly imported contact: imports=%d delete calls=%d contacts=%d", tg.importCalls, tg.deleteCalls, len(tg.deleteContacts))
	}
}

func TestBroadcastReportsTemporaryContactCleanupFailure(t *testing.T) {
	const phone = "+380991234567"
	wa := &mockMessenger{name: ChannelWhatsApp, available: map[string]bool{phone: true}}
	tg := &batchResolverMock{
		name:      ChannelTelegram,
		found:     map[string]bool{phone: true},
		deleteErr: context.DeadlineExceeded,
	}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0

	results := svc.SendBatch(context.Background(), []Contact{{NormalizedPhone: phone}}, "message")
	if len(results) != 1 || results[0].Status != "sent" {
		t.Fatalf("delivery must remain successful when cleanup fails, got %+v", results)
	}
	if !strings.Contains(results[0].Error, "temporary contacts cleanup failed") {
		t.Errorf("cleanup failure must be reported, got %q", results[0].Error)
	}
	if results[0].Channel != ChannelWhatsApp || len(tg.sent) != 0 {
		t.Errorf("cleanup failure must skip Telegram but preserve WhatsApp delivery, channel=%s Telegram sends=%v", results[0].Channel, tg.sent)
	}
}

func TestMediaCleanupFailureSkipsTelegramAndContinuesWhatsApp(t *testing.T) {
	const phone = "+380991234567"
	wa := &mediaMock{name: ChannelWhatsApp, available: map[string]bool{phone: true}}
	tg := &resolvedMediaMock{batchResolverMock: &batchResolverMock{
		name:      ChannelTelegram,
		found:     map[string]bool{phone: true},
		deleteErr: context.DeadlineExceeded,
	}}
	svc := New(wa, tg, nil)
	svc.minDelay, svc.maxDelay = 0, 0
	svc.tgMinDelay, svc.tgMaxDelay = 0, 0
	att := &Attachment{FileName: "picture.jpg", MIME: "image/jpeg", Size: 1, Data: []byte{1}}

	results := svc.SendBatchWithAttachment(context.Background(), []Contact{{NormalizedPhone: phone}}, "caption", att)
	if len(results) != 1 || results[0].Status != "sent" || results[0].Channel != ChannelWhatsApp {
		t.Fatalf("cleanup failure must preserve WA media delivery and skip TG, got %+v", results)
	}
	if len(wa.mediaCalls) != 1 || len(tg.mediaPhones) != 0 {
		t.Errorf("want WA media once and no TG send, wa=%v tg=%v", wa.mediaCalls, tg.mediaPhones)
	}
	if !strings.Contains(results[0].Error, "temporary contacts cleanup failed") {
		t.Errorf("cleanup error not reported: %q", results[0].Error)
	}
}
