package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceCaptureWithoutAgent(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"state":"running","pid":2147483647,"updated_at":"2026-10-01T00:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	capture, err := captureWorkspaceState(context.Background(), workspacePlan{VM: "retained"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(capture, &state); err != nil {
		t.Fatal(err)
	}
	var lifecycle map[string]any
	if err := json.Unmarshal(state["lifecycle"], &lifecycle); err != nil {
		t.Fatal(err)
	}
	if string(state["agentObservation"]) != `"unavailable"` || lifecycle["pid_presence"] != "absent" || lifecycle["reported_state"] != "running" || lifecycle["owner_identity"] != "unverified" {
		t.Fatalf("capture = %s", capture)
	}
}

func TestWorkspaceLifecycleMetadataBounds(t *testing.T) {
	for _, tt := range []struct {
		name string
		data string
		want string
	}{
		{"invalid", "not json", "unavailable"},
		{"oversized", strings.Repeat(" ", 65<<10), "unavailable"},
		{"negative pid", `{"pid":-1,"state":"running"}`, "present"},
		{"out of range pid", `{"pid":2147483648,"state":"running"}`, "present"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			observation := workspaceLifecycleObservation("retained", dir)
			if observation["runtime_metadata"] != tt.want || observation["pid_presence"] != "unknown" || observation["owner_identity"] != "unverified" {
				t.Fatalf("observation = %v", observation)
			}
		})
	}
}

func TestWorkspaceLifecycleDiagnosticPointer(t *testing.T) {
	for _, tt := range []struct {
		name string
		data string
		want bool
	}{
		{"latest launch", `{"directory":"workspace-runtime-123"}`, true},
		{"traversal", `{"directory":"../workspace-runtime-123"}`, false},
		{"absolute", `{"directory":"/workspace-runtime-123"}`, false},
		{"unrelated", `{"directory":"disk.img"}`, false},
		{"oversized", strings.Repeat(" ", 1025), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "workspace-runtime-diagnostics.json"), []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			state := workspaceLifecycleObservation("retained", dir)
			_, got := state["runtime_diagnostics_directory"]
			if got != tt.want || state["owner_identity"] != "unverified" {
				t.Fatalf("observation = %v", state)
			}
		})
	}
}
