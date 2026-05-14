package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintManualElevationManifestLeadsWithUserPath(t *testing.T) {
	out := captureStderrString(t, func() {
		printManualElevationManifest([]byte(`{"op":"test"}`), "Do the privileged thing.")
	})
	for _, field := range strings.Fields(out) {
		if strings.HasPrefix(field, os.TempDir()+"/cove-elev-manifest-") {
			_ = os.Remove(field)
		}
	}
	for _, want := range []string{
		"Cannot show password dialog in this environment",
		"Open a normal Terminal window and rerun the cove command there.",
		"sudo cove helper install",
		"Advanced manual recovery",
		elevatedOpArg,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("manual elevation output missing %q:\n%s", want, out)
		}
	}
	helperAt := strings.Index(out, "sudo cove helper install")
	rawAt := strings.Index(out, elevatedOpArg)
	if rawAt < helperAt {
		t.Fatalf("raw elevated command appears before helper guidance:\n%s", out)
	}
}

func captureStderrString(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}
