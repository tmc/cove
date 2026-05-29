package guibench

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeTrajectory builds an N-step synthetic trajectory for serialization tests.
func fakeTrajectory(id string, n int, withShots bool) (*Trajectory, map[string][]byte) {
	shots := make(map[string][]byte)
	t := &Trajectory{
		TrajectoryID: id,
		TaskID:       "finder-create-folder",
		Domain:       "Finder",
		Instruction:  "Create a folder named Reports",
		Provider:     SourceOracle,
		Source:       SourceOracle,
		Seed:         1,
		Reward:       1,
	}
	for i := 0; i < n; i++ {
		step := TrajectoryStep{
			Index:       i,
			Action:      "mkdir Reports",
			Observation: "Desktop is empty",
		}
		if withShots {
			step.Screenshot = shotName(id, i)
			shots[step.Screenshot] = []byte("\x89PNG\r\n\x1a\n" + id) // synthetic, not a real PNG
		}
		t.Steps = append(t.Steps, step)
	}
	return t, shots
}

func readJSONL[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatalf("unmarshal line in %s: %v", path, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

func TestWriteDatasetSchema(t *testing.T) {
	dir := t.TempDir()
	traj, shots := fakeTrajectory("oracle-finder-create-folder-1", 3, true)
	if err := WriteDataset(dir, []*Trajectory{traj}, shots, VerifierVersion()); err != nil {
		t.Fatalf("WriteDataset: %v", err)
	}

	// trajectories.jsonl round-trips to an equal trajectory.
	got := readJSONL[Trajectory](t, filepath.Join(dir, "trajectories.jsonl"))
	if len(got) != 1 {
		t.Fatalf("trajectories.jsonl: got %d trajectories, want 1", len(got))
	}
	if got[0].TrajectoryID != traj.TrajectoryID || len(got[0].Steps) != 3 {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
	if err := got[0].Validate(); err != nil {
		t.Errorf("round-tripped trajectory invalid: %v", err)
	}

	// metadata.jsonl has one row per step, each carrying the HF file_name.
	rows := readJSONL[stepRow](t, filepath.Join(dir, "metadata.jsonl"))
	if len(rows) != 3 {
		t.Fatalf("metadata.jsonl: got %d rows, want 3", len(rows))
	}
	for i, r := range rows {
		if r.StepIndex != i {
			t.Errorf("row %d: step_index=%d", i, r.StepIndex)
		}
		if r.FileName != shotName(traj.TrajectoryID, i) {
			t.Errorf("row %d: file_name=%q want %q", i, r.FileName, shotName(traj.TrajectoryID, i))
		}
		if r.Reward != 1 {
			t.Errorf("row %d: reward=%v want 1", i, r.Reward)
		}
		if r.Instruction != traj.Instruction {
			t.Errorf("row %d: instruction=%q", i, r.Instruction)
		}
	}

	// Each referenced screenshot was written under images/.
	for i := 0; i < 3; i++ {
		rel := shotName(traj.TrajectoryID, i)
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("screenshot %s not written: %v", rel, err)
		}
	}

	// dataset_info.json carries the schema + provenance.
	var info DatasetInfo
	data, err := os.ReadFile(filepath.Join(dir, "dataset_info.json"))
	if err != nil {
		t.Fatalf("read dataset_info.json: %v", err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal dataset_info.json: %v", err)
	}
	if info.TrajectoryN != 1 || info.StepN != 3 || info.ScreenshotN != 3 {
		t.Errorf("dataset_info counts: trajs=%d steps=%d shots=%d", info.TrajectoryN, info.StepN, info.ScreenshotN)
	}
	if info.VerifierHash != VerifierVersion() {
		t.Errorf("dataset_info verifier hash=%q want %q", info.VerifierHash, VerifierVersion())
	}
	if f, ok := info.Features["file_name"]; !ok || f.Dtype != "image" {
		t.Errorf("dataset_info file_name feature=%+v want image", info.Features["file_name"])
	}
	if len(info.Sources) != 1 || info.Sources[0] != SourceOracle {
		t.Errorf("dataset_info sources=%v want [oracle]", info.Sources)
	}
}

func TestWriteDatasetTextOnlyNoImagesDir(t *testing.T) {
	dir := t.TempDir()
	traj, _ := fakeTrajectory("oracle-text-only-1", 2, false)
	if err := WriteDataset(dir, []*Trajectory{traj}, nil, VerifierVersion()); err != nil {
		t.Fatalf("WriteDataset: %v", err)
	}
	// No step carries a screenshot, so images/ must not be created.
	if _, err := os.Stat(filepath.Join(dir, "images")); !os.IsNotExist(err) {
		t.Errorf("images/ should not exist for a text-only dataset, stat err=%v", err)
	}
	rows := readJSONL[stepRow](t, filepath.Join(dir, "metadata.jsonl"))
	for i, r := range rows {
		if r.FileName != "" {
			t.Errorf("row %d: file_name=%q want empty", i, r.FileName)
		}
	}
}

func TestWriteDatasetMissingScreenshotErrors(t *testing.T) {
	dir := t.TempDir()
	traj, _ := fakeTrajectory("oracle-missing-1", 1, true) // references a shot...
	// ...but we pass an empty screenshot map, so WriteDataset must error rather
	// than emit a dangling file_name.
	if err := WriteDataset(dir, []*Trajectory{traj}, nil, VerifierVersion()); err == nil {
		t.Fatal("WriteDataset: want error for missing screenshot, got nil")
	}
}

func TestTrajectoryValidate(t *testing.T) {
	tests := []struct {
		name    string
		traj    Trajectory
		wantErr bool
	}{
		{
			name:    "ok",
			traj:    Trajectory{TaskID: "t", Instruction: "do", Source: SourceOracle, Reward: 1, Steps: []TrajectoryStep{{Index: 0, Action: "a"}}},
			wantErr: false,
		},
		{name: "no task id", traj: Trajectory{Instruction: "do", Source: SourceOracle}, wantErr: true},
		{name: "no instruction", traj: Trajectory{TaskID: "t", Source: SourceOracle}, wantErr: true},
		{name: "bad source", traj: Trajectory{TaskID: "t", Instruction: "do", Source: "guess"}, wantErr: true},
		{name: "reward over 1", traj: Trajectory{TaskID: "t", Instruction: "do", Source: SourceScored, Reward: 1.5}, wantErr: true},
		{name: "reward under 0", traj: Trajectory{TaskID: "t", Instruction: "do", Source: SourceScored, Reward: -0.1}, wantErr: true},
		{
			name:    "step index mismatch",
			traj:    Trajectory{TaskID: "t", Instruction: "do", Source: SourceScored, Steps: []TrajectoryStep{{Index: 1, Action: "a"}}},
			wantErr: true,
		},
		{
			name:    "empty action",
			traj:    Trajectory{TaskID: "t", Instruction: "do", Source: SourceScored, Steps: []TrajectoryStep{{Index: 0}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.traj.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestSafeSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"oracle-finder-1", "oracle-finder-1"},
		{"a/b c", "a_b_c"},
		{"with.dots", "with_dots"},
	}
	for _, tt := range tests {
		if got := safeSlug(tt.in); got != tt.want {
			t.Errorf("safeSlug(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
	// An all-punctuation id falls back to a non-empty hash slug.
	if got := safeSlug("///"); got == "" {
		t.Error("safeSlug(\"///\") should not be empty")
	}
}
