package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/vmconfig"
)

func pullPublicationFixture(t *testing.T) (*pullPlan, pullOptions) {
	t.Helper()
	root := t.TempDir()
	t.Setenv(vmconfig.StateDirEnv, root)
	return &pullPlan{VMName: "pull-test", VMDir: vmconfig.Path("pull-test"), ManifestDigest: "sha256:test"}, pullOptions{}
}
func openPullPublicationForTest(t *testing.T, plan *pullPlan, opts pullOptions, deps pullPublicationDeps) *pullPublication {
	t.Helper()
	if deps.live == nil {
		deps.live = func(string) (bool, error) { return false, nil }
	}
	p, err := beginPullPublicationWithDeps(context.Background(), plan, opts, deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}
func writePullPartialForTest(t *testing.T, p *pullPublication) {
	t.Helper()
	f, err := p.root.OpenFile("disk.img.partial", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.recordPartial(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	_, err = f.WriteString("downloaded")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestPullPublicationReleasesRootDuringTransfer(t *testing.T) {
	plan, opts := pullPublicationFixture(t)
	p := openPullPublicationForTest(t, plan, opts, pullPublicationDeps{})
	guard, err := mutationguard.Acquire(vmconfig.StateDir())
	if err != nil {
		t.Fatalf("streaming retained root guard: %v", err)
	}
	guard.Release()
	if lock, err := AcquireRunLock(plan.VMDir); err == nil {
		lock.Release()
		t.Fatal("streaming released run lock")
	}
	writePullPartialForTest(t, p)
	if err := p.publish(context.Background(), plan.ManifestDigest); err != nil {
		t.Fatal(err)
	}
	data, err := p.root.ReadFile("disk.img")
	if err != nil || string(data) != "downloaded" {
		t.Fatalf("disk %q %v", data, err)
	}
	if data, err := p.root.ReadFile("disk.provenance"); err != nil || string(data) != plan.ManifestDigest+"\n" {
		t.Fatalf("provenance %q %v", data, err)
	}
}
func TestPullPublicationRefusesChangedHandoff(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *pullPublication)
	}{
		{"vm replaced", func(t *testing.T, p *pullPublication) {
			if err := os.Rename(p.directory, p.directory+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(p.directory, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"partial replaced", func(t *testing.T, p *pullPublication) {
			if err := p.root.Rename("disk.img.partial", "original.partial"); err != nil {
				t.Fatal(err)
			}
			if err := p.root.WriteFile("disk.img.partial", []byte("operator"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"partial symlink", func(t *testing.T, p *pullPublication) {
			if err := p.root.Rename("disk.img.partial", "original.partial"); err != nil {
				t.Fatal(err)
			}
			if err := p.root.Symlink("original.partial", "disk.img.partial"); err != nil {
				t.Fatal(err)
			}
		}},
		{"destination appeared", func(t *testing.T, p *pullPublication) {
			if err := p.root.WriteFile("disk.img", []byte("operator"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"run lock contested", func(t *testing.T, p *pullPublication) {
			lock, err := AcquireRunLock(p.directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lock.Release() })
		}},
		{"root guard contested", func(t *testing.T, p *pullPublication) {
			guard, err := mutationguard.Acquire(vmconfig.StateDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { guard.Release() })
		}},
		{"owner appeared", func(t *testing.T, p *pullPublication) { p.deps.live = func(string) (bool, error) { return true, nil } }},
		{"owner unknown", func(t *testing.T, p *pullPublication) {
			p.deps.live = func(string) (bool, error) { return false, errors.New("observation unavailable") }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, opts := pullPublicationFixture(t)
			p := openPullPublicationForTest(t, plan, opts, pullPublicationDeps{})
			writePullPartialForTest(t, p)
			p.deps.handoff = func() { tt.change(t, p) }
			if err := p.publish(context.Background(), plan.ManifestDigest); err == nil {
				t.Fatal("published after unsafe handoff")
			}
			if _, err := p.root.Lstat("disk.provenance"); !os.IsNotExist(err) {
				t.Fatalf("publication side effects: %v", err)
			}
			if _, err := p.root.Lstat("disk.img.partial"); err != nil {
				t.Fatalf("lost partial: %v", err)
			}
		})
	}
}
func TestPullPublicationAdmissionRejectsActiveWithoutSocket(t *testing.T) {
	plan, opts := pullPublicationFixture(t)
	p, err := beginPullPublicationWithDeps(context.Background(), plan, opts, pullPublicationDeps{live: func(string) (bool, error) { return true, nil }})
	if p != nil {
		p.Close()
	}
	if err == nil {
		t.Fatal("accepted active owner without socket")
	}
	if _, err := os.Stat(filepath.Join(plan.VMDir, "disk.img")); !os.IsNotExist(err) {
		t.Fatalf("created disk: %v", err)
	}
}
func TestPullPublicationSuppressesCacheAliases(t *testing.T) {
	plan, opts := pullPublicationFixture(t)
	plan.VMDir = filepath.Join(t.TempDir(), "cache")
	plan.SuppressAliases = true
	p := openPullPublicationForTest(t, plan, opts, pullPublicationDeps{})
	writePullPartialForTest(t, p)
	if err := p.publish(context.Background(), plan.ManifestDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(vmconfig.BaseDir(), plan.VMName)); !os.IsNotExist(err) {
		t.Fatalf("cache registered alias: %v", err)
	}
}
func TestPullPublicationCancellationDuringHandoff(t *testing.T) {
	plan, opts := pullPublicationFixture(t)
	p := openPullPublicationForTest(t, plan, opts, pullPublicationDeps{})
	writePullPartialForTest(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	p.deps.handoff = cancel
	if err := p.publish(ctx, plan.ManifestDigest); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := p.root.Lstat("disk.img.partial"); err != nil {
		t.Fatal(err)
	}
}

func TestPullPublicationDestinationReplacementIsPreserved(t *testing.T) {
	plan, opts := pullPublicationFixture(t)
	if err := os.MkdirAll(plan.VMDir, 0700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(plan.VMDir, "disk.img")
	if err := os.WriteFile(destination, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	p := openPullPublicationForTest(t, plan, opts, pullPublicationDeps{})
	writePullPartialForTest(t, p)
	p.deps.handoff = func() {
		if err := p.root.Rename("disk.img", "old.img"); err != nil {
			t.Fatal(err)
		}
		if err := p.root.WriteFile("disk.img", []byte("operator"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.publish(context.Background(), plan.ManifestDigest); err == nil {
		t.Fatal("overwrote changed destination")
	}
	if data, err := p.root.ReadFile("disk.img"); err != nil || string(data) != "operator" {
		t.Fatalf("replacement changed %q %v", data, err)
	}
}
func TestPullPublicationRejectsSymlinkAdmission(t *testing.T) {
	for _, name := range []string{"disk.img", "disk.img.partial"} {
		t.Run(name, func(t *testing.T) {
			plan, opts := pullPublicationFixture(t)
			if err := os.MkdirAll(plan.VMDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("operator-missing", filepath.Join(plan.VMDir, name)); err != nil {
				t.Fatal(err)
			}
			p, err := beginPullPublicationWithDeps(context.Background(), plan, opts, pullPublicationDeps{live: func(string) (bool, error) { return false, nil }})
			if p != nil {
				p.Close()
			}
			if err == nil {
				t.Fatal("accepted symlink destination")
			}
		})
	}
}

func TestPullFormatsRequirePublicationAdmission(t *testing.T) {
	tests := []struct {
		name string
		pull func(context.Context, *pullPlan, pullOptions) error
	}{
		{"native", pullDisk}, {"lume", lumePullDisk}, {"tart", tartPullDisk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, opts := pullPublicationFixture(t)
			plan.Manifest.DiskLayers = []ociimage.DiskLayer{{}}
			plan.Manifest.Lume.DiskParts = []ociimage.LumeLayer{{}}
			plan.Manifest.Tart.DiskLayers = []ociimage.TartDiskLayer{{}}
			guard, err := mutationguard.Acquire(vmconfig.StateDir())
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Release()
			if err := tt.pull(context.Background(), plan, opts); !errors.Is(err, mutationguard.ErrBusy) {
				t.Fatalf("format bypassed admission: %v", err)
			}
			if _, err := os.Stat(plan.VMDir); !os.IsNotExist(err) {
				t.Fatalf("format created vm without admission: %v", err)
			}
		})
	}
}

func TestPullPublicationCanonicalizesStreamDirectory(t *testing.T) {
	plan, opts := pullPublicationFixture(t)
	parent := t.TempDir()
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(canonical, alias); err != nil {
		t.Fatal(err)
	}
	plan.VMDir = filepath.Join(alias, "cache")
	plan.SuppressAliases = true
	p := openPullPublicationForTest(t, plan, opts, pullPublicationDeps{})
	if plan.VMDir != filepath.Join(canonical, "cache") || plan.VMDir != p.directory {
		t.Fatalf("stream path %q differs from held directory %q", plan.VMDir, p.directory)
	}
	writePullPartialForTest(t, p)
	if err := p.publish(context.Background(), plan.ManifestDigest); err != nil {
		t.Fatal(err)
	}
}
