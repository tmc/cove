package controlclient

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestClientMetadataResponses(t *testing.T) {
	tests := []struct {
		name     string
		resp     *controlpb.ControlResponse
		run      func(*Client) (string, error)
		wantType string
		want     string
	}{
		{
			name: "capabilities typed",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Capabilities{
				Capabilities: &controlpb.CapabilitiesResponse{ProtocolVersion: "v1"},
			}},
			run:      func(c *Client) (string, error) { r, err := c.Capabilities(); return r.GetProtocolVersion(), err },
			wantType: "capabilities",
			want:     "v1",
		},
		{
			name:     "network json",
			resp:     &controlpb.ControlResponse{Success: true, Data: `{"guest_ip":"192.0.2.2"}`},
			run:      func(c *Client) (string, error) { r, err := c.NetworkInfo(); return r.GetGuestIp(), err },
			wantType: "network",
			want:     "192.0.2.2",
		},
		{
			name: "agent info typed",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentInfo{
				AgentInfo: &controlpb.AgentInfoResponse{Hostname: "guest"},
			}},
			run:      func(c *Client) (string, error) { r, err := c.AgentInfo(); return r.GetHostname(), err },
			wantType: "agent-info",
			want:     "guest",
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

func TestClientMetadataErrors(t *testing.T) {
	tests := []struct {
		name    string
		resp    *controlpb.ControlResponse
		run     func(*Client) error
		want    string
		wantAs  bool
		reqType string
	}{
		{"capabilities server error", &controlpb.ControlResponse{Success: false, Error: "no"}, func(c *Client) error { _, err := c.Capabilities(); return err }, "capabilities: no", false, "capabilities"},
		{"network bad json", &controlpb.ControlResponse{Success: true, Data: "{"}, func(c *Client) error { _, err := c.NetworkInfo(); return err }, "parse network", true, "network"},
		{"agent info bad json", &controlpb.ControlResponse{Success: true, Data: "{"}, func(c *Client) error { _, err := c.AgentInfo(); return err }, "parse agent-info", true, "agent-info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			err := tt.run(New(sock))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want substring %q", err, tt.want)
			}
			var syntax *json.SyntaxError
			if tt.wantAs && !errors.As(err, &syntax) {
				t.Fatalf("err = %v, want json syntax error", err)
			}
			if req := <-reqc; req.GetType() != tt.reqType {
				t.Fatalf("type = %q, want %q", req.GetType(), tt.reqType)
			}
		})
	}
}
