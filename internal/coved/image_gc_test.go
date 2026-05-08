package coved

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

func TestImageGCRunOnce(t *testing.T) {
	tests := []struct {
		name          string
		images        map[string]int
		referenced    []string
		wantRemoved   int
		wantRemaining []string
	}{
		{
			name:        "removes orphan",
			images:      map[string]int{"orphan:old": 17},
			wantRemoved: 1,
		},
		{
			name:          "keeps referenced",
			images:        map[string]int{"base:keep": 23, "orphan:old": 11},
			referenced:    []string{"base:keep"},
			wantRemoved:   1,
			wantRemaining: []string{"base:keep"},
		},
		{
			name:        "empty store",
			wantRemoved: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			for ref, size := range tc.images {
				writeImage(t, home, ref, size)
			}
			for i, ref := range tc.referenced {
				writeVMConfig(t, home, "vm-"+string(rune('a'+i)), ref)
			}

			s := NewImageGCScheduler(home, nil)
			s.Now = func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) }
			stats, err := s.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if stats.ManifestsScanned != len(tc.images) {
				t.Fatalf("ManifestsScanned = %d, want %d", stats.ManifestsScanned, len(tc.images))
			}
			if stats.ManifestsRemoved != tc.wantRemoved {
				t.Fatalf("ManifestsRemoved = %d, want %d", stats.ManifestsRemoved, tc.wantRemoved)
			}
			for ref := range tc.images {
				if contains(tc.wantRemaining, ref) {
					if !imageExists(home, ref) {
						t.Fatalf("referenced image %s was removed", ref)
					}
				} else if imageExists(home, ref) {
					t.Fatalf("orphan image %s still exists", ref)
				}
			}

			events := readMetricEvents(t, filepath.Join(home, ".vz", "metrics.jsonl"))
			if len(events) != 1 {
				t.Fatalf("events = %d, want 1", len(events))
			}
			event := events[0]
			if event.EventType != "image.gc.run" || event.Status != "ok" {
				t.Fatalf("event = %+v", event)
			}
			assertExtraNumber(t, event.Extra, "manifests_scanned", float64(len(tc.images)))
			assertExtraNumber(t, event.Extra, "manifests_removed", float64(tc.wantRemoved))
			if _, ok := event.Extra["bytes_freed"]; !ok {
				t.Fatalf("event missing bytes_freed: %+v", event.Extra)
			}
			if _, ok := event.Extra["duration_ms"]; !ok {
				t.Fatalf("event missing duration_ms: %+v", event.Extra)
			}
		})
	}
}

func TestImageGCRunOnceSkipsWhenLocked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeImage(t, home, "orphan:old", 17)
	lock := filepath.Join(home, ".vz", "image-gc.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0700); err != nil {
		t.Fatal(err)
	}
	// Live PID (this test process) — must be respected.
	if err := os.WriteFile(lock, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}

	s := NewImageGCScheduler(home, nil)
	stats, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !stats.Skipped {
		t.Fatalf("Skipped = false, want true")
	}
	if !imageExists(home, "orphan:old") {
		t.Fatal("locked run removed image")
	}
	events := readMetricEvents(t, filepath.Join(home, ".vz", "metrics.jsonl"))
	if len(events) != 1 || events[0].Status != "skipped" {
		t.Fatalf("events = %+v", events)
	}
}

func TestImageGCRunOncePublishesToBus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeImage(t, home, "orphan:old", 17)
	bus := NewEventBus(4)
	s := NewImageGCScheduler(home, nil)
	s.Bus = bus
	if _, err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	tail := bus.Tail()
	if len(tail) != 1 || tail[0].EventType != "image.gc.run" {
		t.Fatalf("tail = %+v", tail)
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "metrics.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("metrics file err = %v, want missing when bus handles event", err)
	}
}

func writeImage(t *testing.T, home, ref string, payloadSize int) {
	t.Helper()
	name, tag, ok := stringsCut(ref, ":")
	if !ok {
		t.Fatalf("bad ref %q", ref)
	}
	path := filepath.Join(append([]string{home, ".vz", "images"}, append(splitSlash(name), tag)...)...)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"schemaVersion": 1, "name": name, "tag": tag, "createdAt": "2026-05-05T00:00:00Z"}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "manifest.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "disk.img"), make([]byte, payloadSize), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeVMConfig(t *testing.T, home, name, parentImage string) {
	t.Helper()
	path := filepath.Join(home, ".vz", "vms", name)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"parentImage": parentImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "config.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func imageExists(home, ref string) bool {
	name, tag, ok := stringsCut(ref, ":")
	if !ok {
		return false
	}
	path := filepath.Join(append([]string{home, ".vz", "images"}, append(splitSlash(name), tag, "manifest.json")...)...)
	_, err := os.Stat(path)
	return err == nil
}

func readMetricEvents(t *testing.T, path string) []runmetrics.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []runmetrics.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var event runmetrics.Event
		if err := json.Unmarshal(sc.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func assertExtraNumber(t *testing.T, extra map[string]any, key string, want float64) {
	t.Helper()
	got, ok := extra[key].(float64)
	if !ok {
		t.Fatalf("extra[%s] = %T %v", key, extra[key], extra[key])
	}
	if got != want {
		t.Fatalf("extra[%s] = %v, want %v", key, got, want)
	}
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func TestImageGCAcquireLockBreaksStaleLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lock := filepath.Join(home, ".vz", "image-gc.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0700); err != nil {
		t.Fatal(err)
	}
	deadPID := spawnDeadPID(t)
	if err := os.WriteFile(lock, []byte(fmt.Sprintf("%d\n", deadPID)), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewImageGCScheduler(home, nil)
	release, err := s.acquireLock()
	if err != nil {
		t.Fatalf("acquireLock with stale lock: %v", err)
	}
	release()
}

func TestImageGCAcquireLockRespectsLivePID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lock := filepath.Join(home, ".vz", "image-gc.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewImageGCScheduler(home, nil)
	if _, err := s.acquireLock(); !os.IsExist(err) {
		t.Fatalf("acquireLock with live PID: got %v, want ErrExist", err)
	}
}

func TestImageGCAcquireLockConcurrentBreak(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lock := filepath.Join(home, ".vz", "image-gc.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0700); err != nil {
		t.Fatal(err)
	}
	deadPID := spawnDeadPID(t)
	if err := os.WriteFile(lock, []byte(fmt.Sprintf("%d\n", deadPID)), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewImageGCScheduler(home, nil)
	const goroutines = 8
	var winners atomic.Int32
	start := make(chan struct{})
	done := make(chan func(), goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			release, err := s.acquireLock()
			if err == nil {
				winners.Add(1)
				done <- release
			} else {
				done <- nil
			}
		}()
	}
	close(start)
	var releases []func()
	for i := 0; i < goroutines; i++ {
		if r := <-done; r != nil {
			releases = append(releases, r)
		}
	}
	for _, r := range releases {
		r()
	}
	if got := winners.Load(); got != 1 {
		t.Fatalf("concurrent acquire: %d winners, want 1", got)
	}
}

// spawnDeadPID returns a PID that has been reaped and is therefore dead.
func spawnDeadPID(t *testing.T) int {
	t.Helper()
	proc, err := os.StartProcess("/usr/bin/true", []string{"true"}, &os.ProcAttr{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !state.Exited() {
		t.Fatalf("child did not exit")
	}
	return proc.Pid
}

func stringsCut(s, sep string) (string, string, bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

func splitSlash(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
