package controlclient

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestClientSnapshotList(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		want string
		err  string
	}{
		{
			name: "typed",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_SnapshotList{
				SnapshotList: &controlpb.SnapshotListResponse{Snapshots: []*controlpb.SnapshotInfo{{Name: "clean"}}},
			}},
			want: "clean",
		},
		{
			name: "data fallback",
			resp: &controlpb.ControlResponse{Success: true, Data: `{"snapshots":[{"name":"legacy"}]}`},
			want: "legacy",
		},
		{
			name: "server error",
			resp: &controlpb.ControlResponse{Success: false, Error: "offline"},
			err:  "snapshot list: offline",
		},
		{
			name: "bad json",
			resp: &controlpb.ControlResponse{Success: true, Data: "{"},
			err:  "parse snapshot list",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := New(sock).SnapshotList()
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
				t.Fatalf("SnapshotList: %v", err)
			}
			if got.GetSnapshots()[0].GetName() != tt.want {
				t.Fatalf("snapshot = %q, want %q", got.GetSnapshots()[0].GetName(), tt.want)
			}
			req := <-reqc
			if req.GetType() != "snapshot" || req.GetSnapshot().GetAction() != "list" {
				t.Fatalf("snapshot request = %+v", req)
			}
		})
	}
}

func TestClientSnapshotActions(t *testing.T) {
	tests := []struct {
		name   string
		run    func(*Client) (string, error)
		action string
		resp   *controlpb.ControlResponse
		want   string
	}{
		{
			name:   "restore typed",
			run:    func(c *Client) (string, error) { return c.SnapshotRestore("clean") },
			action: "restore",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_SnapshotAction{
				SnapshotAction: &controlpb.SnapshotActionResponse{Message: "restored"},
			}},
			want: "restored",
		},
		{
			name:   "delete fallback",
			run:    func(c *Client) (string, error) { return c.SnapshotDelete("clean") },
			action: "delete",
			resp:   &controlpb.ControlResponse{Success: true, Data: "deleted"},
			want:   "deleted",
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
				t.Fatalf("message = %q, want %q", got, tt.want)
			}
			snap := (<-reqc).GetSnapshot()
			if snap.GetAction() != tt.action || snap.GetName() != "clean" {
				t.Fatalf("snapshot command = %+v", snap)
			}
		})
	}
}
