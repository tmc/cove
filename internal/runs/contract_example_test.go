package runs_test

import (
	"fmt"
	"path/filepath"

	"github.com/tmc/cove/internal/runs"
)

func ExampleLoadRecord() {
	record, err := runs.LoadRecord(filepath.Join("testdata", "task-run"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(record.Manifest.Outcome, record.Incomplete)
	fmt.Println(record.Events[0].StepID)
	// Output:
	// task_failure false
	// build
}

func ExampleValidateTask() {
	err := runs.ValidateTask(runs.Task{Kind: "go-workspace", Inputs: []runs.Input{{Name: "API_TOKEN", Secret: true}}})
	fmt.Println(err)
	// Output: <nil>
}

func ExampleValidateArtifact() {
	err := runs.ValidateArtifact(runs.Artifact{Name: "AX", Status: "unavailable", Reason: "permission denied"})
	fmt.Println(err)
	// Output: <nil>
}

func ExampleValidOutcome() {
	fmt.Println(runs.ValidOutcome("canceled"))
	fmt.Println(runs.ValidOutcome("unknown"))
	// Output:
	// true
	// false
}
