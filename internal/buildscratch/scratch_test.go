package buildscratch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGC(t *testing.T) {
	root := t.TempDir()
	writePID(t, filepath.Join(root, "live"), "100\n")
	writePID(t, filepath.Join(root, "dead"), "200\n")
	writePID(t, filepath.Join(root, "bad"), "not-a-pid\n")
	if err := GC(root, func(pid int) bool { return pid == 100 }); err != nil {
		t.Fatalf("GC(): %v", err)
	}
	for _, name := range []string{"live", "bad"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s removed unexpectedly: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "dead")); !os.IsNotExist(err) {
		t.Fatalf("dead scratch still exists: %v", err)
	}
}

func TestPrune(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	mkdir := func(name string, age time.Duration, payload int64, pid string) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if pid != "" {
			if err := os.WriteFile(filepath.Join(dir, "build.pid"), []byte(pid), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if payload > 0 {
			if err := os.WriteFile(filepath.Join(dir, "disk.img"), make([]byte, payload), 0644); err != nil {
				t.Fatal(err)
			}
		}
		mtime := now.Add(-age)
		if err := os.Chtimes(dir, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	young := mkdir("young", 30*time.Minute, 100, "200")
	old := mkdir("old", 10*24*time.Hour, 1000, "300")
	live := mkdir("live", 10*24*time.Hour, 500, "100")
	recent := mkdir("recent", 2*time.Hour, 200, "400")
	noPID := mkdir("no-pid", 10*24*time.Hour, 250, "")
	veryOld := mkdir("very-old", 30*24*time.Hour, 800, "5")

	isLive := func(pid int) bool { return pid == 100 }
	dryRep, err := Prune(root, 7*24*time.Hour, false, isLive, func() time.Time { return now })
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if dryRep.Apply {
		t.Fatalf("dry-run report claims Apply=true")
	}
	for _, dir := range []string{young, old, live, recent, noPID, veryOld} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("dry-run removed %s: %v", dir, err)
		}
	}
	if want := int64(1003 + 250 + 801); dryRep.BytesRemoved != want {
		t.Errorf("dry-run BytesRemoved = %d, want %d", dryRep.BytesRemoved, want)
	}

	rep, err := Prune(root, 7*24*time.Hour, true, isLive, func() time.Time { return now })
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, dir := range []string{young, live, recent} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("apply removed kept dir %s: %v", dir, err)
		}
	}
	for _, dir := range []string{old, noPID, veryOld} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("apply did not remove %s: %v", dir, err)
		}
	}
	if rep.BytesRemoved != int64(1003+250+801) {
		t.Errorf("apply BytesRemoved = %d, want %d", rep.BytesRemoved, 1003+250+801)
	}
	if rep.BytesKept != int64(103+503+203) {
		t.Errorf("apply BytesKept = %d, want %d", rep.BytesKept, 103+503+203)
	}
	reasons := map[string]string{}
	for _, e := range rep.Entries {
		reasons[filepath.Base(e.Dir)] = e.Reason
	}
	for name, want := range map[string]string{
		"young":    "too-young",
		"recent":   "too-young",
		"live":     "live-pid",
		"old":      "removed",
		"no-pid":   "removed",
		"very-old": "removed",
	} {
		if got := reasons[name]; got != want {
			t.Errorf("reason for %s = %q, want %q", name, got, want)
		}
	}
}

func TestPruneSanityFloor(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(root, "fresh")
	writePID(t, dir, "999")
	mtime := now.Add(-30 * time.Minute)
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	rep, err := Prune(root, time.Second, true, func(int) bool { return false }, func() time.Time { return now })
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir removed despite < 1h floor: %v", err)
	}
	if rep.OlderThan < PruneSanityFloor {
		t.Fatalf("OlderThan = %s, want >= sanity floor %s", rep.OlderThan, PruneSanityFloor)
	}
}

func writePID(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.pid"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
