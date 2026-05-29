package guibench_test

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tmc/cove/internal/guibench"
)

// ExampleTrace_RenderHTML shows that a parsed run bundle renders a
// self-contained local HTML timeline whose screenshots are referenced by
// relative path (no embedded bytes, no cloud dependency).
func ExampleTrace_RenderHTML() {
	tr := &guibench.Trace{
		RunID: "deadbeef",
		Steps: []guibench.TraceStep{
			{Index: 1, Event: "agent.step", Action: "click Finder", Observation: "folder created", Score: "1", Screenshot: "screenshots/step1.png"},
		},
		Screenshots: []string{"step1.png"},
	}
	var buf bytes.Buffer
	if err := tr.RenderHTML(&buf); err != nil {
		fmt.Println("error:", err)
		return
	}
	html := buf.String()
	fmt.Println(strings.Contains(html, "click Finder"))
	fmt.Println(strings.Contains(html, `src="screenshots/step1.png"`))
	fmt.Println(strings.Contains(html, "data:image")) // never inlines bytes
	// Output:
	// true
	// true
	// false
}
