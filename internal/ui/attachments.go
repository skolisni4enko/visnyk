package ui

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"visnyk/internal/cascade"
	"visnyk/internal/paths"
)

// attachmentInDir reports whether path is a file inside the attachments dir.
func attachmentInDir(path string) bool {
	clean := filepath.Clean(path)
	dir := filepath.Clean(paths.AttachmentsDir())
	if clean == dir {
		return false
	}
	return strings.HasPrefix(clean, dir+string(os.PathSeparator))
}

// sanitizeAttachmentName strips directories and caps length so stored
// file names are safe on every OS.
func sanitizeAttachmentName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.ReplaceAll(base, "\x00", "")
	if len([]rune(base)) > 120 {
		ext := filepath.Ext(base)
		runes := []rune(base)
		cut := 120 - len([]rune(ext))
		if cut < 1 {
			cut = 1
		}
		base = string(runes[:cut]) + ext
	}
	if base == "" || base == "." || base == "/" {
		base = fmt.Sprintf("file-%d", time.Now().Unix())
	}
	return base
}

// SaveAttachment stores one broadcast file from the frontend (base64,
// optionally a data URL) into the attachments dir and returns its
// descriptor. The file bytes stay on disk; cascade loads them per batch.
func (a *App) SaveAttachment(filename, base64Data string) (cascade.Attachment, string) {
	if strings.TrimSpace(base64Data) == "" {
		return cascade.Attachment{}, "empty file"
	}
	if idx := strings.Index(base64Data, ","); idx != -1 && strings.Contains(base64Data[:idx], "base64") {
		base64Data = base64Data[idx+1:]
	}
	dataStr := strings.TrimSpace(base64Data)
	// Pre-check: base64 inflates ~4/3, so reject obvious giants before
	// decoding the whole blob into memory.
	if int64(len(dataStr))*3/4 > cascade.MaxDocBytes+1024*1024 {
		return cascade.Attachment{}, fmt.Sprintf("file too large: limit %d MB", cascade.MaxDocBytes/(1024*1024))
	}
	data, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		return cascade.Attachment{}, fmt.Sprintf("base64 decode: %v", err)
	}
	name := sanitizeAttachmentName(filename)
	mimeType := cascade.DetectMIME(name, data)
	if err := cascade.ValidateAttachment(name, mimeType, int64(len(data))); err != nil {
		return cascade.Attachment{}, err.Error()
	}
	dir := paths.AttachmentsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return cascade.Attachment{}, fmt.Sprintf("attachments dir: %v", err)
	}
	stored := fmt.Sprintf("%s-%s", generateBatchID(), name)
	path := filepath.Join(dir, stored)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return cascade.Attachment{}, fmt.Sprintf("save file: %v", err)
	}
	att := cascade.Attachment{
		FileName: name,
		MIME:     mimeType,
		Size:     int64(len(data)),
		Path:     path,
		Kind:     cascade.ClassifyAttachment(name, mimeType),
	}
	a.logFile("INFO", "attach", fmt.Sprintf("saved %q mime=%s size=%d path=%s", name, mimeType, len(data), path))
	return att, ""
}

// ClearAttachment deletes a staged file (user pressed remove in UI).
func (a *App) ClearAttachment(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !attachmentInDir(path) {
		return "not an attachment path"
	}
	clean := filepath.Clean(path)
	if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("remove: %v", err)
	}
	a.logFile("INFO", "attach", fmt.Sprintf("cleared %s", clean))
	return ""
}

// loadAttachment resolves a frontend descriptor into a send-ready
// attachment (bytes loaded, validated). Nil/empty input means text only.
// The path must live inside the attachments dir — the frontend controls
// this value, so anything outside is rejected (no arbitrary file reads).
func (a *App) loadAttachment(att *cascade.Attachment) (*cascade.Attachment, string) {
	if att == nil || strings.TrimSpace(att.Path) == "" {
		return nil, ""
	}
	if !attachmentInDir(att.Path) {
		return nil, "not an attachment path"
	}
	data, err := os.ReadFile(att.Path)
	if err != nil {
		return nil, fmt.Sprintf("read attachment: %v", err)
	}
	if len(data) == 0 {
		return nil, fmt.Sprintf("empty attachment %q", att.FileName)
	}
	out := &cascade.Attachment{
		FileName: att.FileName,
		MIME:     att.MIME,
		Size:     int64(len(data)),
		Path:     att.Path,
		Kind:     att.Kind,
		Data:     data,
	}
	if out.MIME == "" {
		out.MIME = cascade.DetectMIME(out.FileName, data)
	}
	if out.Kind == "" {
		out.Kind = cascade.ClassifyAttachment(out.FileName, out.MIME)
	}
	if err := out.Validate(); err != nil {
		return nil, err.Error()
	}
	return out, ""
}

// withFilePrefix tags history previews so files are visible in history.
func withFilePrefix(template string, att *cascade.Attachment) string {
	if att == nil || strings.TrimSpace(att.FileName) == "" {
		return template
	}
	return fmt.Sprintf("[файл: %s] %s", att.FileName, template)
}
