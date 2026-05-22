package controlclient

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestClientSharedFoldersApply(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		want string
		err  string
	}{
		{
			name: "message response",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "applied"}},
			},
			want: "applied",
		},
		{
			name: "data fallback",
			resp: &controlpb.ControlResponse{Success: true, Data: "legacy"},
			want: "legacy",
		},
		{
			name: "server error",
			resp: &controlpb.ControlResponse{Success: false, Error: "no config"},
			err:  "shared-folders-apply: no config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := New(sock).SharedFoldersApply()
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want substring %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SharedFoldersApply: %v", err)
			}
			if got != tt.want {
				t.Fatalf("message = %q, want %q", got, tt.want)
			}
			if req := <-reqc; req.GetType() != "shared-folders-apply" {
				t.Fatalf("type = %q, want shared-folders-apply", req.GetType())
			}
		})
	}
}

func TestClientSharedFoldersRuntimeStatus(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		want SharedFoldersRuntimeStatus
		err  string
	}{
		{
			name: "json status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"running":true,"virtiofs":true,"state":"ready","message":"mounted"}`,
			},
			want: SharedFoldersRuntimeStatus{Running: true, VirtioFS: true, State: "ready", Message: "mounted"},
		},
		{
			name: "message fallback",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "not running"}},
			},
			want: SharedFoldersRuntimeStatus{Message: "not running"},
		},
		{
			name: "server error",
			resp: &controlpb.ControlResponse{Success: false, Error: "offline"},
			err:  "shared-folders-runtime-status: offline",
		},
		{
			name: "bad json",
			resp: &controlpb.ControlResponse{Success: true, Data: "{"},
			err:  "parse shared-folders-runtime-status",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := New(sock).SharedFoldersRuntimeStatus()
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
				t.Fatalf("SharedFoldersRuntimeStatus: %v", err)
			}
			if got != tt.want {
				t.Fatalf("status = %+v, want %+v", got, tt.want)
			}
			if req := <-reqc; req.GetType() != "shared-folders-runtime-status" {
				t.Fatalf("type = %q, want shared-folders-runtime-status", req.GetType())
			}
		})
	}
}
