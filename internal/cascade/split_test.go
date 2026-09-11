package cascade

import (
	"strings"
	"testing"
)

func TestSplitTextFitsSingle(t *testing.T) {
	got := SplitText("hello", 1024)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("short text must stay whole, got %q", got)
	}
}

func TestSplitTextEmpty(t *testing.T) {
	if got := SplitText("", 100); len(got) != 0 {
		t.Fatalf("empty text must give no pieces, got %q", got)
	}
}

func TestSplitTextChunksWithinLimit(t *testing.T) {
	in := strings.Repeat("слово ", 500) // 3000 runes
	got := SplitText(in, 1024)
	if len(got) < 3 {
		t.Fatalf("3000 runes must give 3+ chunks, got %d", len(got))
	}
	for i, p := range got {
		if len([]rune(p)) > 1024 {
			t.Errorf("chunk %d exceeds limit: %d runes", i, len([]rune(p)))
		}
		if p == "" {
			t.Errorf("chunk %d must not be empty", i)
		}
	}
	if !strings.HasPrefix(got[0], "слово") {
		t.Errorf("first chunk must start at the beginning, got %q", got[0][:20])
	}
	trimmed := strings.TrimSpace(in)
	if !strings.HasSuffix(got[len(got)-1], strings.TrimSpace(trimmed[len(trimmed)-20:])) {
		t.Errorf("last chunk must end at the end, got %q", got[len(got)-1])
	}
}

func TestSplitTextPrefersParagraphBoundary(t *testing.T) {
	head := strings.Repeat("a", 900)
	tail := strings.Repeat("b", 500)
	in := head + "\n\n" + tail
	got := SplitText(in, 1024)
	if len(got) != 2 {
		t.Fatalf("want 2 chunks split at blank line, got %d", len(got))
	}
	if got[0] != head {
		t.Errorf("first chunk must end at paragraph boundary, got len=%d", len(got[0]))
	}
	if got[1] != tail {
		t.Errorf("second chunk must be the tail, got len=%d", len(got[1]))
	}
}

func TestSplitTextHardCutUnbroken(t *testing.T) {
	in := strings.Repeat("x", 2500)
	got := SplitText(in, 1000)
	if len(got) != 3 {
		t.Fatalf("unbroken run must hard-cut into 3, got %d", len(got))
	}
	if len([]rune(got[0])) != 1000 || len([]rune(got[1])) != 1000 || len([]rune(got[2])) != 500 {
		t.Errorf("hard cuts must be exact, got %d/%d/%d", len([]rune(got[0])), len([]rune(got[1])), len([]rune(got[2])))
	}
}

func TestSplitTextRuneSafe(t *testing.T) {
	in := strings.Repeat("😊", 100)
	got := SplitText(in, 30)
	for i, p := range got {
		if len([]rune(p)) > 30 {
			t.Errorf("chunk %d exceeds limit", i)
		}
		if strings.Contains(p, "\uFFFD") {
			t.Errorf("chunk %d has broken UTF-8", i)
		}
	}
}

func TestSplitLongTextFitsCaption(t *testing.T) {
	head, tails := SplitLongText("short", 1024, 4096)
	if head != "short" {
		t.Errorf("head must carry the text, got %q", head)
	}
	if tails != nil {
		t.Errorf("fitting text must have no tails, got %q", tails)
	}
}

func TestSplitLongTextOverflow(t *testing.T) {
	in := strings.Repeat("a", 900) + "\n\n" + strings.Repeat("b", 5000)
	head, tails := SplitLongText(in, 1024, 4096)
	if len([]rune(head)) > 1024 {
		t.Errorf("head exceeds caption: %d runes", len([]rune(head)))
	}
	if len(tails) == 0 {
		t.Fatal("overflow must produce tails")
	}
	for i, tl := range tails {
		if len([]rune(tl)) > 4096 {
			t.Errorf("tail %d exceeds text limit: %d runes", i, len([]rune(tl)))
		}
	}
	// No content lost except boundary whitespace.
	joined := head + strings.Join(tails, "")
	stripped := strings.ReplaceAll(strings.ReplaceAll(in, "\n", ""), " ", "")
	got := strings.ReplaceAll(strings.ReplaceAll(joined, "\n", ""), " ", "")
	if got != stripped {
		t.Errorf("split must preserve content: want %d content runes, got %d", len([]rune(stripped)), len([]rune(got)))
	}
}
