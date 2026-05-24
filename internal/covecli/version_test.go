package covecli

import (
	"bytes"
	"testing"
)

func TestPrintVersion(t *testing.T) {
	var out bytes.Buffer

	PrintVersion(&out, "cove dev unknown unknown")

	if got := out.String(); got != "cove dev unknown unknown\n" {
		t.Fatalf("output = %q, want version line", got)
	}
}
