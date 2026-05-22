package controlclient

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	control "github.com/tmc/cove/internal/control"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestClientSimpleRequests(t *testing.T) {
	tests := []struct {
		name     string
		run      func(*Client) error
		wantType string
		check    func(*testing.T, *controlpb.ControlRequest)
	}{
		{
			name:     "ping",
			run:      func(c *Client) error { return c.Ping() },
			wantType: "ping",
		},
		{
			name:     "gui input backend",
			run:      func(c *Client) error { return c.SetGUIInputBackend("direct") },
			wantType: "gui-input-backend-direct",
		},
		{
			name:     "gui capture backend",
			run:      func(c *Client) error { return c.SetGUICaptureBackend("framebuffer") },
			wantType: "gui-capture-backend-framebuffer",
		},
		{
			name:     "key event",
			run:      func(c *Client) error { return c.SendKeyEvent(36, true, 1, true) },
			wantType: "key",
			check: func(t *testing.T, req *controlpb.ControlRequest) {
				key := req.GetKey()
				if key.GetKeyCode() != 36 || !key.GetKeyDown() || key.GetModifiers() != 1 || !key.GetUseCgEvent() {
					t.Fatalf("key = %+v", key)
				}
			},
		},
		{
			name:     "text",
			run:      func(c *Client) error { return c.TypeText("hello") },
			wantType: "text",
			check: func(t *testing.T, req *controlpb.ControlRequest) {
				if got := req.GetText().GetText(); got != "hello" {
					t.Fatalf("text = %q, want hello", got)
				}
			},
		},
		{
			name:     "pause",
			run:      func(c *Client) error { return c.Pause() },
			wantType: "pause",
		},
		{
			name:     "resume",
			run:      func(c *Client) error { return c.Resume() },
			wantType: "resume",
		},
		{
			name:     "stop",
			run:      func(c *Client) error { return c.Stop() },
			wantType: "stop",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return &controlpb.ControlResponse{Success: true}
			})
			c := New(sock)
			c.SetAuthToken("secret")
			if err := tt.run(c); err != nil {
				t.Fatalf("run: %v", err)
			}
			req := <-reqc
			if req.GetType() != tt.wantType {
				t.Fatalf("type = %q, want %q", req.GetType(), tt.wantType)
			}
			if req.GetAuthToken() != "secret" {
				t.Fatalf("auth token = %q, want secret", req.GetAuthToken())
			}
			if tt.check != nil {
				tt.check(t, req)
			}
		})
	}
}

func TestClientSimpleRequestFailures(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Client) error
		want string
	}{
		{"ping", func(c *Client) error { return c.Ping() }, "ping failed: no"},
		{"pause", func(c *Client) error { return c.Pause() }, "pause: no"},
		{"resume", func(c *Client) error { return c.Resume() }, "resume: no"},
		{"stop", func(c *Client) error { return c.Stop() }, "stop: no"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock := serveControlClientTest(t, func(*controlpb.ControlRequest) *controlpb.ControlResponse {
				return &controlpb.ControlResponse{Success: false, Error: "no"}
			})
			if err := tt.run(New(sock)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestClientTimeoutRestored(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Client) error
	}{
		{"type text", func(c *Client) error { return c.TypeText("hello") }},
		{"ocr all text", func(c *Client) error { _, err := c.OCRAllText(); return err }},
		{"ocr click text", func(c *Client) error { return c.OCRClickText("OK", time.Second) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				if req.GetType() == "ocr" {
					return &controlpb.ControlResponse{Success: true, Data: "ok"}
				}
				return &controlpb.ControlResponse{Success: true}
			})
			c := New(sock)
			c.SetTimeout(123 * time.Millisecond)
			if err := tt.run(c); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := c.Timeout(); got != 123*time.Millisecond {
				t.Fatalf("timeout = %v, want restored 123ms", got)
			}
		})
	}
}

func serveControlClientTest(t *testing.T, fn func(*controlpb.ControlRequest) *controlpb.ControlResponse) string {
	t.Helper()
	dir := t.TempDir()
	link := filepath.Join("/tmp", fmt.Sprintf("controlclient-%d", time.Now().UnixNano()))
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	sock := filepath.Join(link, "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveControlClientConn(conn, fn)
		}
	}()
	return sock
}

func serveControlClientConn(conn net.Conn, fn func(*controlpb.ControlRequest) *controlpb.ControlResponse) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req controlpb.ControlRequest
	if err := control.ProtoJSONUnmarshaler.Unmarshal(line, &req); err != nil {
		fmt.Fprintf(conn, `{"success":false,"error":%q}`+"\n", err.Error())
		return
	}
	resp, err := control.ProtoJSONMarshaler.Marshal(fn(&req))
	if err != nil {
		return
	}
	_, _ = conn.Write(append(resp, '\n'))
}
