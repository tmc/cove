package ios

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/ios/bundle"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestConfigureStagesROMAndPreservesMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "phone.covevm")
	config := bundle.DefaultConfig()
	if err := CreateBundle(dir, config, 2, 2, 1<<20); err != nil {
		t.Fatal(err)
	}
	current, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	current.ParentVM = "parent"
	if err := vmconfig.Save(dir, current); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "input.rom")
	data := []byte("test ROM bytes")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	config.ROM, config.Network, config.Display.Scale = source, "none", 2
	hardware := vmconfig.Hardware{CPU: 2, MemoryGB: 2}
	if err := Configure(dir, hardware, config); err != nil {
		t.Fatal(err)
	}
	saved, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("roms", fmt.Sprintf("%x.rom", sha256.Sum256(data)))
	if saved.IOS.ROM != want || saved.IOS.Network != "none" || saved.IOS.Display.Scale != 2 || saved.ParentVM != "parent" {
		t.Fatalf("config=%+v ios=%+v", saved, saved.IOS)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if _, err := romFile(dir, saved.IOS.ROM); err != nil {
		t.Fatal(err)
	}
	if err := Configure(dir, hardware, *saved.IOS); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "roms"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, want), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := romFile(dir, want); err == nil {
		t.Fatal("runtime accepted corrupt staged ROM")
	}
	if err := Configure(dir, hardware, *saved.IOS); err == nil {
		t.Fatal("silently restaged corrupted ROM")
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed update changed config")
	}
}

func TestConfigureFailurePreservesConfig(t *testing.T) {
	for _, kind := range []string{"missing-rom", "invalid-display", "invalid-memory", "symlink-roms"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "phone")
			config := bundle.DefaultConfig()
			if err := CreateBundle(dir, config, 2, 2, 512); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			hardware := vmconfig.Hardware{CPU: 2, MemoryGB: 2}
			switch kind {
			case "missing-rom":
				config.ROM = "missing"
			case "invalid-display":
				config.Display.Scale = 0
			case "invalid-memory":
				hardware.MemoryGB = 1 << 63
			case "symlink-roms":
				external := t.TempDir()
				if err := os.Symlink(external, filepath.Join(dir, "roms")); err != nil {
					t.Fatal(err)
				}
				config.ROM = "input.rom"
				if err := os.WriteFile(filepath.Join(dir, config.ROM), []byte("ROM"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := Configure(dir, hardware, config); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			after, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("failed update replaced config")
			}
		})
	}
}

func ExampleConfigure() {
	parent, err := os.MkdirTemp("", "cove-ios-config-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(parent)
	dir := filepath.Join(parent, "phone.covevm")
	config := bundle.DefaultConfig()
	if err := CreateBundle(dir, config, 2, 2, 1<<20); err != nil {
		panic(err)
	}
	config.Network = "none"
	err = Configure(dir, vmconfig.Hardware{CPU: 2, MemoryGB: 2}, config)
	fmt.Println(err)
	// Output: <nil>
}

func TestFailedROMUpdateRemovesOnlyNewContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "phone")
	config := bundle.DefaultConfig()
	hardware := vmconfig.Hardware{CPU: 2, MemoryGB: 2}
	if err := CreateBundle(dir, config, 2, 2, 512); err != nil {
		t.Fatal(err)
	}
	sources := t.TempDir()
	oldROM := filepath.Join(sources, "old.rom")
	newROM := filepath.Join(sources, "new.rom")
	for path, data := range map[string]string{oldROM: "old", newROM: "new"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config.ROM = oldROM
	if err := Configure(dir, hardware, config); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{oldROM, newROM} {
		config.ROM, config.SEPROM = source, "missing"
		if err := Configure(dir, hardware, config); err == nil {
			t.Fatal("missing SEP ROM accepted")
		}
		entries, err := os.ReadDir(filepath.Join(dir, "roms"))
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%x.rom", sha256.Sum256([]byte("old")))
		if len(entries) != 1 || entries[0].Name() != want {
			t.Fatalf("content after failed update=%v", entries)
		}
		after, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatal("failed update changed config")
		}
	}
}
