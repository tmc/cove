package runs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/metrics"
)

// seedRuns creates n synthetic run dirs under root. Each dir has a
// metrics.jsonl with a small lifecycle and a run_complete event. Returns
// the total bytes written across all metrics.jsonl files.
func seedRuns(tb testing.TB, root string, n int) int64 {
	tb.Helper()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	var total int64
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, fmt.Sprintf("run-%08d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		path := filepath.Join(dir, "metrics.jsonl")
		f, err := os.Create(path)
		if err != nil {
			tb.Fatal(err)
		}
		ts := base.Add(time.Duration(i) * time.Second)
		status := "ok"
		if i%5 == 0 {
			status = "fail"
		}
		events := []metrics.Event{
			{Timestamp: ts.Format(time.RFC3339Nano), EventType: "fork_created"},
			{Timestamp: ts.Add(50 * time.Millisecond).Format(time.RFC3339Nano), EventType: "vm_start"},
			{Timestamp: ts.Add(time.Second).Format(time.RFC3339Nano), EventType: "agent_ready"},
			{
				Timestamp:  ts.Add(2 * time.Second).Format(time.RFC3339Nano),
				EventType:  runCompleteEvent,
				ImageRef:   "img:latest",
				VMName:     "bench",
				DurationMS: 2000,
				Status:     status,
				Extra:      map[string]any{"exit_code": float64(0)},
			},
		}
		w := &countWriter{w: f}
		enc := json.NewEncoder(w)
		for _, e := range events {
			if err := enc.Encode(e); err != nil {
				tb.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			tb.Fatal(err)
		}
		total += w.n
	}
	return total
}

// seedShowRun creates a single run dir with a metrics.jsonl whose size
// is approximately targetBytes. Returns dir and actual bytes written.
func seedShowRun(tb testing.TB, root string, targetBytes int) (string, int64) {
	tb.Helper()
	dir := filepath.Join(root, "run-show-0001")
	if err := os.Mkdir(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	path := filepath.Join(dir, "metrics.jsonl")
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	w := &countWriter{w: f}
	enc := json.NewEncoder(w)
	// Write a leading non-lifecycle filler event repeatedly.
	step := time.Millisecond
	i := 0
	for w.n < int64(targetBytes) {
		e := metrics.Event{
			Timestamp:  ts.Add(time.Duration(i) * step).Format(time.RFC3339Nano),
			EventType:  "build_step",
			DurationMS: int64(i),
			Status:     "ok",
			Extra:      map[string]any{"i": i, "note": "filler-bench-event"},
		}
		if err := enc.Encode(e); err != nil {
			tb.Fatal(err)
		}
		i++
	}
	// Terminal event.
	if err := enc.Encode(metrics.Event{
		Timestamp:  ts.Add(time.Duration(i) * step).Format(time.RFC3339Nano),
		EventType:  runCompleteEvent,
		Status:     "ok",
		DurationMS: 1234,
		Extra:      map[string]any{"exit_code": float64(0)},
	}); err != nil {
		tb.Fatal(err)
	}
	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
	return dir, w.n
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func BenchmarkList(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			root := b.TempDir()
			total := seedRuns(b, root, n)
			b.SetBytes(total)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := List(root, Filter{})
				if err != nil {
					b.Fatal(err)
				}
				if len(out) != n {
					b.Fatalf("got %d runs, want %d", len(out), n)
				}
			}
		})
	}
}

func BenchmarkLoadShow(b *testing.B) {
	sizes := []int{1 << 10, 100 << 10, 10 << 20}
	names := []string{"1KB", "100KB", "10MB"}
	for i, target := range sizes {
		b.Run(names[i], func(b *testing.B) {
			root := b.TempDir()
			_, written := seedShowRun(b, root, target)
			b.SetBytes(written)
			b.ReportAllocs()
			b.ResetTimer()
			for j := 0; j < b.N; j++ {
				show, err := LoadShow(root, "run-show-0001")
				if err != nil {
					b.Fatal(err)
				}
				if show.Result.Status != "ok" {
					b.Fatalf("status=%q", show.Result.Status)
				}
			}
		})
	}
}
