package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/runs"
)

func TestAgentSandboxTaskProvenance(t *testing.T) {
	for _, image := range []string{"image:mutable", "image@sha256:" + strings.Repeat("a", 64)} {
		t.Run(image, func(t *testing.T) {
			root := t.TempDir()
			bundle, err := NewRunBundle(root, "vm", image)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("OPENAI_API_KEY", "credential-secret-never-record")
			opts := agentSandboxRunOptions{provider: "openai", image: image, task: "prompt-secret-never-record"}
			if err := configureAgentSandboxBundle(bundle, opts); err != nil {
				t.Fatal(err)
			}
			if err := bundle.AppendEvent(map[string]any{"event": "agent_sandbox.start"}); err != nil {
				t.Fatal(err)
			}
			finishAgentSandboxBundle(bundle, errors.New("primary provider failure"))
			record, err := runs.LoadRecord(bundle.Dir())
			if err != nil {
				t.Fatal(err)
			}
			task := record.Manifest.Task
			if task == nil || task.Kind != "agent_sandbox" || task.Provider != "openai" || task.GuestRoute != "control-socket" || task.Retention != "discard-on-stop" {
				t.Fatalf("task=%+v", task)
			}
			if strings.Contains(image, "@") && task.ImageDigest == "" {
				t.Fatal("explicit digest missing")
			}
			if !strings.Contains(image, "@") && task.ImageDigest != "" {
				t.Fatal("tag inferred as digest")
			}
			if len(task.Inputs) != 1 || task.Inputs[0].Name != "OPENAI_API_KEY" || !task.Inputs[0].Secret || task.Inputs[0].Digest != "" {
				t.Fatalf("inputs=%+v", task.Inputs)
			}
			data, err := os.ReadFile(filepath.Join(bundle.Dir(), "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "never-record") {
				t.Fatal("secret value recorded")
			}
			if record.Manifest.PrimaryError != "primary provider failure" || record.Manifest.Outcome != "task_failure" {
				t.Fatalf("manifest=%+v", record.Manifest)
			}
			if _, err := json.Marshal(record); err != nil {
				t.Fatal(err)
			}
		})
	}
}
