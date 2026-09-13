package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name, source, text string
		entities           []textEntity
	}{
		{"plain", "<b>x</b> & a_b /tmp/a_b\n• punkt\n1. steg", "<b>x</b> & a_b /tmp/a_b\n• punkt\n1. steg", nil},
		{"bold", "**Status**\n\n• **Klar**", "Status\n\n• Klar", []textEntity{{Type: "bold", Offset: 0, Length: 6}, {Type: "bold", Offset: 10, Length: 4}}},
		{"emoji", "🐕 **Blå** `æøå`", "🐕 Blå æøå", []textEntity{{Type: "bold", Offset: 3, Length: 3}, {Type: "code", Offset: 7, Length: 3}}},
		{"literal code", "`**x** <b> & a_b`", "**x** <b> & a_b", []textEntity{{Type: "code", Offset: 0, Length: 15}}},
		{"fence", "Kjør:\n```bash\nprintf '**x** & < >'\n```\nFerdig", "Kjør:\nprintf '**x** & < >'\nFerdig", []textEntity{{Type: "pre", Offset: 6, Length: 21, Language: "bash"}}},
		{"unclosed", "**åpen og `åpen", "**åpen og `åpen", nil},
		{"unfinished block", "```sh\n**literal** `literal`", "```sh\n**literal** `literal`", nil},
		{"unsupported delimiters", "``x`` ***y***", "``x`` ***y***", nil},
		{"empty", "```\n```", "```\n```", nil},
		{"two blocks", "```\nx\n```\n\n```go\ny\n```", "x\n\ny\n", []textEntity{{Type: "pre", Offset: 0, Length: 2}, {Type: "pre", Offset: 3, Length: 2, Language: "go"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMessage(tt.source)
			if got.Text != tt.text || !reflect.DeepEqual(got.Entities, tt.entities) {
				t.Fatalf("got %#v; want text %q entities %#v", got, tt.text, tt.entities)
			}
		})
	}
}

func TestFormattedPagination(t *testing.T) {
	for _, token := range []string{"x", "ø", "🐕"} {
		t.Run(token, func(t *testing.T) {
			body := strings.Repeat(token, 4500) + "\n"
			pages := renderMessages("```text\n" + body + "```")
			var joined strings.Builder
			for _, page := range pages {
				if !utf8.ValidString(page.Text) || utf16Len(page.Text) > maxMessageLen {
					t.Fatalf("invalid page")
				}
				if len(page.Entities) != 1 || page.Entities[0].Offset != 0 || page.Entities[0].Length != utf16Len(page.Text) || page.Entities[0].Type != "pre" {
					t.Fatalf("bad entity: %#v", page.Entities)
				}
				joined.WriteString(page.Text)
			}
			if joined.String() != body {
				t.Fatal("pagination lost content")
			}
		})
	}
}

func TestFormattingTruncation(t *testing.T) {
	pages := renderMessages("**" + strings.Repeat("🐕", 9000) + "**")
	if len(pages) != maxPages {
		t.Fatalf("pages %d", len(pages))
	}
	last := pages[len(pages)-1]
	suffix := "\n... (truncated)"
	if !strings.HasSuffix(last.Text, suffix) {
		t.Fatal("missing notice")
	}
	if last.Entities[0].Length != utf16Len(strings.TrimSuffix(last.Text, suffix)) {
		t.Fatal("notice styled or bad offset")
	}
	for _, page := range pages {
		if !utf8.ValidString(page.Text) || utf16Len(page.Text) > maxMessageLen {
			t.Fatal("invalid page")
		}
	}
}

func TestSendFormattingEntities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := r.FormValue("text"); got != "🐕 Status\nKjør h2o list" {
			t.Errorf("text %q", got)
		}
		if r.FormValue("parse_mode") != "" {
			t.Error("must not use parse_mode")
		}
		var entities []textEntity
		if err := json.Unmarshal([]byte(r.FormValue("entities")), &entities); err != nil {
			t.Error(err)
		}
		want := []textEntity{{Type: "bold", Offset: 3, Length: 6}, {Type: "code", Offset: 15, Length: 8}}
		if !reflect.DeepEqual(entities, want) {
			t.Errorf("entities %#v", entities)
		}
		json.NewEncoder(w).Encode(apiResponse{OK: true})
	}))
	defer srv.Close()
	tg := &Telegram{Token: "TOKEN", ChatID: 42, BaseURL: srv.URL}
	if err := tg.Send(context.Background(), "🐕 **Status**\nKjør `h2o list`"); err != nil {
		t.Fatal(err)
	}
}

func FuzzRenderMessages(f *testing.F) {
	for _, s := range []string{"🐕 **klar**", "```sh\necho x\n```", "`x`", "\xff**x**", strings.Repeat("🐕", 9000)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		pages := renderMessages(s)
		if len(pages) > maxPages {
			t.Fatal("too many pages")
		}
		for _, p := range pages {
			n := utf16Len(p.Text)
			if !utf8.ValidString(p.Text) || n > maxMessageLen {
				t.Fatal("invalid text")
			}
			lastEnd := 0
			for _, e := range p.Entities {
				if e.Offset < lastEnd || e.Length <= 0 || e.Offset+e.Length > n {
					t.Fatalf("invalid entity %#v", e)
				}
				lastEnd = e.Offset + e.Length
			}
		}
	})
}
