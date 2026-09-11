package ui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"visnyk/internal/cascade"
)

func tempApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("VISNYK_DATA_DIR", t.TempDir())
	return &App{}
}

func TestSaveAttachmentRoundTrip(t *testing.T) {
	a := tempApp(t)
	raw := base64.StdEncoding.EncodeToString([]byte("hello visnyk"))
	att, errStr := a.SaveAttachment("notes.txt", raw)
	if errStr != "" {
		t.Fatalf("want save ok, got %q", errStr)
	}
	if att.FileName != "notes.txt" {
		t.Errorf("want original name kept, got %q", att.FileName)
	}
	if att.Size != int64(len("hello visnyk")) {
		t.Errorf("want size %d, got %d", len("hello visnyk"), att.Size)
	}
	if att.Kind != cascade.AttachmentDocument {
		t.Errorf("txt must be document, got %q", att.Kind)
	}
	if _, err := os.Stat(att.Path); err != nil {
		t.Fatalf("stored file must exist: %v", err)
	}

	loaded, errStr := a.loadAttachment(&att)
	if errStr != "" {
		t.Fatalf("want load ok, got %q", errStr)
	}
	if string(loaded.Data) != "hello visnyk" {
		t.Errorf("bytes must survive disk round-trip, got %q", loaded.Data)
	}
}

func TestSaveAttachmentDataURLPrefix(t *testing.T) {
	a := tempApp(t)
	raw := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("x"))
	if _, errStr := a.SaveAttachment("x.txt", raw); errStr != "" {
		t.Fatalf("data URL prefix must be stripped, got %q", errStr)
	}
}

func TestSaveAttachmentBlocked(t *testing.T) {
	a := tempApp(t)
	raw := base64.StdEncoding.EncodeToString([]byte("MZ"))
	for _, name := range []string{"run.exe", "RUN.EXE", "x.html", "x.js", "x.ps1", "x.bat"} {
		if _, errStr := a.SaveAttachment(name, raw); errStr == "" {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestSaveAttachmentBadBase64(t *testing.T) {
	a := tempApp(t)
	if _, errStr := a.SaveAttachment("a.txt", "!!!not-base64!!!"); errStr == "" {
		t.Fatal("broken base64 must be rejected")
	}
}

func TestSaveAttachmentLongNameCapped(t *testing.T) {
	a := tempApp(t)
	raw := base64.StdEncoding.EncodeToString([]byte("x"))
	long := strings.Repeat("a", 150) + ".txt"
	att, errStr := a.SaveAttachment(long, raw)
	if errStr != "" {
		t.Fatalf("long name must be capped, not rejected: %q", errStr)
	}
	if len([]rune(att.FileName)) > 120 {
		t.Errorf("stored name must be capped at 120 runes, got %d", len([]rune(att.FileName)))
	}
}

func TestLoadAttachmentRejectsForeignPath(t *testing.T) {
	a := tempApp(t)
	foreign := filepath.Join(t.TempDir(), "evil.txt")
	if err := os.WriteFile(foreign, []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	att := &cascade.Attachment{FileName: "evil.txt", MIME: "text/plain", Size: 4, Path: foreign}
	if _, errStr := a.loadAttachment(att); errStr == "" {
		t.Fatal("path outside attachments dir must be rejected")
	} else if !strings.Contains(errStr, "not an attachment path") {
		t.Fatalf("want path error, got %q", errStr)
	}
}

func TestSaveAttachmentEmpty(t *testing.T) {
	a := tempApp(t)
	if _, errStr := a.SaveAttachment("a.txt", ""); errStr == "" {
		t.Fatal("empty input must be rejected")
	}
}

func TestClearAttachment(t *testing.T) {
	a := tempApp(t)
	raw := base64.StdEncoding.EncodeToString([]byte("bye"))
	att, errStr := a.SaveAttachment("bye.txt", raw)
	if errStr != "" {
		t.Fatalf("save: %q", errStr)
	}
	if errStr := a.ClearAttachment(att.Path); errStr != "" {
		t.Fatalf("clear: %q", errStr)
	}
	if _, err := os.Stat(att.Path); !os.IsNotExist(err) {
		t.Fatal("file must be gone after clear")
	}
	// Path outside the attachments dir must be refused.
	if errStr := a.ClearAttachment(filepath.Join(t.TempDir(), "evil.txt")); errStr == "" {
		t.Fatal("foreign path must be refused")
	}
	// Empty path is a no-op (remove button with nothing staged).
	if errStr := a.ClearAttachment(""); errStr != "" {
		t.Fatalf("empty path must be ok, got %q", errStr)
	}
}

func TestLoadAttachmentNil(t *testing.T) {
	a := tempApp(t)
	if out, errStr := a.loadAttachment(nil); out != nil || errStr != "" {
		t.Fatalf("nil means text-only, got %v %q", out, errStr)
	}
	if out, errStr := a.loadAttachment(&cascade.Attachment{}); out != nil || errStr != "" {
		t.Fatalf("empty path means text-only, got %v %q", out, errStr)
	}
}

func TestWithFilePrefix(t *testing.T) {
	if got := withFilePrefix("hi", nil); got != "hi" {
		t.Errorf("nil keeps template, got %q", got)
	}
	att := &cascade.Attachment{FileName: "r.pdf"}
	if got := withFilePrefix("hi", att); !strings.Contains(got, "r.pdf") || !strings.Contains(got, "hi") {
		t.Errorf("prefix must carry file + text, got %q", got)
	}
}

func TestSanitizeAttachmentName(t *testing.T) {
	if got := sanitizeAttachmentName("../../etc/passwd"); got != "passwd" {
		t.Errorf("directories must be stripped, got %q", got)
	}
	if got := sanitizeAttachmentName(""); got == "" {
		t.Error("empty name must get a fallback")
	}
}
