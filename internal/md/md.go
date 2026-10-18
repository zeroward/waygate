package md

import (
	"html"
	"regexp"
	"strings"
)

var (
	reBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reCode = regexp.MustCompile("`([^`]+)`")
	reLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+|mailto:[^)\s]+|/[A-Za-z0-9/_?=&-]*)\)`)
	reOL   = regexp.MustCompile(`^\d+[.)]\s+`)
)

// HTML renders a small, safe markdown subset to HTML.
// Raw HTML is escaped. Only http(s), mailto, and same-site relative links are allowed.
func HTML(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	var b strings.Builder
	inList := false
	inOL := false
	inCode := false
	var code []string

	flushList := func() {
		if inList {
			b.WriteString("</ul>\n")
			inList = false
		}
		if inOL {
			b.WriteString("</ol>\n")
			inOL = false
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "```") {
			if inCode {
				writeCode(&b, code)
				code = nil
				inCode = false
			} else {
				flushList()
				inCode = true
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		trim := strings.TrimSpace(line)
		if trim == "" {
			flushList()
			continue
		}
		if strings.HasPrefix(trim, "|") && i+1 < len(lines) && isTableSep(strings.TrimSpace(lines[i+1])) {
			flushList()
			header := splitRow(trim)
			i++ // separator
			var rows [][]string
			for i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(next, "|") {
					break
				}
				i++
				rows = append(rows, splitRow(next))
			}
			writeTable(&b, header, rows)
			continue
		}
		if strings.HasPrefix(trim, "# ") {
			flushList()
			b.WriteString("<h1>")
			b.WriteString(inline(strings.TrimPrefix(trim, "# ")))
			b.WriteString("</h1>\n")
			continue
		}
		if strings.HasPrefix(trim, "## ") {
			flushList()
			b.WriteString("<h2>")
			b.WriteString(inline(strings.TrimPrefix(trim, "## ")))
			b.WriteString("</h2>\n")
			continue
		}
		if strings.HasPrefix(trim, "### ") {
			flushList()
			b.WriteString("<h3>")
			b.WriteString(inline(strings.TrimPrefix(trim, "### ")))
			b.WriteString("</h3>\n")
			continue
		}
		if strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") {
			if inOL {
				b.WriteString("</ol>\n")
				inOL = false
			}
			if !inList {
				b.WriteString("<ul>\n")
				inList = true
			}
			item := strings.TrimPrefix(trim, "- ")
			item = strings.TrimPrefix(item, "* ")
			b.WriteString("<li>")
			b.WriteString(inline(item))
			b.WriteString("</li>\n")
			continue
		}
		if reOL.MatchString(trim) {
			if inList {
				b.WriteString("</ul>\n")
				inList = false
			}
			if !inOL {
				b.WriteString("<ol>\n")
				inOL = true
			}
			item := reOL.ReplaceAllString(trim, "")
			b.WriteString("<li>")
			b.WriteString(inline(item))
			b.WriteString("</li>\n")
			continue
		}
		flushList()
		b.WriteString("<p>")
		b.WriteString(inline(trim))
		b.WriteString("</p>\n")
	}
	flushList()
	if inCode {
		writeCode(&b, code)
	}
	return b.String()
}

func writeCode(b *strings.Builder, code []string) {
	body := strings.Join(code, "\n")
	b.WriteString(`<div class="kb-code"><div class="kb-code-actions"><button type="button" class="kb-copy">Copy</button>`)
	if strings.Contains(strings.ToLower(body), "set realmlist") {
		b.WriteString(`<a class="kb-dl" href="/realmlist.wtf">Download</a>`)
	}
	b.WriteString(`</div><pre><code>`)
	b.WriteString(html.EscapeString(body))
	b.WriteString("</code></pre></div>\n")
}

func writeTable(b *strings.Builder, header []string, rows [][]string) {
	b.WriteString("<table>\n<thead><tr>")
	for _, h := range header {
		b.WriteString("<th>")
		b.WriteString(inline(h))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead>\n<tbody>\n")
	for _, row := range rows {
		b.WriteString("<tr>")
		n := len(header)
		if len(row) > n {
			n = len(row)
		}
		for i := 0; i < n; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString("<td>")
			b.WriteString(inline(cell))
			b.WriteString("</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody></table>\n")
}

func isTableSep(line string) bool {
	if !strings.HasPrefix(line, "|") {
		return false
	}
	cells := splitRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if cell == "" {
			return false
		}
		for _, r := range cell {
			if r != '-' && r != ':' {
				return false
			}
		}
		if !strings.ContainsRune(cell, '-') {
			return false
		}
	}
	return true
}

func splitRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

func inline(s string) string {
	s = html.EscapeString(s)
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reCode.ReplaceAllString(s, "<code>$1</code>")
	s = reLink.ReplaceAllString(s, `<a href="$2" rel="noopener noreferrer">$1</a>`)
	return s
}
