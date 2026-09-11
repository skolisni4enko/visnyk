package whatsapp

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"

	"visnyk/internal/cascade"
)

// waCaptionMax is the WhatsApp caption limit for media messages.
// waTextMax is the conservative chunk size for follow-up texts: the personal
// client limit is far higher but undocumented, so long texts are split small.
const waCaptionMax = 1024
const waTextMax = 16000

func strPtr(s string) *string { return &s }

func uint64Ptr(v uint64) *uint64 { return &v }

func boolPtr(v bool) *bool { return &v }

// SendMedia delivers one file with caption to phone (E.164).
// It implements cascade.MediaSender so broadcast batches can carry files.
// Caption is converted from HTML to WhatsApp markdown like text messages.
func (s *Service) SendMedia(phone, caption string, att *cascade.Attachment) error {
	if s.client == nil || !s.client.IsConnected() {
		return fmt.Errorf("whatsapp not connected")
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
	jid, err := parseJID(phone)
	if err != nil {
		return err
	}
	// Long texts don't fit the 1024 caption: the file goes bare and the
	// full text follows as chunked text message(s) — no silent truncation.
	// (Markdown cuts are cosmetic only: a split "*bold*" renders literally,
	// it never fails the send, unlike Telegram HTML.)
	md := toWhatsAppMessage(caption)
	captionText := ""
	if md == "" || len([]rune(md)) <= waCaptionMax {
		captionText = md
	}
	var tails []string
	if md != "" && captionText == "" {
		tails = cascade.SplitText(md, waTextMax)
	}

	mimeType := att.MIME
	if mimeType == "" {
		mimeType = cascade.DetectMIME(att.FileName, data)
	}
	kind := att.Kind
	if kind == "" {
		kind = cascade.ClassifyAttachment(att.FileName, mimeType)
	}

	var mediaType whatsmeow.MediaType
	msg := &waProto.Message{}
	switch kind {
	case cascade.AttachmentImage:
		mediaType = whatsmeow.MediaImage
	case cascade.AttachmentVideo:
		mediaType = whatsmeow.MediaVideo
	case cascade.AttachmentAudio:
		mediaType = whatsmeow.MediaAudio
	default:
		mediaType = whatsmeow.MediaDocument
	}

	uploadCtx, cancelUpload := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelUpload()
	resp, err := s.client.Upload(uploadCtx, data, mediaType)
	if err != nil {
		return fmt.Errorf("upload %q: %w", att.FileName, err)
	}

	switch kind {
	case cascade.AttachmentImage:
		msg.ImageMessage = &waProto.ImageMessage{
			Caption:       strPtr(captionText),
			Mimetype:      strPtr(mimeType),
			URL:           strPtr(resp.URL),
			DirectPath:    strPtr(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    uint64Ptr(resp.FileLength),
		}
	case cascade.AttachmentVideo:
		msg.VideoMessage = &waProto.VideoMessage{
			Caption:       strPtr(captionText),
			Mimetype:      strPtr(mimeType),
			URL:           strPtr(resp.URL),
			DirectPath:    strPtr(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    uint64Ptr(resp.FileLength),
		}
	case cascade.AttachmentAudio:
		msg.AudioMessage = &waProto.AudioMessage{
			Mimetype:      strPtr(mimeType),
			URL:           strPtr(resp.URL),
			DirectPath:    strPtr(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    uint64Ptr(resp.FileLength),
			PTT:           boolPtr(false),
		}
	default:
		msg.DocumentMessage = &waProto.DocumentMessage{
			Title:         strPtr(att.FileName),
			FileName:      strPtr(att.FileName),
			Caption:       strPtr(captionText),
			Mimetype:      strPtr(mimeType),
			URL:           strPtr(resp.URL),
			DirectPath:    strPtr(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    uint64Ptr(resp.FileLength),
		}
	}

	sendCtx, cancelSend := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSend()
	if _, err := s.client.SendMessage(sendCtx, jid, msg); err != nil {
		return fmt.Errorf("send media %q: %w", att.FileName, err)
	}
	if kind == cascade.AttachmentAudio && tails == nil && md != "" {
		// AudioMessage has no caption field: deliver the text separately.
		tails = cascade.SplitText(md, waTextMax)
	}
	for _, tail := range tails {
		if err := s.sendConvertedText(phone, tail); err != nil {
			return fmt.Errorf("sent %q but follow-up text failed: %w", att.FileName, err)
		}
	}
	return nil
}
