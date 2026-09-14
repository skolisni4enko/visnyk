package telegram

import (
	"context"
	"fmt"
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
func (s *Service) SendMedia(phone, caption string, att *cascade.Attachment) error {
	s.mu.Lock()
	client := s.client
	connected := s.connected && s.loggedIn
	s.mu.Unlock()
	if !connected || client == nil {
		return fmt.Errorf("telegram not connected/logged in")
	}
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

	upCtx, cancelUp := context.WithTimeout(context.Background(), 3*time.Minute)
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
	sendCtx, cancelSend := context.WithTimeout(context.Background(), 60*time.Second)
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
		// Chunks are cut at text boundaries, but a long formatted run can
		// still straddle the seam — drop a trailing broken tag, and a
		// stray opener renders literally rather than failing the send.
		if err := s.sendStyledToPeer(sendCtx, client, peer, trimBrokenTag(tail)); err != nil {
			return fmt.Errorf("file %q sent but follow-up text failed: %w", att.FileName, err)
		}
	}

	// Cleanup: delete the temporary contact if we just created it
	// (fallback path only), same as ResolveAndSend.
	if newlyImported {
		go func() {
			cctx, ccancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer ccancel()
			s.mu.Lock()
			cl := s.client
			ok := s.connected && s.loggedIn
			s.mu.Unlock()
			if !ok || cl == nil {
				return
			}
			if _, derr := cl.API().ContactsDeleteContacts(cctx, []tg.InputUserClass{&tg.InputUser{UserID: uid, AccessHash: accessHash}}); derr != nil {
				fmt.Printf("[telegram] send-media cleanup delete %s -> %v\n", phone, derr)
			} else {
				fmt.Printf("[telegram] send-media cleanup delete %s ok\n", phone)
			}
		}()
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
