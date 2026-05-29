package guibench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRunBundle creates a synthetic run bundle dir under t.TempDir(): a
// manifest.json, an events.jsonl whose agent.step lines carry action +
// observation + screenshot, and a screenshots/ dir with tiny synthetic
// captures. It mirrors the on-disk shape LoadTrace reads (see run_bundle.go in
// the main package). No committed images — the PNG bytes are synthetic.
func writeRunBundle(t *testing.T, runID string, steps []struct{ action, obs, shot string }) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), runID)
	if err := os.MkdirAll(filepath.Join(dir, "screenshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"run_id":"` + runID + `","vm_name":"bench-fork","fork_from":"macos-base","started_at":"2026-05-29T00:00:00Z","exit_status":"ok"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	lines := []string{`{"ts":"2026-05-29T00:00:00Z","event":"run.start","run_id":"` + runID + `"}`}
	for i, s := range steps {
		line := `{"ts":"2026-05-29T00:00:0` + string(rune('1'+i)) + `Z","event":"agent.step","action":"` + s.action + `","observation":"` + s.obs + `"`
		if s.shot != "" {
			line += `,"screenshot":"` + s.shot + `"`
			if err := os.WriteFile(filepath.Join(dir, "screenshots", s.shot), []byte("\x89PNG\r\n\x1a\nshot"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		line += "}"
		lines = append(lines, line)
	}
	lines = append(lines, `{"ts":"2026-05-29T00:00:09Z","event":"run.exit","exit_status":"ok"}`)
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTrajectoryFromBundle(t *testing.T) {
	dir := writeRunBundle(t, "deadbeef", []struct{ action, obs, shot string }{
		{"click Finder", "empty Desktop", "step0.png"},
		{"type Reports", "naming dialog", "step1.png"},
		{"press Enter", "folder created", "step2.png"},
	})
	traj, shots, err := TrajectoryFromBundle(BundleSpec{
		Dir:         dir,
		TaskID:      "finder-create-folder",
		Provider:    "anthropic",
		Domain:      "Finder",
		Instruction: "Create a folder named Reports",
		Seed:        7,
		Reward:      1,
	})
	if err != nil {
		t.Fatalf("TrajectoryFromBundle: %v", err)
	}
	if traj.Source != SourceScored {
		t.Errorf("source = %q want scored", traj.Source)
	}
	if traj.Reward != 1 || traj.Provider != "anthropic" || traj.Seed != 7 {
		t.Errorf("reward/provider/seed = %v/%q/%d", traj.Reward, traj.Provider, traj.Seed)
	}
	if traj.TrajectoryID != "deadbeef" {
		t.Errorf("trajectory id = %q want deadbeef (run id)", traj.TrajectoryID)
	}
	if len(traj.Steps) != 3 {
		t.Fatalf("steps = %d want 3 (one per agent.step event)", len(traj.Steps))
	}
	want := []struct{ action, obs string }{
		{"click Finder", "empty Desktop"},
		{"type Reports", "naming dialog"},
		{"press Enter", "folder created"},
	}
	for i, s := range traj.Steps {
		if s.Action != want[i].action || s.Observation != want[i].obs {
			t.Errorf("step %d = %q/%q want %q/%q", i, s.Action, s.Observation, want[i].action, want[i].obs)
		}
		if s.Screenshot == "" {
			t.Errorf("step %d has no screenshot", i)
		}
		if _, ok := shots[s.Screenshot]; !ok {
			t.Errorf("step %d screenshot %q not in shots map", i, s.Screenshot)
		}
	}
	if len(shots) != 3 {
		t.Errorf("shots = %d want 3", len(shots))
	}

	// The transformed trajectory writes a valid HF dataset.
	out := t.TempDir()
	if err := WriteDataset(out, []*Trajectory{traj}, shots, VerifierVersion()); err != nil {
		t.Fatalf("WriteDataset on bundle trajectory: %v", err)
	}
}

func TestTrajectoryFromBundleNoActions(t *testing.T) {
	dir := writeRunBundle(t, "nostep", nil)
	traj, _, err := TrajectoryFromBundle(BundleSpec{Dir: dir, TaskID: "t", Reward: 0})
	if err != nil {
		t.Fatalf("TrajectoryFromBundle: %v", err)
	}
	if len(traj.Steps) != 1 {
		t.Fatalf("steps = %d want 1 terminal record", len(traj.Steps))
	}
	if traj.Steps[0].Action == "" {
		t.Error("terminal step has empty action")
	}
	if traj.Instruction != "t" {
		t.Errorf("instruction fallback = %q want task id", traj.Instruction)
	}
}

func TestTrajectoryFromBundleValidation(t *testing.T) {
	dir := writeRunBundle(t, "valid", []struct{ action, obs, shot string }{{"click", "", ""}})
	tests := []struct {
		name string
		spec BundleSpec
	}{
		{"no dir", BundleSpec{TaskID: "t", Reward: 1}},
		{"no task id", BundleSpec{Dir: dir, Reward: 1}},
		{"reward over 1", BundleSpec{Dir: dir, TaskID: "t", Reward: 2}},
		{"reward under 0", BundleSpec{Dir: dir, TaskID: "t", Reward: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := TrajectoryFromBundle(tt.spec); err == nil {
				t.Errorf("TrajectoryFromBundle(%+v) = nil err, want error", tt.spec)
			}
		})
	}
}

func TestTrajectoryFromBundleMissingEvents(t *testing.T) {
	dir := t.TempDir() // no events.jsonl
	if _, _, err := TrajectoryFromBundle(BundleSpec{Dir: dir, TaskID: "t", Reward: 1}); err == nil {
		t.Fatal("want error for missing events.jsonl")
	}
}
