package runs

import (
	"fmt"
	"os"
)

func ExampleInspectRecord() {
	inspection := InspectRecord(Record{Manifest: Manifest{RunID: "task", Outcome: "task_failure", PrimaryError: "build failed"}})
	fmt.Println(inspection.Outcome, inspection.PrimaryError)
	// Output: task_failure build failed
}

func ExampleRenderInspection() {
	RenderInspection(os.Stdout, Inspection{RunID: "task", Outcome: "canceled"}, "text")
	// Output:
	// Run: task attempt=
	// Outcome: canceled incomplete=false truncated_events=false
	// Guest:  (recorded identity; current existence unverified)
	// Declared retention:  route= backend=
	// Primary failure:
}

func ExampleCompareRecords() {
	comparison := CompareRecords(Record{}, Record{})
	fmt.Println(comparison.Comparable, comparison.Reasons[0])
	// Output: false task provenance unavailable
}
