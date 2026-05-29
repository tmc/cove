package guibench_test

import (
	"fmt"
	"strings"

	"github.com/tmc/cove/internal/guibench"
)

// ExampleTask_Provenance parses the adapter citation an adapted task records in
// its Source: the upstream benchmark, the verbatim upstream task id, and the
// adaptation mode (port = genuinely cross-platform; intent = foreign-app intent
// re-expressed against an Apple-native app).
func ExampleTask_Provenance() {
	const taskJSON = `{
		"id": "safari-do-not-track",
		"image": "macos-base:v1",
		"domain": "Safari",
		"instruction": "Enable Do Not Track in Safari.",
		"source": "adapted:osworld:030eeff7-b492-4218-b312-701ec99ee0cc mode=port; Chrome DNT toggle maps to Safari defaults",
		"evaluator": {
			"func": "plist_equals",
			"result": {"kind": "defaults", "domain": "com.apple.Safari", "key": "SendDoNotTrackHTTPHeader"}
		}
	}`

	task, err := guibench.Decode(strings.NewReader(taskJSON))
	if err != nil {
		fmt.Println("decode:", err)
		return
	}
	p, err := task.Provenance()
	if err != nil {
		fmt.Println("provenance:", err)
		return
	}
	fmt.Println("adapted:", task.IsAdapted())
	fmt.Println("benchmark:", p.Benchmark)
	fmt.Println("upstream:", p.UpstreamID)
	fmt.Println("mode:", p.Mode)
	// Output:
	// adapted: true
	// benchmark: osworld
	// upstream: 030eeff7-b492-4218-b312-701ec99ee0cc
	// mode: port
}
