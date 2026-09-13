package telegram

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Telegram entity offsets and lengths are UTF-16 code units, not bytes or runes.
// Explicit entities keep arbitrary text (HTML, shell operators, underscores)
// literal without Telegram parse_mode escaping rules.
type textEntity struct {
	Type     string `json:"type"`
	Offset   int    `json:"offset"`
	Length   int    `json:"length"`
	Language string `json:"language,omitempty"`
}

type formattedMessage struct {
	Text     string
	Entities []textEntity
}

// This deliberately small syntax is not a full Markdown parser. Unsupported or
// unclosed markup stays literal. Code contents are never parsed for emphasis.
var (
	fencedCode  = regexp.MustCompile("^```([a-zA-Z0-9_+-]*)[ \\t]*\\r?\\n$")
	inlineStyle = regexp.MustCompile("`([^`\\n]+)`|\\*\\*([^*`\\n]+)\\*\\*")
)

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func formatMessage(source string) formattedMessage {
	var b strings.Builder
	var entities []textEntity
	offset := 0
	write := func(s string) { b.WriteString(s); offset += utf16Len(s) }
	styled := func(s, kind, language string) {
		length := utf16Len(s)
		if length > 0 {
			entities = append(entities, textEntity{Type: kind, Offset: offset, Length: length, Language: language})
		}
		write(s)
	}
	inline := func(s string) {
		start := 0
		for _, m := range inlineStyle.FindAllStringSubmatchIndex(s, -1) {
			delimiter := s[m[0]]
			if (m[0] > 0 && s[m[0]-1] == delimiter) || (m[1] < len(s) && s[m[1]] == delimiter) {
				continue
			}
			slashes := 0
			for j := m[0] - 1; j >= 0 && s[j] == '\\'; j-- {
				slashes++
			}
			if slashes%2 != 0 {
				continue
			}
			write(s[start:m[0]])
			if m[2] >= 0 {
				styled(s[m[2]:m[3]], "code", "")
			} else {
				styled(s[m[4]:m[5]], "bold", "")
			}
			start = m[1]
		}
		write(s[start:])
	}
	lines := strings.SplitAfter(source, "\n")
	for i := 0; i < len(lines); i++ {
		match := fencedCode.FindStringSubmatch(lines[i])
		if match == nil {
			inline(lines[i])
			continue
		}
		end := i + 1
		for end < len(lines) && strings.TrimRight(lines[end], " \t\r\n") != "```" {
			end++
		}
		if end == len(lines) {
			// An unfinished block must not reinterpret shell syntax as emphasis.
			write(strings.Join(lines[i:], ""))
			break
		}
		body := strings.Join(lines[i+1:end], "")
		if strings.TrimSpace(body) == "" {
			write(strings.Join(lines[i:end+1], ""))
		} else {
			styled(body, "pre", match[1])
		}
		i = end
	}
	return formattedMessage{Text: b.String(), Entities: entities}
}

// splitFormatted preserves UTF-8 boundaries and clips/rebases entities on each
// page, including when a code block spans pages. The truncation notice is plain.
func splitFormatted(message formattedMessage) []formattedMessage {
	const suffix = "\n... (truncated)"
	var pages []formattedMessage
	text := message.Text
	offset := 0
	for {
		limit := maxMessageLen
		truncated := len(pages) == maxPages-1 && utf16Len(text) > limit
		if truncated {
			limit -= utf16Len(suffix)
		}
		cut, units := utf16Cut(text, limit)
		if cut < len(text) && !truncated {
			if newline := strings.LastIndexByte(text[:cut], '\n'); newline >= 0 && utf16Len(text[:newline+1]) >= limit/2 {
				cut = newline + 1
				units = utf16Len(text[:cut])
			}
		}
		page := formattedMessage{Text: text[:cut]}
		for _, e := range message.Entities {
			start := max(e.Offset, offset)
			end := min(e.Offset+e.Length, offset+units)
			if end > start {
				e.Offset = start - offset
				e.Length = end - start
				page.Entities = append(page.Entities, e)
			}
		}
		if truncated {
			page.Text += suffix
		}
		pages = append(pages, page)
		if cut == len(text) || truncated {
			return pages
		}
		text = text[cut:]
		offset += units
	}
}

func utf16Cut(s string, limit int) (int, int) {
	units := 0
	for i, r := range s {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > limit {
			return i, units
		}
		units += width
	}
	return len(s), units
}

// Invalid UTF-8 cannot be represented faithfully in JSON. Normalize before
// measuring entity offsets so encoding/json cannot change their meaning.
func renderMessages(s string) []formattedMessage {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	return splitFormatted(formatMessage(s))
}
