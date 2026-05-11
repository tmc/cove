package controlclient

import (
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestClientTypedResponses(t *testing.T) {
	tests := []struct {
		name     string
		resp     *controlpb.ControlResponse
		run      func(*Client) (string, error)
		wantType string
		want     string
	}{
		{
			name: "status",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Status{
				Status: &controlpb.StatusResponse{State: "running"},
			}},
			run:      func(c *Client) (string, error) { s, err := c.Status(); return s.GetState(), err },
			wantType: "status",
			want:     "running",
		},
		{
			name: "agent exec",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentExecResult{
				AgentExecResult: &controlpb.AgentExecResponse{Stdout: "ok\n"},
			}},
			run: func(c *Client) (string, error) {
				s, err := c.AgentExecTypedTimeout([]string{"echo", "ok"}, map[string]string{"A": "B"}, "/tmp", time.Second)
				return s.GetStdout(), err
			},
			wantType: "agent-exec-auto",
			want:     "ok\n",
		},
		{
			name: "agent read",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentFile{
				AgentFile: &controlpb.AgentFileResponse{Data: []byte("file")},
			}},
			run:      func(c *Client) (string, error) { b, err := c.AgentReadFile("/tmp/file"); return string(b), err },
			wantType: "agent-read",
			want:     "file",
		},
		{
			name: "agent ping",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{
				AgentPing: &controlpb.AgentPingResponse{Version: "agent-v1"},
			}},
			run:      func(c *Client) (string, error) { return c.AgentPingTyped() },
			wantType: "agent-ping",
			want:     "agent-v1",
		},
		{
			name: "snapshot save",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_SnapshotAction{
				SnapshotAction: &controlpb.SnapshotActionResponse{Message: "saved"},
			}},
			run:      func(c *Client) (string, error) { return c.SnapshotSave("snap") },
			wantType: "snapshot",
			want:     "saved",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := tt.run(New(sock))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
			if req := <-reqc; req.GetType() != tt.wantType {
				t.Fatalf("type = %q, want %q", req.GetType(), tt.wantType)
			}
		})
	}
}

func TestClientLegacyResponseFallbacks(t *testing.T) {
	tests := []struct {
		name     string
		resp     *controlpb.ControlResponse
		run      func(*Client) (string, error)
		want     string
		wantErr  string
		wantType string
	}{
		{
			name:     "status data",
			resp:     &controlpb.ControlResponse{Success: true, Data: `{"state":"paused"}`},
			run:      func(c *Client) (string, error) { s, err := c.Status(); return s.GetState(), err },
			want:     "paused",
			wantType: "status",
		},
		{
			name:     "agent read base64",
			resp:     &controlpb.ControlResponse{Success: true, Data: "ZmlsZQ=="},
			run:      func(c *Client) (string, error) { b, err := c.AgentReadFile("/tmp/file"); return string(b), err },
			want:     "file",
			wantType: "agent-read",
		},
		{
			name: "status bad json",
			resp: &controlpb.ControlResponse{Success: true, Data: "{"},
			run: func(c *Client) (string, error) {
				s, err := c.Status()
				if s == nil {
					return "", err
				}
				return s.GetState(), err
			},
			wantErr:  "parse status",
			wantType: "status",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := tt.run(New(sock))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
			if req := <-reqc; req.GetType() != tt.wantType {
				t.Fatalf("type = %q, want %q", req.GetType(), tt.wantType)
			}
		})
	}
}
