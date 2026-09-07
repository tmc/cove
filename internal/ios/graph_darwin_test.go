package ios

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/objc"
	"github.com/tmc/cove/internal/ios/bundle"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestGraphRejectsMissingROMBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	config := bundle.DefaultConfig()
	if err := vmconfig.Save(dir, &vmconfig.Config{IOS: &config, CPU: 2, MemoryGB: 2}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if graph, err := buildGraph(dir, true, 0); err == nil || graph != nil {
		t.Fatalf("graph=%v err=%v", graph, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("created guest state: %v", entries)
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("changed config")
	}
}

func TestGraphFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rom"), []byte("firmware"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rom", filepath.Join(dir, "rom")} {
		got, err := graphFile(dir, name)
		if err != nil || got != filepath.Join(dir, "rom") {
			t.Fatalf("path=%q err=%v", got, err)
		}
	}
	for _, name := range []string{"", "missing", dir} {
		if _, err := graphFile(dir, name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestGraphCloseReleasesPipes(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	graph := deviceGraph{files: []*os.File{read, write}}
	graph.close()
	graph.close()
	if _, err := write.Write([]byte("x")); err == nil {
		t.Fatal("pipe still open")
	}
}

func TestObserveResearchDeviceABI(t *testing.T) {
	objc.AutoreleasePool(func() {
		if err := probeResearchDevices(); err != nil {
			t.Logf("research device ABI unavailable: %v", err)
			return
		}
		t.Log("research device constructors and scalar setter ABI available; no devices or VM created")
	})
}
