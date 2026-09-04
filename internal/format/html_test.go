package format

import "testing"

func TestHTMLToWhatsApp(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`<p>Вітаю <strong>жирний</strong> текст</p>`, "Вітаю *жирний* текст"},
		{`<p><em>курсив</em> і <s>закресл</s></p>`, "_курсив_ і ~закресл~"},
		{`<p>код <code>mono</code></p>`, "код `mono`"},
		{`<p><a href="https://example.com">лінк</a></p>`, "лінк https://example.com"},
		{`<p><a href="https://example.com">https://example.com</a></p>`, "https://example.com"},
		{`<p>hello</p><p>world</p>`, "hello\n\nworld"},
		{`plain text`, "plain text"},
		{`<p>emoji 😊 <strong>bold 😂</strong></p>`, "emoji 😊 *bold 😂*"},
		{`<ul><li>one</li><li>two</li></ul>`, "• one\n• two"},
		// regression: user bug — meet.google.com was duplicated and became non-clickable
		{`<p>🔗 <a href="https://meet.google.com/gqs-gdnd-ayy">https://meet.google.com/gqs-gdnd-ayy</a></p>`, "🔗 https://meet.google.com/gqs-gdnd-ayy"},
		{`<p>🔗https://meet.google.com/gqs-gdnd-ayy</p>`, "🔗 https://meet.google.com/gqs-gdnd-ayy"},
		{`🔗https://meet.google.com/gqs-gdnd-ayy`, "🔗 https://meet.google.com/gqs-gdnd-ayy"},
		{`<p><em><a href="https://meet.google.com/gqs-gdnd-ayy">https://meet.google.com/gqs-gdnd-ayy</a></em></p>`, "https://meet.google.com/gqs-gdnd-ayy"},
		{`<p><strong><a href="https://meet.google.com/gqs-gdnd-ayy">https://meet.google.com/gqs-gdnd-ayy</a></strong></p>`, "https://meet.google.com/gqs-gdnd-ayy"},
		{`<p><em>https://example.com</em></p>`, "https://example.com"},
		{`<p><strong>https://example.com</strong></p>`, "https://example.com"},
		// dedup with trailing slash
		{`<p><a href="https://example.com/">https://example.com</a></p>`, "https://example.com/"},
		{`🔔 Нагадування!`, "🔔 Нагадування!"},
		// full user case
		{`<p>🔔 Нагадування!</p><p>Просимо приєднатися:<br>🔗 <a href="https://meet.google.com/gqs-gdnd-ayy">https://meet.google.com/gqs-gdnd-ayy</a></p><p>📞 +380 50 111 22 33</p>`, "🔔 Нагадування!\n\nПросимо приєднатися:\n🔗 https://meet.google.com/gqs-gdnd-ayy\n\n📞 +380 50 111 22 33"},
	}
	for i, c := range cases {
		got := HTMLToWhatsApp(c.in)
		if got != c.want {
			t.Errorf("case %d: got %q want %q (in %q)", i, got, c.want, c.in)
		}
	}
}

func TestHTMLToWhatsApp_URLInsideFormatting(t *testing.T) {
	// URL must not be wrapped in * _ ~ ` — otherwise WhatsApp will not make it clickable
	cases := []string{
		`<p><em>курсив <a href="https://example.com">линк</a></em></p>`,
		`<p><strong>жирный <a href="https://example.com">https://example.com</a> конец</strong></p>`,
	}
	for i, in := range cases {
		got := HTMLToWhatsApp(in)
		if got == "" {
			t.Fatalf("case %d empty", i)
		}
		if containsWrappedURL(got) {
			t.Errorf("case %d: url wrapped in markdown, got %q", i, got)
		}
		if !containsURL(got) {
			t.Errorf("case %d: missing url, got %q", i, got)
		}
	}
}

func containsWrappedURL(s string) bool {
	// look for *https://, _https://, ~https://, `https://
	for _, prefix := range []string{"*https://", "_https://", "~https://", "`https://"} {
		if contains(s, prefix) {
			return true
		}
	}
	for _, suffix := range []string{"https://", "com*", "com_", "com~", "com`"} {
		_ = suffix
	}
	return false
}

func containsURL(s string) bool { return contains(s, "https://") }
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestHTMLToTelegram(t *testing.T) {
	html := `<p>Вітаю <strong>жирний</strong></p><p><a href="https://example.com">лінк</a></p>`
	got := HTMLToTelegram(html)
	// should contain newlines and keep tags
	if got == "" {
		t.Fatal("empty")
	}
	// should keep strong and a
	if got != "Вітаю <strong>жирний</strong>\n\n<a href=\"https://example.com\">лінк</a>" {
		t.Errorf("got %q", got)
	}
}
