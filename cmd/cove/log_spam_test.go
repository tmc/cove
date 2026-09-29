package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

type recordCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordCaptureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

func (h *recordCaptureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *recordCaptureHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *recordCaptureHandler) WithGroup(name string) slog.Handler       { return h }

func (h *recordCaptureHandler) findRecords(prefix string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if strings.Contains(r.Message, prefix) {
			out = append(out, r)
		}
	}
	return out
}

type failConn struct {
	net.Conn
	writeErr error
}

func (c *failConn) Write(b []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(b), nil
}

func (c *failConn) Close() error {
	return nil
}

func TestAgentRouteLogsAtDebug(t *testing.T) {
	handler := &recordCaptureHandler{}
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(prevLogger)

	var stdLogBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&stdLogBuf)
	defer log.SetOutput(prevLogWriter)

	cs := &ControlServer{}

	tests := []struct {
		name    string
		req     *controlpb.ControlRequest
		wantMsg string
	}{
		{
			name: "exec auto user route",
			req: &controlpb.ControlRequest{
				Type: "agent-exec-auto",
				Command: &controlpb.ControlRequest_AgentExec{
					AgentExec: &controlpb.AgentExecCommand{
						Args: []string{"ls", "-1", "/Volumes/My Shared Files"},
					},
				},
			},
			wantMsg: "agent-route: exec",
		},
		{
			name: "read tcc route",
			req: &controlpb.ControlRequest{
				Type: "agent-read",
				Command: &controlpb.ControlRequest_AgentRead{
					AgentRead: &controlpb.AgentFileReadCommand{
						Path: "/Users/test/Documents/file.txt",
					},
				},
			},
			wantMsg: "agent-route: read",
		},
		{
			name: "write tcc route",
			req: &controlpb.ControlRequest{
				Type: "agent-write",
				Command: &controlpb.ControlRequest_AgentWrite{
					AgentWrite: &controlpb.AgentFileWriteCommand{
						Path: "/Users/test/Documents/file.txt",
						Data: "dGVzdA==",
					},
				},
			},
			wantMsg: "agent-route: write",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler.mu.Lock()
			handler.records = nil
			handler.mu.Unlock()
			stdLogBuf.Reset()

			_, _ = cs.handleAgentCommand(tt.req)

			if strings.Contains(stdLogBuf.String(), tt.wantMsg) {
				t.Fatalf("agent-route message spammed to standard logger: %s", stdLogBuf.String())
			}

			recs := handler.findRecords(tt.wantMsg)
			if len(recs) == 0 {
				t.Fatalf("no slog record found matching %q", tt.wantMsg)
			}
			for _, r := range recs {
				if r.Level != slog.LevelDebug {
					t.Fatalf("agent-route record level = %v, want %v", r.Level, slog.LevelDebug)
				}
			}
		})
	}
}

func TestAgentVersionMismatchLoggedOnce(t *testing.T) {
	var stdLogBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&stdLogBuf)
	defer log.SetOutput(prevLogWriter)

	cs := &ControlServer{}
	guestVer := "v0.0.1"

	// First call should log the mismatch warning and the upgrade hint.
	cs.MaybeAutoUpgradeAgent(guestVer, nil)
	firstLog := stdLogBuf.String()
	if !strings.Contains(firstLog, "agent-health: version mismatch") {
		t.Fatalf("expected version mismatch log on first call, got: %s", firstLog)
	}
	if !strings.Contains(firstLog, "run 'cove agent-upgrade'") {
		t.Fatalf("expected upgrade hint on first call, got: %s", firstLog)
	}

	// Repeated reconnects with the same version should not re-log.
	for i := 0; i < 5; i++ {
		cs.MaybeAutoUpgradeAgent(guestVer, nil)
	}

	if count := strings.Count(stdLogBuf.String(), "agent-health: version mismatch"); count != 1 {
		t.Fatalf("version mismatch logged %d times, want 1", count)
	}
	if count := strings.Count(stdLogBuf.String(), "run 'cove agent-upgrade'"); count != 1 {
		t.Fatalf("upgrade hint logged %d times, want 1", count)
	}

	// A different guest version on the same session should be logged once.
	cs.MaybeAutoUpgradeAgent("v0.0.2", nil)
	if count := strings.Count(stdLogBuf.String(), "agent-health: version mismatch"); count != 2 {
		t.Fatalf("distinct version mismatch logged %d times total, want 2", count)
	}

	// A new session (different ControlServer) should log once for its own session.
	cs2 := &ControlServer{}
	cs2.MaybeAutoUpgradeAgent(guestVer, nil)
	if count := strings.Count(stdLogBuf.String(), "agent-health: version mismatch"); count != 3 {
		t.Fatalf("new session version mismatch logged %d times total, want 3", count)
	}
}

func TestWriteResponseLogsClientDisconnectAtDebug(t *testing.T) {
	handler := &recordCaptureHandler{}
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(prevLogger)

	tests := []struct {
		name      string
		conn      net.Conn
		wantDebug bool
		wantError bool
	}{
		{
			name: "net pipe closed peer",
			conn: func() net.Conn {
				srv, cli := net.Pipe()
				_ = cli.Close()
				return srv
			}(),
			wantDebug: true,
			wantError: false,
		},
		{
			name:      "broken pipe error",
			conn:      &failConn{writeErr: errors.New("write unix: broken pipe")},
			wantDebug: true,
			wantError: false,
		},
		{
			name:      "connection reset error",
			conn:      &failConn{writeErr: errors.New("write: connection reset by peer")},
			wantDebug: true,
			wantError: false,
		},
		{
			name:      "unrelated write error",
			conn:      &failConn{writeErr: errors.New("disk quota exceeded")},
			wantDebug: false,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler.mu.Lock()
			handler.records = nil
			handler.mu.Unlock()

			err := writeResponse(tt.conn, &controlpb.ControlResponse{Success: true})
			if err == nil {
				t.Fatal("writeResponse returned nil error, want error")
			}
			_ = tt.conn.Close()

			recs := handler.findRecords("control socket: write response")
			if len(recs) == 0 {
				t.Fatal("no write response log records found")
			}

			var hasDebug, hasError bool
			for _, r := range recs {
				if r.Level == slog.LevelDebug {
					hasDebug = true
				}
				if r.Level >= slog.LevelError {
					hasError = true
				}
			}

			if hasDebug != tt.wantDebug {
				t.Fatalf("hasDebug = %v, want %v", hasDebug, tt.wantDebug)
			}
			if hasError != tt.wantError {
				t.Fatalf("hasError = %v, want %v", hasError, tt.wantError)
			}
		})
	}
}
