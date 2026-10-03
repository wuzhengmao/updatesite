package server

import (
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

// The changelog renderer intentionally supports only the small Markdown subset
// that release notes actually use: headings, lists, quotes, fenced code, rules,
// bold, italic, inline code and links. It is built to be safe by construction:
// the input is HTML-escaped before any markup is recognised, link targets are
// scheme-checked, and code spans are pulled out into NUL-delimited placeholders
// that no escaped text can forge.

var (
	reCodeSpan    = regexp.MustCompile("`([^`\n]+)`")
	reMDLink      = regexp.MustCompile(`\[([^\]\n]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	reAutoLink    = regexp.MustCompile(`(^|[\s(（])((?:https?://)[^\s<>()（）]+)`)
	reBold        = regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`)
	reItalic      = regexp.MustCompile(`(^|[\s(（*])\*([^*\n]+)\*`)
	reStrike      = regexp.MustCompile(`~~([^~\n]+)~~`)
	reHeading     = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reBullet      = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	reOrdered     = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	reRule        = regexp.MustCompile(`^\s*(?:---+|___+|\*\*\*+)\s*$`)
	reFence       = regexp.MustCompile("^\\s*(?:```|~~~)")
	rePlaceholder = regexp.MustCompile("\x00(\\d+)\x00")
)

// RenderMarkdown converts release notes to HTML.
func RenderMarkdown(src string) template.HTML {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")

	var b strings.Builder
	var para []string
	var list []string
	listOrdered := false
	inCode := false
	var code []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		b.WriteString("<p>")
		for i, l := range para {
			if i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(inline(l))
		}
		b.WriteString("</p>\n")
		para = para[:0]
	}
	flushList := func() {
		if len(list) == 0 {
			return
		}
		tag := "ul"
		if listOrdered {
			tag = "ol"
		}
		b.WriteString("<" + tag + ">\n")
		for _, item := range list {
			b.WriteString("<li>" + inline(item) + "</li>\n")
		}
		b.WriteString("</" + tag + ">\n")
		list = list[:0]
	}
	flushAll := func() {
		flushPara()
		flushList()
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")

		if inCode {
			if reFence.MatchString(line) {
				b.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
				code = code[:0]
				inCode = false
				continue
			}
			code = append(code, raw)
			continue
		}
		if reFence.MatchString(line) {
			flushAll()
			inCode = true
			continue
		}
		if strings.TrimSpace(line) == "" {
			flushAll()
			continue
		}
		if reRule.MatchString(line) {
			flushAll()
			b.WriteString("<hr>\n")
			continue
		}
		if m := reHeading.FindStringSubmatch(line); m != nil {
			flushAll()
			level := len(m[1])
			b.WriteString("<h" + strconv.Itoa(level) + ">" + inline(strings.TrimSpace(m[2])) + "</h" + strconv.Itoa(level) + ">\n")
			continue
		}
		if m := reBullet.FindStringSubmatch(line); m != nil {
			flushPara()
			if listOrdered {
				flushList()
			}
			listOrdered = false
			list = append(list, strings.TrimSpace(m[1]))
			continue
		}
		if m := reOrdered.FindStringSubmatch(line); m != nil {
			flushPara()
			if !listOrdered {
				flushList()
			}
			listOrdered = true
			list = append(list, strings.TrimSpace(m[1]))
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			flushAll()
			quote := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
			b.WriteString("<blockquote>" + inline(quote) + "</blockquote>\n")
			continue
		}
		flushList()
		para = append(para, strings.TrimSpace(line))
	}

	if inCode && len(code) > 0 { // unterminated fence
		b.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
	}
	flushAll()
	return template.HTML(b.String())
}

// inline renders one line of Markdown. The input is escaped first, so every
// transformation below operates on text that can no longer break out of HTML.
func inline(s string) string {
	s = html.EscapeString(s)

	var spans []string
	s = reCodeSpan.ReplaceAllStringFunc(s, func(m string) string {
		spans = append(spans, "<code>"+m[1:len(m)-1]+"</code>")
		return "\x00" + strconv.Itoa(len(spans)-1) + "\x00"
	})

	s = reMDLink.ReplaceAllStringFunc(s, func(m string) string {
		g := reMDLink.FindStringSubmatch(m)
		return link(g[1], g[2])
	})
	s = reAutoLink.ReplaceAllStringFunc(s, func(m string) string {
		g := reAutoLink.FindStringSubmatch(m)
		return g[1] + link(g[2], g[2])
	})
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		g := reBold.FindStringSubmatch(m)
		txt := g[1]
		if txt == "" {
			txt = g[2]
		}
		return "<strong>" + txt + "</strong>"
	})
	s = reStrike.ReplaceAllStringFunc(s, func(m string) string {
		return "<del>" + m[2:len(m)-2] + "</del>"
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		g := reItalic.FindStringSubmatch(m)
		return g[1] + "<em>" + g[2] + "</em>"
	})

	s = rePlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		i, err := strconv.Atoi(rePlaceholder.FindStringSubmatch(m)[1])
		if err != nil || i < 0 || i >= len(spans) {
			return ""
		}
		return spans[i]
	})
	return s
}

// link builds an anchor, dropping targets with a dangerous scheme.
func link(text, href string) string {
	if !safeURL(href) {
		return text
	}
	return `<a href="` + href + `" rel="noopener noreferrer">` + text + `</a>`
}

// safeURL allows only absolute http(s), mailto and same-site targets.
func safeURL(u string) bool {
	trimmed := strings.TrimSpace(u)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "#") {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
