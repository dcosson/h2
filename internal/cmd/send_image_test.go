package cmd

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func TestSendImageCLI(t *testing.T) {
	for _, closeID := range []string{"", "done"} {
		t.Run(closeID, func(t *testing.T) {
			home := setupFakeHome(t)
			t.Setenv("H2_DIR", filepath.Join(home, ".h2"))
			config.ResetResolveCache()
			socketdir.ResetDirCache()
			t.Cleanup(func() { config.ResetResolveCache(); socketdir.ResetDirCache() })
			dir := socketdir.Dir()
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(dir, socketdir.Format(socketdir.TypeBridge, "test")))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			requests := make(chan *message.Request, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				req, err := message.ReadRequest(conn)
				if err != nil {
					return
				}
				requests <- req
				message.SendResponse(conn, &message.Response{OK: true})
			}()
			cmd := newSendCmd()
			args := []string{"--image", "example.png", "test"}
			if closeID != "" {
				args = append(args, "--closes", closeID)
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			select {
			case req := <-requests:
				expected, _ := filepath.Abs("example.png")
				if req.Type != "send-image" || req.ImagePath != expected || req.Body != "" {
					t.Fatalf("bad request %+v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("no image request")
			}
		})
	}
}

func TestSendImageValidation(t *testing.T) {
	for _, flag := range []string{"--raw", "--expects-response"} {
		t.Run(flag, func(t *testing.T) {
			cmd := newSendCmd()
			cmd.SetArgs([]string{"--image", "a.png", flag, "test"})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
	if err := checkImageTarget("/tmp/agent.test.sock", "/tmp/image.png"); err == nil {
		t.Fatal("agent image target accepted")
	}
	if err := checkImageTarget("/tmp/bridge.test.sock", "/tmp/image.png"); err != nil {
		t.Fatal(err)
	}
	if err := handleCloses("done", nil, "", "normal", false, "/tmp/image.png"); err == nil {
		t.Fatal("image close without target accepted")
	}
}
