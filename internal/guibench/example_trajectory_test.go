package guibench_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/guibench"
)

// ExampleWriteDataset writes a one-trajectory dataset and reads back its
// dataset_info.json, showing the HuggingFace-loadable layout (metadata.jsonl +
// trajectories.jsonl + dataset_info.json). It needs no VM.
func ExampleWriteDataset() {
	dir, _ := os.MkdirTemp("", "guibench-traj-")
	defer os.RemoveAll(dir)

	traj := &guibench.Trajectory{
		TrajectoryID: "oracle-finder-create-folder-1",
		TaskID:       "finder-create-folder",
		Domain:       "Finder",
		Instruction:  "Create a folder named Reports on the Desktop",
		Provider:     guibench.SourceOracle,
		Source:       guibench.SourceOracle,
		Seed:         1,
		Reward:       1,
		Steps: []guibench.TrajectoryStep{
			{Index: 0, Action: "mkdir ~/Desktop/Reports", Observation: "empty Desktop"},
		},
	}
	if err := guibench.WriteDataset(dir, []*guibench.Trajectory{traj}, nil, guibench.VerifierVersion()); err != nil {
		fmt.Println("error:", err)
		return
	}

	data, _ := os.ReadFile(filepath.Join(dir, "dataset_info.json"))
	var info guibench.DatasetInfo
	_ = json.Unmarshal(data, &info)
	fmt.Printf("trajectories=%d steps=%d builder=%s\n", info.TrajectoryN, info.StepN, info.Builder)
	// Output: trajectories=1 steps=1 builder=guibench-trajectory
}
