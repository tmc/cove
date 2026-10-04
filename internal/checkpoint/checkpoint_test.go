package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var fixtureSources = []Source{{Path: "disk.img", Role: "disk"}, {Path: "data.img", Role: "disk"}, {Path: "aux.img", Role: "firmware"}}

func fixture(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	for _, source := range fixtureSources {
		writeFixture(t, root, source.Path, "saved "+source.Path)
	}
	m := New(root)
	if _, err := m.Save("baseline", "same-guest", fixtureSources); err != nil {
		t.Fatal(err)
	}
	for _, source := range fixtureSources {
		writeFixture(t, root, source.Path, "current "+source.Path)
	}
	return m, root
}

func writeFixture(t *testing.T, root, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertFiles(t *testing.T, root, prefix string) {
	t.Helper()
	for _, source := range fixtureSources {
		data, err := os.ReadFile(filepath.Join(root, source.Path))
		if err != nil || string(data) != prefix+source.Path {
			t.Fatalf("%s = %q, %v", source.Path, data, err)
		}
	}
}

func TestCheckpointSaveRestoreMultipleFiles(t *testing.T) {
	m, root := fixture(t)
	if err := m.Restore("baseline", "same-guest"); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, root, "saved ")
	if pending, err := m.Pending(); err != nil || pending {
		t.Fatalf("pending = %v, %v", pending, err)
	}
}

func TestCheckpointRestoreCrashRecovery(t *testing.T) {
	points := []string{"backup:disk.img", "prepare:data.img", "prepare-publish", "prepared", "journal:applying", "replace:disk.img", "replaced:disk.img", "replace:data.img", "replaced:data.img", "replace:aux.img", "replaced:aux.img", "journal:committed", "committed", "cleanup"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			m, root := fixture(t)
			injected := errors.New("crash")
			m.fault = func(p string) error {
				if p == point {
					return injected
				}
				return nil
			}
			if err := m.Restore("baseline", "same-guest"); !errors.Is(err, injected) {
				t.Fatalf("restore error = %v", err)
			}
			m.fault = nil
			if err := m.Recover(); err != nil {
				t.Fatal(err)
			}
			if point == "committed" || point == "cleanup" {
				assertFiles(t, root, "saved ")
			} else {
				assertFiles(t, root, "current ")
			}
			if pending, err := m.Pending(); err != nil || pending {
				t.Fatalf("pending after recovery = %v, %v", pending, err)
			}
			if err := m.Recover(); err != nil {
				t.Fatalf("repeated recovery = %v", err)
			}
		})
	}
}

func TestCheckpointRollbackCrashRecovery(t *testing.T) {
	for _, point := range []string{"journal:rolling-back", "rollback:disk.img", "rolled-back:disk.img", "rolled-back:data.img", "journal:rolled-back", "cleanup"} {
		t.Run(point, func(t *testing.T) {
			m, root := fixture(t)
			m.fault = func(p string) error {
				if p == "replaced:data.img" {
					return errors.New("restore crash")
				}
				return nil
			}
			if err := m.Restore("baseline", "same-guest"); err == nil {
				t.Fatal("restore succeeded")
			}
			m.fault = func(p string) error {
				if p == point {
					return errors.New("rollback crash")
				}
				return nil
			}
			if err := m.Recover(); err == nil {
				t.Fatal("rollback fault did not stop recovery")
			}
			m.fault = nil
			if err := m.Recover(); err != nil {
				t.Fatal(err)
			}
			assertFiles(t, root, "current ")
		})
	}
}

func TestCheckpointCaptureFailuresDoNotPublish(t *testing.T) {
	for _, point := range []string{"capture-copy:data.img", "capture-manifest", "capture-publish"} {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			for _, source := range fixtureSources {
				writeFixture(t, root, source.Path, "saved")
			}
			m := New(root)
			m.fault = func(p string) error {
				if p == point {
					return errors.New("capture failed")
				}
				return nil
			}
			if _, err := m.Save("bad", "same-guest", fixtureSources); err == nil {
				t.Fatal("save succeeded")
			}
			entries, err := os.ReadDir(filepath.Join(root, directory))
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed capture remains: %v, %v", entries, err)
			}
		})
	}
}

func TestCheckpointRefusesUnsafeSourcesAndCompatibility(t *testing.T) {
	for _, path := range []string{"../outside", "/absolute", "./disk.img", ".checkpoint-restore/x", "checkpoints/x", "run.lock", "manifest.json", "disk.img/child"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "disk.img", "source")
			if _, err := New(root).Save("bad", "identity", []Source{{Path: path, Role: "disk"}}); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	m, root := fixture(t)
	if err := m.Restore("baseline", "other-guest"); err == nil {
		t.Fatal("incompatible checkpoint accepted")
	}
	assertFiles(t, root, "current ")
	if err := os.Symlink(filepath.Join(root, "disk.img"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Save("link", "identity", []Source{{Path: "link", Role: "disk"}}); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestCheckpointCorruptionStopsBeforeRestore(t *testing.T) {
	m, root := fixture(t)
	writeFixture(t, filepath.Join(root, directory, "baseline"), "aux.img", "corrupt")
	if err := m.Restore("baseline", "same-guest"); err == nil {
		t.Fatal("corrupt checkpoint accepted")
	}
	assertFiles(t, root, "current ")
	if pending, _ := m.Pending(); pending {
		t.Fatal("invalid input started restore")
	}
}

func TestCheckpointRecoveryRefusesChangedTarget(t *testing.T) {
	m, root := fixture(t)
	m.fault = func(p string) error {
		if p == "replaced:disk.img" {
			return errors.New("crash")
		}
		return nil
	}
	if err := m.Restore("baseline", "same-guest"); err == nil {
		t.Fatal("restore succeeded")
	}
	writeFixture(t, root, "disk.img", "unrelated writer data")
	m.fault = nil
	if err := m.Recover(); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("changed target recovery error = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "disk.img"))
	if string(got) != "unrelated writer data" {
		t.Fatal("overwrote unrelated writer")
	}
	if pending, _ := m.Pending(); !pending {
		t.Fatal("lost recoverable transaction")
	}
}

func TestCheckpointRecoveryRemovesNewOptionalFile(t *testing.T) {
	m, root := fixture(t)
	writeFixture(t, root, "memory-source", "memory bytes")
	sources := append(append([]Source(nil), fixtureSources...), Source{Path: "suspend.vmstate", SourcePath: "memory-source", Role: "memory"})
	if _, err := m.Save("paired", "same-guest", sources); err != nil {
		t.Fatal(err)
	}
	for _, source := range fixtureSources {
		writeFixture(t, root, source.Path, "current "+source.Path)
	}
	m.fault = func(p string) error {
		if p == "replaced:suspend.vmstate" {
			return errors.New("crash")
		}
		return nil
	}
	if err := m.Restore("paired", "same-guest"); err == nil {
		t.Fatal("restore succeeded")
	}
	m.fault = nil
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, root, "current ")
	if _, err := os.Stat(filepath.Join(root, "suspend.vmstate")); !os.IsNotExist(err) {
		t.Fatalf("new optional memory retained: %v", err)
	}
}

func TestCheckpointReaderExclusion(t *testing.T) {
	m, root := fixture(t)
	reader, err := AcquireRead(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Restore("baseline", "same-guest"); err == nil {
		t.Fatal("restore overlapped an active reader")
	}
	assertFiles(t, root, "current ")
	if err := reader(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	m.fault = func(point string) error {
		if point == "replaced:disk.img" {
			close(started)
			<-finish
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- m.Restore("baseline", "same-guest") }()
	<-started
	if reader, err := AcquireRead(root); err == nil {
		reader()
		close(finish)
		<-done
		t.Fatal("reader entered partly installed checkpoint")
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	reader, err = AcquireRead(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reader()
	assertFiles(t, root, "saved ")
}

func TestCheckpointMissingRestoreParentPreflight(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "disks/data.img", "saved")
	m := New(root)
	if _, err := m.Save("nested", "same", []Source{{Path: "disks/data.img", Role: "disk"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "disks")); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore("nested", "same"); err == nil {
		t.Fatal("missing restore parent accepted")
	}
	if pending, err := m.Pending(); err != nil || pending {
		t.Fatalf("created transaction before parent validation: %v %v", pending, err)
	}
}

func TestCheckpointMutationImmediatelyBeforeReplace(t *testing.T) {
	m, root := fixture(t)
	m.fault = func(point string) error {
		if point == "replace:disk.img" {
			writeFixture(t, root, "disk.img", "outside writer")
		}
		return nil
	}
	if err := m.Restore("baseline", "same-guest"); err == nil {
		t.Fatal("outside write overwritten")
	}
	data, err := os.ReadFile(filepath.Join(root, "disk.img"))
	if err != nil || string(data) != "outside writer" {
		t.Fatalf("outside data=%q err=%v", data, err)
	}
	m.fault = nil
	if err := m.Recover(); err == nil {
		t.Fatal("recovery overwrote unknown state")
	}
}

func TestCheckpointRecoveryPaths(t *testing.T) {
	m, _ := fixture(t)
	m.fault = func(point string) error {
		if point == "prepared" {
			return errors.New("interrupted")
		}
		return nil
	}
	if err := m.Restore("baseline", "same-guest"); err == nil {
		t.Fatal("expected interruption")
	}
	paths, err := m.RecoveryPaths()
	if err != nil || strings.Join(paths, ",") != "disk.img,data.img,aux.img" {
		t.Fatalf("paths=%v err=%v", paths, err)
	}
}

func TestCheckpointAtomicJournalRetirement(t *testing.T) {
	m, root := fixture(t)
	m.fault = func(point string) error {
		if point == "cleanup-retired" {
			return errors.New("cleanup interrupted")
		}
		return nil
	}
	if err := m.Restore("baseline", "same-guest"); err == nil {
		t.Fatal("expected cleanup interruption")
	}
	assertFiles(t, root, "saved ")
	if pending, err := m.Pending(); err != nil || pending {
		t.Fatalf("partially deleted recovery journal exposed: %v %v", pending, err)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".checkpoint-retired-*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("retired=%v err=%v", matches, err)
	}
	if _, err := readJournal(matches[0]); err != nil {
		t.Fatalf("retired journal not complete: %v", err)
	}
	m.fault = nil
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
}
