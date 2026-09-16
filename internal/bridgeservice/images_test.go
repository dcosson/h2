package bridgeservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"h2/internal/bridge"
	"h2/internal/bridge/telegram"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

type mockImageSender struct {
	mockSender
	path, caption string
	fail          bool
}

func (m *mockImageSender) SendImage(_ context.Context, path, caption string) error {
	m.path = path
	m.caption = caption
	if m.fail {
		return errors.New("upload failed")
	}
	return nil
}

func TestSendOutboundImage(t *testing.T) {
	img := &mockImageSender{mockSender: mockSender{name: "image"}}
	text := &mockSender{name: "text-only"}
	s := New([]bridge.Bridge{img, text}, "test", "concierge", "", t.TempDir(), nil)
	if err := s.sendOutboundImage("coder", "caption", "/tmp/image.png"); err != nil {
		t.Fatal(err)
	}
	if img.path != "/tmp/image.png" || img.caption != "[coder] caption" {
		t.Fatalf("wrong image delivery: %+v", img)
	}
	if len(text.Messages()) != 0 {
		t.Fatal("silently sent caption without image to text-only bridge")
	}
	if err := s.sendOutboundImage("concierge", "plain", "/tmp/image.png"); err != nil {
		t.Fatal(err)
	}
	if img.caption != "plain" {
		t.Fatalf("concierge caption: %q", img.caption)
	}
	img.fail = true
	if err := s.sendOutboundImage("coder", "caption", "/tmp/image.png"); err == nil {
		t.Fatal("upload failure swallowed")
	}
	s = New([]bridge.Bridge{text}, "test", "", "", t.TempDir(), nil)
	if err := s.sendOutboundImage("coder", "caption", "/tmp/image.png"); err == nil {
		t.Fatal("text-only bridge accepted image")
	}
}

func TestSendImageSocket(t *testing.T) {
	for _, filename := range []string{"/tmp/image.png", ""} {
		t.Run(filename, func(t *testing.T) {
			img := &mockImageSender{mockSender: mockSender{name: "image"}}
			s := New([]bridge.Bridge{img}, "test", "", "", t.TempDir(), nil)
			client, server := net.Pipe()
			defer client.Close()
			go s.handleConn(server)
			if err := message.SendRequest(client, &message.Request{Type: "send-image", From: "coder", ImagePath: filename}); err != nil {
				t.Fatal(err)
			}
			resp, err := message.ReadResponse(client)
			if err != nil {
				t.Fatal(err)
			}
			if resp.OK != (filename != "") {
				t.Fatalf("unexpected response %+v", resp)
			}
			if filename != "" && img.path != filename {
				t.Error("image path lost on wire")
			}
		})
	}
}

// Exercise the actual Telegram receiver and sender with the bridge socket and
// agent message protocol; only the remote Bot API and the agent are test doubles.
func TestTelegramImageRoundTrip(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	var polled atomic.Bool
	uploaded := make(chan string, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botTEST/getUpdates":
			if !polled.Swap(true) {
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":42},"caption":"test-agent: look","photo":[{"file_id":"photo","width":2,"height":2}]}}]}`)
			} else {
				<-r.Context().Done()
			}
		case "/botTEST/getFile":
			fmt.Fprint(w, `{"ok":true,"result":{"file_path":"photos/a.png"}}`)
		case "/file/botTEST/photos/a.png":
			w.Write(data.Bytes())
		case "/botTEST/sendPhoto":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				http.Error(w, "bad multipart", http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			f, _, err := r.FormFile("photo")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			got, err := io.ReadAll(f)
			if err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(got, data.Bytes()) {
				t.Error("round trip changed image bytes")
			}
			uploaded <- r.FormValue("caption")
			fmt.Fprint(w, `{"ok":true}`)
		default:
			t.Errorf("unexpected API request %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer api.Close()
	dir := shortTempDir(t)
	agentSocket := filepath.Join(dir, socketdir.Format(socketdir.TypeAgent, "test-agent"))
	ln, err := net.Listen("unix", agentSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan *message.Request, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := message.ReadRequest(conn)
		if err != nil {
			return
		}
		received <- req
		message.SendResponse(conn, &message.Response{OK: true})
	}()
	tg := &telegram.Telegram{Token: "TEST", ChatID: 42, BaseURL: api.URL, AttachmentDir: filepath.Join(dir, "attachments")}
	s := New([]bridge.Bridge{tg}, "telegram", "test-agent", "", dir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tg.Start(ctx, s.handleInbound); err != nil {
		t.Fatal(err)
	}
	defer tg.Stop()
	var filename string
	select {
	case req := <-received:
		if req.Type != "send" || req.From != "telegram" || !strings.HasPrefix(req.Body, "look") {
			t.Fatalf("bad inbound request %+v", req)
		}
		marker := "[Image attachment: "
		start := strings.Index(req.Body, marker)
		if start < 0 {
			t.Fatal("attachment missing from delivered message")
		}
		filename = strings.SplitN(req.Body[start+len(marker):], "]", 2)[0]
		got, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data.Bytes()) {
			t.Fatal("agent cannot read the received image")
		}
	case <-ctx.Done():
		t.Fatal("no agent delivery")
	}
	client, server := net.Pipe()
	defer client.Close()
	go s.handleConn(server)
	if err := message.SendRequest(client, &message.Request{Type: "send-image", From: "test-agent", ImagePath: filename, Body: "inspected"}); err != nil {
		t.Fatal(err)
	}
	resp, err := message.ReadResponse(client)
	if err != nil || !resp.OK {
		t.Fatalf("outbound failed: %+v, %v", resp, err)
	}
	select {
	case caption := <-uploaded:
		if caption != "inspected" {
			t.Fatalf("caption %q", caption)
		}
	case <-ctx.Done():
		t.Fatal("no upload")
	}
}
