package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkInjectSucceededForVM(t *testing.T) {
	t.Run("writes marker", func(t *testing.T) {
		dir := t.TempDir()
		target := vmSelection{Name: "test", Directory: dir}
		markInjectSucceededForVM(target)
		if _, err := os.Stat(filepath.Join(dir, ".inject-succeeded")); err != nil {
			t.Fatalf("marker missing: %v", err)
		}
	})

	t.Run("warns on unwritable directory", func(t *testing.T) {
		target := vmSelection{Name: "test", Directory: "/nonexistent/cove-r256"}
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		t.Cleanup(func() { os.Stderr = oldStderr })

		markInjectSucceededForVM(target)
		w.Close()
		var buf bytes.Buffer
		io.Copy(&buf, r)
		if !strings.Contains(buf.String(), "mark inject succeeded") {
			t.Errorf("expected warning on stderr, got %q", buf.String())
		}
	})
}
