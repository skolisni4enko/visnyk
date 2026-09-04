package format

import (
	stdhtml "html"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

// HTMLToWhatsApp converts Quill/HTML to WhatsApp markdown plain text.
// Mapping: <b><strong> -> *...*, <i><em> -> _..._, <s><strike><del> -> ~...~, <u><ins> -> _..._, <code> -> `...`, <pre> -> ```...```, <a href> -> text (url) or url, <br>/<p>/<div>/<li> -> newline.
func HTMLToWhatsApp(in string) string {
	if strings.TrimSpace(in) == "" {
		return ""
	}
	if !strings.Contains(in, "<") {
		out := stdhtml.UnescapeString(strings.TrimSpace(in))
		return postProcessWhatsApp(out)
	}
	wrapped := "<div>" + in + "</div>"
	doc, err := xhtml.Parse(strings.NewReader(wrapped))
	if err != nil {
		return postProcessWhatsApp(fallbackStrip(in))
	}
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		switch n.Type {
		case xhtml.TextNode:
			b.WriteString(n.Data)
		case xhtml.ElementNode:
			tag := strings.ToLower(n.Data)
			switch tag {
			case "br":
				b.WriteString("\n")
				return
			case "p", "div", "h1", "h2", "h3", "h4", "blockquote", "pre":
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				// paragraph separation -> double newline, trimmed later
				if !strings.HasSuffix(b.String(), "\n\n") {
					b.WriteString("\n\n")
				}
				return
			case "ul", "ol":
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				if !strings.HasSuffix(b.String(), "\n\n") {
					b.WriteString("\n")
				}
				return
			case "li":
				b.WriteString("• ")
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				b.WriteString("\n")
				return
			case "strong", "b":
				// Do not wrap URL in markdown — WhatsApp will not make *https://* or _https://_ clickable
				if subtreeHasLink(n) {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
					return
				}
				b.WriteString("*")
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				b.WriteString("*")
				return
			case "em", "i":
				if subtreeHasLink(n) {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
					return
				}
				b.WriteString("_")
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				b.WriteString("_")
				return
			case "u", "ins":
				if subtreeHasLink(n) {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
					return
				}
				b.WriteString("_")
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				b.WriteString("_")
				return
			case "s", "strike", "del":
				if subtreeHasLink(n) {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
					return
				}
				b.WriteString("~")
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				b.WriteString("~")
				return
			case "code":
				parentIsPre := n.Parent != nil && strings.ToLower(n.Parent.Data) == "pre"
				if subtreeHasLink(n) {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
					return
				}
				if !parentIsPre {
					b.WriteString("`")
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				if !parentIsPre {
					b.WriteString("`")
				}
				return
			case "a":
				href := ""
				for _, a := range n.Attr {
					if strings.ToLower(a.Key) == "href" {
						href = a.Val
						break
					}
				}
				var inner strings.Builder
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					collectText(c, &inner)
				}
				text := strings.TrimSpace(inner.String())
				href = strings.TrimSpace(href)
				// normalized comparison for dedup (case meet.google.com with trailing / and &amp;)
				if text == "" && href != "" {
					b.WriteString(href)
				} else if href == "" || urlsEqual(text, href) {
					// text is already a URL — do not duplicate
					if href != "" && text != "" && urlsEqual(text, href) {
						b.WriteString(href)
					} else {
						b.WriteString(text)
					}
				} else {
					b.WriteString(text + " " + href)
				}
				return
			default:
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				return
			}
		default:
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	var div *xhtml.Node
	var findDiv func(*xhtml.Node)
	findDiv = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && strings.ToLower(n.Data) == "div" && div == nil {
			div = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findDiv(c)
		}
	}
	findDiv(doc)
	if div == nil {
		return fallbackStrip(in)
	}
	for c := div.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	out := stdhtml.UnescapeString(b.String())
	out = regexp.MustCompile(`\n{3,}`).ReplaceAllString(out, "\n\n")
	out = strings.TrimSpace(out)
	return postProcessWhatsApp(out)
}

func postProcessWhatsApp(s string) string {
	if s == "" {
		return s
	}
	// 1) Remove markdown wrapper that breaks clickability: *https://*, _https://_, ~https://~, `https://`
	//    Do it BEFORE inserting space, otherwise "_https://..." -> "_ https://..." will not match.
	s = regexp.MustCompile(`\*+(https?://[^\s*]+)\*+`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`_+(https?://[^\s_]+)_+`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`~+(https?://[^\s~]+)~+`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile("`+(https?://[^\\s`]+)`+").ReplaceAllString(s, "$1")
	// 2) Ensure space before URL if it was eaten by emoji/symbol like "🔗https://"
	s = regexp.MustCompile(`([^\s\n])(https?://)`).ReplaceAllString(s, "$1 $2")
	// 3) Consecutive duplicate "URL URL" -> keep one (guards if dedup comparison failed)
	//    RE2 does not support \1, do manual dedup
	s = dedupConsecutiveURLs(s)
	s = strings.TrimSpace(s)
	return s
}

func dedupConsecutiveURLs(s string) string {
	reURL := regexp.MustCompile(`https?://[^\s]+`)
	urls := reURL.FindAllString(s, -1)
	for _, u := range urls {
		dup := u + " " + u
		for strings.Contains(s, dup) {
			s = strings.ReplaceAll(s, dup, u)
		}
		dupNL := u + "\n" + u
		for strings.Contains(s, dupNL) {
			s = strings.ReplaceAll(s, dupNL, u)
		}
	}
	return s
}

func normalizeURLForCompare(s string) string {
	s = strings.TrimSpace(stdhtml.UnescapeString(s))
	s = strings.TrimRight(s, "/")
	return strings.ToLower(s)
}

func urlsEqual(a, b string) bool {
	return normalizeURLForCompare(a) == normalizeURLForCompare(b)
}

func subtreeHasLink(n *xhtml.Node) bool {
	if n == nil {
		return false
	}
	if n.Type == xhtml.ElementNode && strings.ToLower(n.Data) == "a" {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if subtreeHasLink(c) {
			return true
		}
	}
	return false
}

func collectText(n *xhtml.Node, b *strings.Builder) {
	if n.Type == xhtml.TextNode {
		b.WriteString(n.Data)
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, b)
	}
}

func fallbackStrip(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")
	s = stdhtml.UnescapeString(s)
	return strings.TrimSpace(s)
}

// IsHTML reports if string looks like HTML from Quill.
func IsHTML(s string) bool {
	return strings.Contains(s, "<") && strings.Contains(s, ">")
}

// HTMLToTelegram sanitizes Quill HTML for Telegram HTML parser.
func HTMLToTelegram(in string) string {
	if strings.TrimSpace(in) == "" {
		return ""
	}
	if !IsHTML(in) {
		return stdhtml.EscapeString(in)
	}
	s := in
	replacements := []struct{ re, repl string }{
		{`(?i)<br\s*/?>`, "\n"},
		{`(?i)</p>\s*<p[^>]*>`, "\n\n"},
		{`(?i)<p[^>]*>`, ""},
		{`(?i)</p>`, "\n"},
		{`(?i)<div[^>]*>`, ""},
		{`(?i)</div>`, "\n"},
		{`(?i)<h[1-6][^>]*>`, ""},
		{`(?i)</h[1-6]>`, "\n"},
		{`(?i)<li[^>]*>`, "• "},
		{`(?i)</li>`, "\n"},
		{`(?i)<ul[^>]*>`, ""},
		{`(?i)</ul>`, "\n"},
		{`(?i)<ol[^>]*>`, ""},
		{`(?i)</ol>`, "\n"},
		{`(?i)<blockquote[^>]*>`, ""},
		{`(?i)</blockquote>`, "\n"},
	}
	for _, r := range replacements {
		re := regexp.MustCompile(r.re)
		s = re.ReplaceAllString(s, r.repl)
	}
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	return s
}

// PlainForTelegram returns HTML suitable for telegram html parser.
func PlainForTelegram(in string) string {
	if IsHTML(in) {
		return HTMLToTelegram(in)
	}
	return stdhtml.EscapeString(in)
}
