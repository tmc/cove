package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/runs"
)

func TestRunBundleTaskWriterReader(t *testing.T) {
	for _, name := range []string{"success", "failure", "canceled", "timed_out", "cleanup", "prerequisite_failure", "failure_cleanup"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			bundle, err := NewRunBundle(root, "vm", "image:mutable")
			if err != nil {
				t.Fatal(err)
			}
			task := runs.Task{Kind: "recipe", GuestRoute: "user", Inputs: []runs.Input{{Name: "TOKEN", Secret: true}}, Retention: "retain"}
			if err := bundle.ConfigureTask(task); err != nil {
				t.Fatal(err)
			}
			task.Inputs[0].Name = "mutated"
			if err := bundle.AppendTaskEvent(runs.TaskEvent{Kind: "step_started", StepID: "build"}); err != nil {
				t.Fatal(err)
			}
			interrupted, err := runs.LoadRecord(bundle.Dir())
			if err != nil || !interrupted.Incomplete || interrupted.Manifest.Outcome != "interrupted" {
				t.Fatalf("interrupted=%+v error=%v", interrupted, err)
			}
			if err := bundle.RecordArtifact(runs.Artifact{Name: "screenshot", Status: "present", Path: "artifacts/screenshot.png", ContentType: "image/png"}, []byte("capture")); err != nil {
				t.Fatal(err)
			}
			if err := bundle.RecordArtifact(runs.Artifact{Name: "AX", Status: "unavailable", Reason: "no user session"}, nil); err != nil {
				t.Fatal(err)
			}
			if err := bundle.RecordArtifact(runs.Artifact{Name: "OCR", Status: "failed", Reason: "capture timed out"}, nil); err != nil {
				t.Fatal(err)
			}
			if err := bundle.AppendTaskEvent(runs.TaskEvent{Kind: "step_completed", StepID: "build"}); err != nil {
				t.Fatal(err)
			}
			var primary error
			switch name {
			case "failure", "prerequisite_failure", "failure_cleanup":
				primary = errors.New("compile failed")
			case "canceled":
				primary = context.Canceled
			case "timed_out":
				primary = context.DeadlineExceeded
			}
			want := name
			switch name {
			case "failure":
				want = "task_failure"
			case "prerequisite_failure":
				err = bundle.FinalizeTask("prerequisite_failure", primary, nil)
			case "failure_cleanup":
				want = "task_failure"
				err = bundle.FinalizeTask("task_failure", primary, []string{"retain failed"})
			case "cleanup":
				want = "cleanup_incomplete"
				err = bundle.FinalizeTask("success", nil, []string{"retain failed"})
			}
			if name != "cleanup" && name != "prerequisite_failure" && name != "failure_cleanup" {
				err = bundle.Finalize(primary)
			}
			if err != nil {
				t.Fatal(err)
			}
			show, err := runs.LoadShow(root, bundle.ID())
			if err != nil {
				t.Fatal(err)
			}
			record := show.TaskRecord
			if record == nil || record.Incomplete || record.Manifest.Outcome != want {
				t.Fatalf("record = %+v", record)
			}
			if record.Manifest.Task.Inputs[0].Name != "TOKEN" {
				t.Fatal("task provenance mutated")
			}
			if len(record.Events) != 2 || record.Events[0].Sequence != 1 || record.Events[1].Sequence != 2 {
				t.Fatalf("events=%+v", record.Events)
			}
			if len(record.Manifest.CaptureErrors) != 1 || len(record.Manifest.Artifacts) != 3 {
				t.Fatalf("artifacts=%+v", record.Manifest)
			}
			if primary != nil && record.Manifest.PrimaryError != primary.Error() {
				t.Fatalf("primary error hidden: %+v", record.Manifest)
			}
			listed, err := runs.List(root, runs.Filter{Status: "all"})
			if err != nil || len(listed) != 1 {
				t.Fatalf("list=%+v error=%v", listed, err)
			}
		})
	}
}

func TestRunBundleTaskArtifactPathRefusal(t *testing.T) {
	bundle, err := NewRunBundle(t.TempDir(), "vm", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", "artifacts/../../outside", "/outside", "artifacts/x/../outside"} {
		if err := bundle.RecordArtifact(runs.Artifact{Name: path, Status: "present", Path: path}, []byte("secret")); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if err := bundle.AppendEvent(map[string]any{"event": "start"}); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(bundle.Dir(), "artifacts")); err != nil {
		t.Fatal(err)
	}
	if err := bundle.RecordArtifact(runs.Artifact{Name: "escape", Status: "present", Path: "artifacts/outside"}, []byte("secret")); err == nil {
		t.Fatal("symlink escape accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "outside")); !os.IsNotExist(err) {
		t.Fatalf("outside file created: %v", err)
	}
}

func TestRunBundleTaskFailedFinalizeRemainsIncomplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires nonroot filesystem permissions")
	}
	bundle, err := NewRunBundle(t.TempDir(), "vm", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.AppendEvent(map[string]any{"event": "start"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bundle.Dir(), 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(bundle.Dir(), 0700)
	if err := bundle.Finalize(errors.New("original task failure")); err == nil {
		t.Fatal("Finalize succeeded in read-only directory")
	}
	record, err := runs.LoadRecord(bundle.Dir())
	if err != nil || !record.Incomplete {
		t.Fatalf("record=%+v error=%v", record, err)
	}
	if err := os.Chmod(bundle.Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	record, err = runs.LoadRecord(bundle.Dir())
	if err != nil || record.Incomplete || record.Manifest.Outcome != "task_failure" || record.Manifest.PrimaryError != "original task failure" {
		t.Fatalf("retry record=%+v error=%v", record, err)
	}
}

func TestRunBundleTaskProvenanceBounds(t *testing.T) {
	bundle, err := NewRunBundle(t.TempDir(), "vm", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.ConfigureTask(runs.Task{Kind: "recipe", Inputs: []runs.Input{{Name: "secret", Secret: true, Digest: "sha256:" + strings.Repeat("a", 64)}}}); err == nil {
		t.Fatal("secret digest accepted")
	}
	event := map[string]any{"event": "start"}
	if err := bundle.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	if _, ok := event["schema_version"]; ok {
		t.Fatal("caller event mutated")
	}
	if err := bundle.ConfigureTask(runs.Task{Kind: "recipe"}); err == nil {
		t.Fatal("late task mutation accepted")
	}
}

func TestRunBundleTaskRefusesExistingDirectory(t *testing.T) {
	bundle, err := NewRunBundle(t.TempDir(), "vm", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bundle.Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(bundle.Dir(), "manifest.json")
	if err := os.WriteFile(sentinel, []byte("existing run"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := bundle.AppendEvent(map[string]any{"event": "start"}); err == nil {
		t.Fatal("existing bundle adopted")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "existing run" {
		t.Fatalf("existing bundle changed: %q %v", data, err)
	}
}
