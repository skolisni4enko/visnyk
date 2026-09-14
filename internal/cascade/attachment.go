package cascade

import (
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// AttachmentKind groups files by how messengers deliver them.
type AttachmentKind string

const (
	AttachmentImage    AttachmentKind = "image"
	AttachmentVideo    AttachmentKind = "video"
	AttachmentAudio    AttachmentKind = "audio"
	AttachmentDocument AttachmentKind = "document"
)

// Size limits: WhatsApp allows ~16MB for image/video/audio and ~100MB
// for documents; Telegram user API allows much more, but we cap at the
// same values to keep pacing sane and avoid flood bans.
const (
	MaxMediaBytes = 16 * 1024 * 1024
	MaxDocBytes   = 100 * 1024 * 1024
)

// Attachment is one file attached to a broadcast. The same file is sent
// to every contact with the message template as caption.
// Path is server-side only (attachments dir); Data is loaded per batch
// and excluded from Wails JSON so the frontend never ships bytes twice.
type Attachment struct {
	FileName string         `json:"fileName"`
	MIME     string         `json:"mime"`
	Size     int64          `json:"size"`
	Path     string         `json:"path,omitempty"`
	Kind     AttachmentKind `json:"kind"`
	Data     []byte         `json:"-"`
}

// blockedExt are never sent (executables / scripts / HTML app).
var blockedExt = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".sh": true, ".ps1": true,
	".js": true, ".jar": true, ".msi": true, ".com": true, ".scr": true,
	".html": true, ".htm": true,
}

// ClassifyAttachment maps file name + MIME to a delivery kind.
func ClassifyAttachment(fileName, mimeType string) AttachmentKind {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mt, "image/"):
		return AttachmentImage
	case strings.HasPrefix(mt, "video/"):
		return AttachmentVideo
	case strings.HasPrefix(mt, "audio/"):
		return AttachmentAudio
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp":
		return AttachmentImage
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		return AttachmentVideo
	case ".mp3", ".ogg", ".oga", ".opus", ".m4a", ".wav", ".flac":
		return AttachmentAudio
	default:
		return AttachmentDocument
	}
}

// DetectMIME sniffs content type from bytes, falling back to extension.
func DetectMIME(fileName string, data []byte) string {
	if len(data) > 0 {
		if mt := strings.SplitN(http.DetectContentType(data), ";", 2)[0]; strings.TrimSpace(mt) != "" {
			return strings.TrimSpace(mt)
		}
	}
	if ext := strings.ToLower(filepath.Ext(fileName)); ext != "" {
		if mt := mime.TypeByExtension(ext); mt != "" {
			return strings.SplitN(mt, ";", 2)[0]
		}
	}
	return "application/octet-stream"
}

// ValidateAttachment checks name, type and size before anything is stored.
func ValidateAttachment(fileName, mimeType string, size int64) error {
	return validateWithKind(fileName, mimeType, ClassifyAttachment(fileName, mimeType), size)
}

// validateWithKind enforces name rules plus the media/document size limit
// for an explicit kind.
func validateWithKind(fileName, mimeType string, kind AttachmentKind, size int64) error {
	name := strings.TrimSpace(fileName)
	if name == "" {
		return fmt.Errorf("empty file name")
	}
	// Reject anything with a directory component (traversal attempts).
	// The UI sanitizes names before storing, this is defense in depth.
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid file name %q", fileName)
	}
	ext := strings.ToLower(filepath.Ext(name))
	if blockedExt[ext] {
		return fmt.Errorf("file type %q is not allowed (executables/scripts blocked)", ext)
	}
	if ext == ".heic" || ext == ".heif" {
		return fmt.Errorf("heic photos are not supported — save as jpg/png first")
	}
	if size <= 0 {
		return fmt.Errorf("empty file")
	}
	limit := int64(MaxDocBytes)
	if kind != AttachmentDocument {
		limit = int64(MaxMediaBytes)
	}
	if size > limit {
		return fmt.Errorf("%s files are limited to %d MB (got %.1f MB)", kind, limit/(1024*1024), float64(size)/(1024*1024))
	}
	return nil
}

// Validate checks the attachment itself. It is pure (no mutation) so the
// same *Attachment can be shared by parallel channel workers — normalize
// Kind/MIME once via Normalize before fan-out.
func (a *Attachment) Validate() error {
	if a == nil {
		return fmt.Errorf("nil attachment")
	}
	kind := a.Kind
	if kind == "" {
		kind = ClassifyAttachment(a.FileName, a.MIME)
	}
	size := a.Size
	if size == 0 && len(a.Data) > 0 {
		size = int64(len(a.Data))
	}
	return validateWithKind(a.FileName, a.MIME, kind, size)
}

// Normalize fills in Kind/MIME/Size defaults. Call once per batch before
// parallel sends so workers never mutate the shared struct.
func (a *Attachment) Normalize() {
	if a == nil {
		return
	}
	if a.Kind == "" {
		a.Kind = ClassifyAttachment(a.FileName, a.MIME)
	}
	if a.MIME == "" {
		a.MIME = DetectMIME(a.FileName, a.Data)
	}
	if a.Size == 0 && len(a.Data) > 0 {
		a.Size = int64(len(a.Data))
	}
}

// TruncateCaption cuts captions to maxRunes without breaking UTF-8.
func TruncateCaption(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// SplitText chunks s into pieces of at most max runes, snapping cuts to
// paragraph/newline/space boundaries when one is nearby. It never returns
// empty pieces and never breaks UTF-8. Unbroken runs hard-cut at max.
func SplitText(s string, max int) []string {
	if max <= 0 {
		max = 1
	}
	var out []string
	rest := strings.TrimSpace(s)
	for rest != "" {
		runes := []rune(rest)
		if len(runes) <= max {
			out = append(out, rest)
			break
		}
		cut := cutAtBoundary(runes, max)
		out = append(out, string(runes[:cut]))
		rest = strings.TrimSpace(string(runes[cut:]))
	}
	return out
}

// SplitLongText splits s into a caption head (at most captionMax runes)
// and follow-up tails (each at most textMax runes). If s fits the caption,
// tails is nil — the caller sends a single captioned message.
func SplitLongText(s string, captionMax, textMax int) (string, []string) {
	if len([]rune(s)) <= captionMax {
		return s, nil
	}
	chunks := SplitText(s, captionMax)
	head := chunks[0]
	var tails []string
	for _, c := range chunks[1:] {
		tails = append(tails, SplitText(c, textMax)...)
	}
	return head, tails
}

// cutAtBoundary returns the cut index (in runes) at or below max,
// preferring blank lines, then newlines, then spaces. It accepts a
// boundary only past the first quarter so chunks stay sizable;
// otherwise it hard-cuts at max (progress is always guaranteed).
func cutAtBoundary(runes []rune, max int) int {
	if len(runes) <= max {
		return len(runes)
	}
	window := string(runes[:max])
	for _, sep := range []string{"\n\n", "\n", " "} {
		if idx := strings.LastIndex(window, sep); idx > 0 {
			runeIdx := len([]rune(window[:idx]))
			if runeIdx > max/4 {
				return runeIdx
			}
		}
	}
	return max
}
