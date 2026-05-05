package runs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/metrics"
)

func TestExportJSON(t *testing.T) {
	root := t.TempDir()
	events := []metrics.Event{
		event("vm_start", "ok", 12, nil),
		event("run_complete", "ok", 34, map[string]any{"exit_code": 0}),
	}
	writeRun(t, root, "20260505-json", events, nil)

	var buf bytes.Buffer
	if err := ExportJSON(&buf, root, "20260505-j"); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var got []metrics.Event
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 2 || got[0].EventType != "vm_start" || got[1].EventType != "run_complete" {
		t.Fatalf("events = %+v", got)
	}
}

func TestExportGHASummary(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260505-gha", []metrics.Event{
		event("fork_created", "ok", 10, nil),
		event("vm_start", "failed", 20, map[string]any{"reason": "boot failed"}),
		event("run_complete", "failed", 30, map[string]any{"exit_code": 1}),
	}, nil)

	var buf bytes.Buffer
	if err := ExportGHASummary(&buf, root, "20260505-g"); err != nil {
		t.Fatalf("ExportGHASummary: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"## Cove Run 20260505-gha",
		"| Phase | Status | Duration |",
		"| fork_created | [ok] ok | 10ms |",
		"| vm_start | [fail] failed | 20ms |",
		"**Result:** [fail] failed exit_code=1 wallclock=30ms",
		"**Failure:** `vm_start`: boot failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestExportTarGzContainsRunFiles(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260505-tar", []metrics.Event{
		event("run_complete", "ok", 1, nil),
	}, map[string]string{
		"stdout.log":           "out\n",
		"screenshots/shot.txt": "shot\n",
	})

	var buf bytes.Buffer
	if err := ExportTarGz(&buf, root, "20260505-t"); err != nil {
		t.Fatalf("ExportTarGz: %v", err)
	}
	names := tarNames(t, buf.Bytes())
	for _, want := range []string{
		"20260505-tar/metrics.jsonl",
		"20260505-tar/stdout.log",
		"20260505-tar/screenshots/shot.txt",
	} {
		if !names[want] {
			t.Fatalf("tar missing %q in %#v", want, names)
		}
	}
}

func tarNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		names[h.Name] = true
	}
	return names
}
