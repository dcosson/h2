package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSendImage(t *testing.T) {
	data := testPNG(t)
	filename := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botSECRET/sendPhoto" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("chat_id") != "42" || r.FormValue("caption") != "[coder] see this" {
			t.Errorf("unexpected form %v", r.Form)
		}
		f, _, err := r.FormFile("photo")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		got, _ := io.ReadAll(f)
		if !bytes.Equal(got, data) {
			t.Error("upload changed image bytes")
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	tg := &Telegram{Token: "SECRET", ChatID: 42, BaseURL: srv.URL}
	if err := tg.SendImage(context.Background(), filename, "[coder] see this"); err != nil {
		t.Fatal(err)
	}
}

func TestSendImageRejectsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.png")
	os.WriteFile(valid, testPNG(t), 0o600)
	invalid := filepath.Join(dir, "not-image.png")
	os.WriteFile(invalid, []byte("not an image"), 0o600)
	oversized := filepath.Join(dir, "large.png")
	f, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(maxImageBytes + 1)
	f.Close()
	symlink := filepath.Join(dir, "link.png")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, path, caption string }{
		{"not-image", invalid, ""}, {"directory", dir, ""}, {"missing", filepath.Join(dir, "missing"), ""}, {"size", oversized, ""}, {"symlink", symlink, ""}, {"caption", valid, strings.Repeat("a", 1025)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tg := &Telegram{}
			if err := tg.SendImage(context.Background(), tt.path, tt.caption); err == nil {
				t.Fatal("accepted invalid image")
			}
		})
	}
}

func TestReceiveImageRoutingAndStorage(t *testing.T) {
	data := testPNG(t)
	var fileRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botSECRET/getFile":
			fileRequests.Add(1)
			r.ParseForm()
			if r.FormValue("file_id") != "large" {
				t.Errorf("wrong file id: %v", r.Form)
			}
			fmt.Fprint(w, `{"ok":true,"result":{"file_path":"photos/image.png"}}`)
		case "/file/botSECRET/photos/image.png":
			w.Write(data)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer srv.Close()
	tg := &Telegram{Token: "SECRET", ChatID: 42, BaseURL: srv.URL, AttachmentDir: filepath.Join(t.TempDir(), "images")}
	cases := []struct {
		name, caption, reply, wantAgent string
		document                        bool
	}{
		{"prefix", "coder: inspect this", "", "coder", false},
		{"reply", "inspect", "[reviewer] previous image", "reviewer", false},
		{"captionless", "", "", "", false},
		{"document", "inspect", "", "", true},
		{"command-caption", "/danger arguments", "", "", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := &message{Chat: chat{ID: 42}, Caption: tt.caption, Photo: []photoSize{{FileID: "small", Width: 1, Height: 1}, {FileID: "large", Width: 2, Height: 2}}}
			if tt.document {
				m.Photo = nil
				m.Document = &document{FileID: "large", MimeType: "image/png"}
			}
			if tt.reply != "" {
				m.ReplyToMessage = &message{Caption: tt.reply}
			}
			called := false
			tg.handleMessage(context.Background(), m, func(agent, body string) {
				called = true
				if agent != tt.wantAgent {
					t.Errorf("agent %q want %q", agent, tt.wantAgent)
				}
				marker := "[Image attachment: "
				start := strings.Index(body, marker)
				if start < 0 {
					t.Fatalf("missing image in %q", body)
				}
				filename := strings.SplitN(body[start+len(marker):], "]", 2)[0]
				got, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, data) {
					t.Error("stored bytes changed")
				}
				info, _ := os.Stat(filename)
				if info.Mode().Perm() != 0o600 {
					t.Errorf("mode %v", info.Mode())
				}
				if !strings.HasPrefix(filename, tg.AttachmentDir+string(os.PathSeparator)) {
					t.Error("attachment escaped storage")
				}
				if strings.Contains(body, "SECRET") {
					t.Error("token leaked")
				}
			})
			if !called {
				t.Fatal("not delivered")
			}
		})
	}
	if fileRequests.Load() != int32(len(cases)) {
		t.Errorf("downloads %d", fileRequests.Load())
	}
}

func TestReceiveImageRejectsAndNotifies(t *testing.T) {
	for _, tt := range []struct {
		name, remotePath             string
		size                         int64
		invalid, oversized, redirect bool
	}{
		{name: "metadata-size", remotePath: "photos/a.png", size: maxImageBytes + 1},
		{name: "traversal", remotePath: "../secret"}, {name: "absolute", remotePath: "/etc/passwd"}, {name: "url", remotePath: "https://elsewhere/image.png"},
		{name: "encoded", remotePath: "photos/%2e%2e/secret"}, {name: "invalid", remotePath: "photos/a.png", invalid: true},
		{name: "oversized", remotePath: "photos/a.png", oversized: true}, {name: "redirect", remotePath: "photos/a.png", redirect: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var notifications atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "getFile"):
					json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": tt.remotePath, "file_size": tt.size}})
				case strings.Contains(r.URL.Path, "/file/"):
					if tt.redirect {
						http.Redirect(w, r, "https://example.invalid/botSECRET", http.StatusFound)
					} else if tt.invalid {
						fmt.Fprint(w, "invalid image")
					} else if tt.oversized {
						w.Write(make([]byte, maxImageBytes+1))
					} else {
						w.Write(testPNG(t))
					}
				case strings.HasSuffix(r.URL.Path, "sendMessage"):
					notifications.Add(1)
					r.ParseForm()
					if strings.Contains(r.FormValue("text"), "SECRET") {
						t.Error("token leaked")
					}
					fmt.Fprint(w, `{"ok":true}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			dir := t.TempDir()
			tg := &Telegram{Token: "SECRET", ChatID: 42, BaseURL: srv.URL, AttachmentDir: dir}
			tg.handleMessage(context.Background(), &message{Chat: chat{ID: 42}, Photo: []photoSize{{FileID: "x"}}}, func(string, string) { t.Error("failed image delivered") })
			if notifications.Load() != 1 {
				t.Errorf("notifications %d", notifications.Load())
			}
			files, _ := os.ReadDir(dir)
			if len(files) != 0 {
				t.Error("failed download left files")
			}
		})
	}
}

func TestImageRequestRedactsTransportError(t *testing.T) {
	tg := &Telegram{Token: "SECRET", BaseURL: "http://127.0.0.1:1", AttachmentDir: t.TempDir()}
	_, err := tg.receiveImage(context.Background(), "x", 0)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unsafe error %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tg.receiveImage(ctx, "x", 0)
	if err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestImageChatAuthorization(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "must not download", 500) }))
	defer srv.Close()
	tg := &Telegram{ChatID: 42, AttachmentDir: t.TempDir(), BaseURL: srv.URL}
	tg.handleMessage(context.Background(), &message{Chat: chat{ID: 99}, Photo: []photoSize{{FileID: "x"}}}, func(string, string) { t.Fatal("unauthorized delivery") })
	if requests.Load() != 0 {
		t.Fatal("unauthorized image triggered network activity")
	}
}

func TestPollImage(t *testing.T) {
	data := testPNG(t)
	var polled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botTOKEN/getUpdates":
			if !polled.Swap(true) {
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42},"caption":"coder: inspect","photo":[{"file_id":"img","width":2,"height":2}]}}]}`)
			} else {
				<-r.Context().Done()
			}
		case "/botTOKEN/getFile":
			fmt.Fprint(w, `{"ok":true,"result":{"file_path":"photos/a.png"}}`)
		case "/file/botTOKEN/photos/a.png":
			w.Write(data)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tg := &Telegram{Token: "TOKEN", ChatID: 42, BaseURL: srv.URL, AttachmentDir: filepath.Join(t.TempDir(), "images")}
	got := make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tg.Start(ctx, func(agent, body string) {
		if agent != "coder" {
			t.Errorf("agent %q", agent)
		}
		got <- body
	}); err != nil {
		t.Fatal(err)
	}
	defer tg.Stop()
	select {
	case body := <-got:
		if !strings.Contains(body, "[Image attachment:") {
			t.Fatalf("missing image: %q", body)
		}
	case <-ctx.Done():
		t.Fatal("no image delivered")
	}
}

func TestImageStorageAndDecodeFailures(t *testing.T) {
	data := testPNG(t)
	if _, err := imageFormat(data[:len(data)/2]); err == nil {
		t.Fatal("accepted truncated PNG")
	}
	tg := &Telegram{AttachmentDir: t.TempDir(), BaseURL: "http://127.0.0.1:1"}
	if _, err := tg.receiveImage(context.Background(), "id", maxImageBytes+1); err == nil || !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("declared size was not rejected: %v", err)
	}
	tg.AttachmentDir = ""
	if _, err := tg.receiveImage(context.Background(), "id", 0); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("missing storage accepted: %v", err)
	}
}
