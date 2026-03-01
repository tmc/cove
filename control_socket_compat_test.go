package main

import (
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestPopulateLegacyRequestPayloads_ScreenshotFlatFields(t *testing.T) {
	req := &controlpb.ControlRequest{Type: "screenshot"}
	line := `{"type":"screenshot","scale":0.75,"quality":80,"format":"png","diff":true}`
	populateLegacyRequestPayloads(line, req)

	cmd := req.GetScreenshot()
	if cmd == nil {
		t.Fatalf("expected screenshot payload to be populated")
	}
	if cmd.Scale != 0.75 {
		t.Fatalf("scale = %v, want 0.75", cmd.Scale)
	}
	if cmd.Quality != 80 {
		t.Fatalf("quality = %v, want 80", cmd.Quality)
	}
	if cmd.Format != "png" {
		t.Fatalf("format = %q, want png", cmd.Format)
	}
	if !cmd.Diff {
		t.Fatalf("diff = false, want true")
	}
}

func TestPopulateLegacyRequestPayloads_SnapshotDataEnvelope(t *testing.T) {
	req := &controlpb.ControlRequest{Type: "snapshot"}
	line := `{"type":"snapshot","data":{"action":"save","name":"checkpoint-1"}}`
	populateLegacyRequestPayloads(line, req)

	cmd := req.GetSnapshot()
	if cmd == nil {
		t.Fatalf("expected snapshot payload to be populated")
	}
	if cmd.Action != "save" {
		t.Fatalf("action = %q, want save", cmd.Action)
	}
	if cmd.Name != "checkpoint-1" {
		t.Fatalf("name = %q, want checkpoint-1", cmd.Name)
	}
}

func TestPopulateLegacyRequestPayloads_AgentExecFlatFields(t *testing.T) {
	req := &controlpb.ControlRequest{Type: "agent-exec-stream"}
	line := `{"type":"agent-exec-stream","args":["echo","hello"],"working_dir":"/tmp"}`
	populateLegacyRequestPayloads(line, req)

	cmd := req.GetAgentExec()
	if cmd == nil {
		t.Fatalf("expected agent-exec payload to be populated")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "echo" || cmd.Args[1] != "hello" {
		t.Fatalf("args = %#v, want [echo hello]", cmd.Args)
	}
	if cmd.WorkingDir != "/tmp" {
		t.Fatalf("working_dir = %q, want /tmp", cmd.WorkingDir)
	}
}

func TestPopulateLegacyRequestPayloads_LegacyTokenField(t *testing.T) {
	req := &controlpb.ControlRequest{Type: "ping"}
	line := `{"type":"ping","token":"legacy-token"}`
	populateLegacyRequestPayloads(line, req)
	if req.AuthToken != "legacy-token" {
		t.Fatalf("auth_token = %q, want legacy-token", req.AuthToken)
	}
}
