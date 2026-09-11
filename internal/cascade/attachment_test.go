package cascade

import (
	"strings"
	"testing"
)

func TestClassifyAttachment(t *testing.T) {
	cases := []struct {
		name string
		mime string
		want AttachmentKind
	}{
		{"photo.jpg", "image/jpeg", AttachmentImage},
		{"pic.PNG", "image/png", AttachmentImage},
		{"clip.mp4", "video/mp4", AttachmentVideo},
		{"movie.bin", "video/mp4", AttachmentVideo},
		{"voice.ogg", "audio/ogg", AttachmentAudio},
		{"song.mp3", "audio/mpeg", AttachmentAudio},
		{"report.pdf", "application/pdf", AttachmentDocument},
		{"table.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", AttachmentDocument},
		{"notes.txt", "text/plain", AttachmentDocument},
		{"archive.zip", "application/zip", AttachmentDocument},
		{"mystery.xyz", "application/octet-stream", AttachmentDocument},
		{"noext", "", AttachmentDocument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyAttachment(tc.name, tc.mime); got != tc.want {
				t.Errorf("ClassifyAttachment(%q, %q) = %q, want %q", tc.name, tc.mime, got, tc.want)
			}
		})
	}
}

func TestValidateAttachment(t *testing.T) {
	cases := []struct {
		name     string
		fileName string
		mime     string
		size     int64
		wantErr  string
	}{
		{"ok photo", "photo.jpg", "image/jpeg", 2 * 1024 * 1024, ""},
		{"ok doc", "report.pdf", "application/pdf", 50 * 1024 * 1024, ""},
		{"ok small txt", "n.txt", "text/plain", 10, ""},
		{"blocked exe", "run.exe", "application/octet-stream", 100, "not allowed"},
		{"blocked upper", "RUN.EXE", "application/octet-stream", 100, "not allowed"},
		{"blocked html", "x.html", "text/html", 100, "not allowed"},
		{"blocked js", "x.js", "text/javascript", 100, "not allowed"},
		{"blocked ps1", "x.ps1", "text/plain", 100, "not allowed"},
		{"blocked script", "x.sh", "application/x-sh", 100, "not allowed"},
		{"heic rejected", "img.heic", "image/heic", 1000, "heic"},
		{"image too big", "big.jpg", "image/jpeg", 17 * 1024 * 1024, "limited to 16 MB"},
		{"video too big", "big.mp4", "video/mp4", 20 * 1024 * 1024, "limited to 16 MB"},
		{"doc too big", "big.pdf", "application/pdf", 101 * 1024 * 1024, "limited to 100 MB"},
		{"empty name", "", "image/jpeg", 100, "empty file name"},
		{"empty file", "a.jpg", "image/jpeg", 0, "empty file"},
		{"traversal unix", "../../etc/passwd", "text/plain", 10, "invalid file name"},
		{"traversal subdir", "a/b.txt", "text/plain", 10, "invalid file name"},
		{"media at limit", "p.jpg", "image/jpeg", 16 * 1024 * 1024, ""},
		{"media over limit", "p.jpg", "image/jpeg", 16*1024*1024 + 1, "limited to 16 MB"},
		{"doc at limit", "d.pdf", "application/pdf", 100 * 1024 * 1024, ""},
		{"negative size", "d.pdf", "application/pdf", -5, "empty file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAttachment(tc.fileName, tc.mime, tc.size)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %q", tc.wantErr, err.Error())
				}
			}
		})
	}
}

func TestDetectMIME(t *testing.T) {
	// JPEG magic bytes must win over a wrong extension.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	if got := DetectMIME("file.bin", jpeg); !strings.HasPrefix(got, "image/jpeg") {
		t.Errorf("want image/jpeg sniff, got %q", got)
	}
	if got := DetectMIME("doc.pdf", nil); !strings.Contains(got, "pdf") {
		t.Errorf("want pdf by extension, got %q", got)
	}
}

func TestTruncateCaption(t *testing.T) {
	if got := TruncateCaption("hello", 10); got != "hello" {
		t.Errorf("short string must pass through, got %q", got)
	}
	// Multibyte must not break: cut keeps whole runes (❤️ is 2 runes).
	in := "😊😂❤️🔥👍"
	got := TruncateCaption(in, 3)
	if len([]rune(got)) != 3 {
		t.Errorf("want 3 runes, got %q (%d runes)", got, len([]rune(got)))
	}
	if got := TruncateCaption("hello", 0); got != "" {
		t.Errorf("zero limit must give empty, got %q", got)
	}
}

func TestAttachmentValidateMethod(t *testing.T) {
	a := &Attachment{FileName: "p.png", MIME: "image/png", Size: 100}
	if err := a.Validate(); err != nil {
		t.Fatalf("want valid, got %v", err)
	}
	if a.Kind != "" {
		t.Errorf("Validate must stay pure (no mutation), kind=%q", a.Kind)
	}
	a.Normalize()
	if a.Kind != AttachmentImage {
		t.Errorf("Normalize must infer kind, got %q", a.Kind)
	}
	var nilAtt *Attachment
	if err := nilAtt.Validate(); err == nil {
		t.Fatal("nil attachment must fail")
	}
	nilAtt.Normalize() // must not panic
}
