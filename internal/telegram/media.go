package telegram

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	tgmessage "github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"visnyk/internal/cascade"
	"visnyk/internal/common"
	"visnyk/internal/format"
)

// tgMediaCaptionMax is the Telegram caption limit for media messages.
// tgTextMax is the server limit for a follow-up text message (4096 code
// points); longer texts are chunked so nothing is ever silently cut.
const tgMediaCaptionMax = 1024
const tgTextMax = 4096

// SendMedia delivers one file with caption to phone (E.164).
// It implements cascade.MediaSender: resolve-first (no address-book
// pollution), single ImportContacts fallback only for privacy-hidden
// numbers, upload via gotd uploader, photo for images and document
// (with filename + MIME) for everything else.
func (s *Service) SendMedia(phone, caption string, att *cascade.Attachment) (sendErr error) {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return fmt.Errorf("telegram not connected/logged in")
	}
	phone = normalizePhone(phone)
	if phone == "" {
		return fmt.Errorf("empty phone")
	}

	rctx, cancelResolve := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelResolve()
	peer, uid, accessHash, newlyImported, err := s.resolveMediaPeer(rctx, phone)
	if err != nil {
		return err
	}
	// If privacy-hidden lookup imported a temporary contact, wait for cleanup
	// before returning, even when upload or delivery fails.
	if newlyImported {
		defer func() {
			cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer ccancel()
			if _, derr := client.API().ContactsDeleteContacts(cctx, []tg.InputUserClass{&tg.InputUser{UserID: uid, AccessHash: accessHash}}); derr != nil {
				sendErr = errors.Join(sendErr, fmt.Errorf("delete temporary Telegram contact %s: %w", phone, derr))
			}
		}()
	}
	return s.sendMediaToPeer(context.Background(), client, peer, caption, att)
}

// SendResolvedMedia sends an attachment to a user resolved by BatchImport.
// It never calls resolvePhone or imports a contact; the caller owns batch
// cleanup and deletes newly imported contacts before returning.
func (s *Service) SendResolvedMedia(ctx context.Context, phone, caption string, att *cascade.Attachment) error {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return fmt.Errorf("telegram not connected/logged in")
	}
	key := batchKey(phone)
	if key == "" {
		return fmt.Errorf("empty phone")
	}
	s.batchMu.Lock()
	usr := s.batchUsers[key]
	s.batchMu.Unlock()
	if usr == nil {
		return fmt.Errorf("user not found for %s", key)
	}
	peer := &tg.InputPeerUser{UserID: usr.ID, AccessHash: usr.AccessHash}
	return s.sendMediaToPeer(ctx, client, peer, caption, att)
}

func (s *Service) sendMediaToPeer(ctx context.Context, client *telegram.Client, peer *tg.InputPeerUser, caption string, att *cascade.Attachment) error {
	if att == nil {
		return fmt.Errorf("nil attachment")
	}
	if err := att.Validate(); err != nil {
		return fmt.Errorf("attachment: %w", err)
	}
	data := att.Data
	if len(data) == 0 && att.Path != "" {
		b, err := os.ReadFile(att.Path)
		if err != nil {
			return fmt.Errorf("read attachment: %w", err)
		}
		data = b
	}
	if len(data) == 0 {
		return fmt.Errorf("empty attachment %q", att.FileName)
	}

	upCtx, cancelUp := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelUp()
	inputFile, err := uploader.NewUploader(client.API()).FromBytes(upCtx, att.FileName, data)
	if err != nil {
		return fmt.Errorf("upload %q: %w", att.FileName, err)
	}

	// Long texts don't fit the 1024 caption: the file goes bare and the
	// full text follows as chunked text message(s) — no silent truncation.
	htmlStr := ""
	if caption != "" {
		htmlStr = format.HTMLToTelegram(caption)
	}
	var captionOpts []tgmessage.StyledTextOption
	var tails []string
	if htmlStr != "" {
		if len([]rune(htmlStr)) <= tgMediaCaptionMax {
			captionOpts = append(captionOpts, html.String(nil, htmlStr))
		} else {
			tails = cascade.SplitText(htmlStr, tgTextMax)
		}
	}

	kind := att.Kind
	if kind == "" {
		kind = cascade.ClassifyAttachment(att.FileName, att.MIME)
	}
	sender := tgmessage.NewSender(client.API())
	sendCtx, cancelSend := context.WithTimeout(ctx, 60*time.Second)
	defer cancelSend()
	if kind == cascade.AttachmentImage {
		if _, err := sender.To(peer).Media(sendCtx, tgmessage.UploadedPhoto(inputFile, captionOpts...)); err != nil {
			return fmt.Errorf("send photo %q: %w", att.FileName, err)
		}
	} else {
		doc := tgmessage.UploadedDocument(inputFile, captionOpts...).Filename(att.FileName)
		if att.MIME != "" {
			doc = doc.MIME(att.MIME)
		}
		if _, err := sender.To(peer).Media(sendCtx, doc); err != nil {
			return fmt.Errorf("send document %q: %w", att.FileName, err)
		}
	}
	for _, tail := range tails {
		if err := s.sendStyledToPeerWithFloodRetry(ctx, client, peer, trimBrokenTag(tail)); err != nil {
			return &cascade.PartialDeliveryError{Err: fmt.Errorf("file %q sent but follow-up text failed: %w", att.FileName, err)}
		}
	}
	return nil
}

func (s *Service) sendStyledToPeerWithFloodRetry(ctx context.Context, client *telegram.Client, peer *tg.InputPeerUser, htmlStr string) error {
	send := func(parent context.Context) error {
		reqCtx, cancel := context.WithTimeout(parent, 60*time.Second)
		defer cancel()
		return s.sendStyledToPeer(reqCtx, client, peer, htmlStr)
	}
	err := send(ctx)
	if err == nil {
		return nil
	}
	return retryFloodWait(ctx, err, waitTelegramFlood, send)
}

func retryFloodWait(ctx context.Context, err error, wait func(context.Context, time.Duration) error, retry func(context.Context) error) error {
	d, isFlood := common.FloodWaitDuration(err)
	if !isFlood {
		return err
	}
	if err := wait(ctx, d); err != nil {
		return err
	}
	return retry(ctx)
}

// retryImportWithFloodWait retries a failed contact import once. If Telegram
// returns another FLOOD_WAIT, it honors that second server wait and returns
// the error so callers stop before issuing another contacts.* request.
func retryImportWithFloodWait(ctx context.Context, initial error, wait func(context.Context, time.Duration) error, retry func(context.Context) error) error {
	firstWait, isFlood := common.FloodWaitDuration(initial)
	if !isFlood {
		return initial
	}
	if err := wait(ctx, firstWait); err != nil {
		return errors.Join(initial, err)
	}
	err := retry(ctx)
	if err == nil {
		return nil
	}
	secondWait, repeatedFlood := common.FloodWaitDuration(err)
	if repeatedFlood {
		if waitErr := wait(ctx, secondWait); waitErr != nil {
			return errors.Join(err, waitErr)
		}
	}
	return err
}

func waitTelegramFlood(ctx context.Context, d time.Duration) error {
	wait := d + 2*time.Second + time.Duration(rand.Int63n(int64(3*time.Second)))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
	}
	return nil
}

// sendStyledToPeer delivers an already-rendered Telegram HTML fragment to
// a resolved peer. Tails from SplitText go through here directly so markup
// is parsed once, not double-converted.
func (s *Service) sendStyledToPeer(ctx context.Context, client *telegram.Client, inputPeer *tg.InputPeerUser, htmlStr string) error {
	sender := tgmessage.NewSender(client.API())
	_, err := sender.To(inputPeer).StyledText(ctx, html.String(nil, htmlStr))
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// trimBrokenTag drops a trailing incomplete "<..." fragment left by caption
// truncation so Telegram's HTML parser never chokes on broken markup.
func trimBrokenTag(s string) string {
	if strings.Count(s, "<") > strings.Count(s, ">") {
		if idx := strings.LastIndex(s, "<"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}

// resolveMediaPeer maps phone to an input peer, resolve-first with a
// single import fallback for privacy-hidden numbers. It reports whether
// the user was newly imported (caller decides about cleanup delete).
func (s *Service) resolveMediaPeer(ctx context.Context, phone string) (*tg.InputPeerUser, int64, int64, bool, error) {
	var res *tg.ContactsImportedContacts
	user, err := s.resolveUser(ctx, phone)
	if err != nil {
		if !common.IsTelegramNotFound(err) && !isNotFoundText(err) {
			return nil, 0, 0, false, err
		}
		var ierr error
		user, res, ierr = s.importAndFind(ctx, phone)
		if ierr != nil {
			return nil, 0, 0, false, fmt.Errorf("import: %w", ierr)
		}
	}
	if user == nil {
		return nil, 0, 0, false, fmt.Errorf("user not found for %s", phone)
	}
	newlyImported := false
	if res != nil {
		for _, ic := range res.Imported {
			if ic.UserID == user.ID {
				newlyImported = true
				break
			}
		}
	}
	return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, user.ID, user.AccessHash, newlyImported, nil
}
