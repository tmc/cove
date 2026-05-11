package controlclient

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestClientMemoryInfo(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		want float64
		err  string
	}{
		{
			name: "typed",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_MemoryInfo{
				MemoryInfo: &controlpb.MemoryInfoResponse{Info: &controlpb.MemoryInfo{TargetGb: 8}},
			}},
			want: 8,
		},
		{
			name: "data fallback",
			resp: &controlpb.ControlResponse{Success: true, Data: `{"info":{"target_gb":12}}`},
			want: 12,
		},
		{
			name: "server error",
			resp: &controlpb.ControlResponse{Success: false, Error: "offline"},
			err:  "memory info: offline",
		},
		{
			name: "bad json",
			resp: &controlpb.ControlResponse{Success: true, Data: "{"},
			err:  "parse memory info",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := New(sock).MemoryInfo()
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want substring %q", err, tt.err)
				}
				var syntax *json.SyntaxError
				if tt.name == "bad json" && !errors.As(err, &syntax) {
					t.Fatalf("err = %v, want json syntax error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("MemoryInfo: %v", err)
			}
			if got.GetInfo().GetTargetGb() != tt.want {
				t.Fatalf("target gb = %v, want %v", got.GetInfo().GetTargetGb(), tt.want)
			}
			req := <-reqc
			if req.GetType() != "memory" || req.GetMemory().GetAction() != "info" {
				t.Fatalf("memory request = %+v", req)
			}
		})
	}
}

func TestClientMemorySet(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		want string
	}{
		{
			name: "message response",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Message{
				Message: &controlpb.MessageResponse{Message: "updated"},
			}},
			want: "updated",
		},
		{
			name: "data fallback",
			resp: &controlpb.ControlResponse{Success: true, Data: "legacy"},
			want: "legacy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := New(sock).MemorySet(6.5)
			if err != nil {
				t.Fatalf("MemorySet: %v", err)
			}
			if got != tt.want {
				t.Fatalf("message = %q, want %q", got, tt.want)
			}
			mem := (<-reqc).GetMemory()
			if mem.GetAction() != "set" || mem.GetSizeGb() != 6.5 {
				t.Fatalf("memory command = %+v", mem)
			}
		})
	}
}
