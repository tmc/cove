package ios

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/ios/bundle"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestCreateBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "phone.covevm")
	config := bundle.DefaultConfig()
	if err := CreateBundle(dir, config, 2, 2, 1<<30); err != nil {
		t.Fatal(err)
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IOS == nil || *cfg.IOS != config || cfg.CPU != 2 || cfg.MemoryGB != 2 {
		t.Fatalf("config = %+v", cfg)
	}
	for _, file := range []struct {
		name string
		size int64
	}{{"disk.img", 1 << 30}, {"sep.img", 512 << 10}} {
		info, err := os.Stat(filepath.Join(dir, file.name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != file.size {
			t.Fatalf("%s size = %d", file.name, info.Size())
		}
	}
	sep, err := os.ReadFile(filepath.Join(dir, "sep.img"))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range sep {
		if b != 0 {
			t.Fatal("sep storage is not zero filled")
		}
	}
	if !vmconfig.Validate(dir) || vmconfig.DetectOSType(dir) != "iOS" {
		t.Fatal("blank bundle not recognized")
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateBundle(dir, config, 4, 4, 2<<30); err == nil {
		t.Fatal("overwrote bundle")
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("collision changed config")
	}
	if err := os.Remove(filepath.Join(dir, "sep.img")); err != nil {
		t.Fatal(err)
	}
	if vmconfig.Validate(dir) {
		t.Fatal("missing sep accepted")
	}
}

func TestCreateBundleRejectsInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cpu    uint
		memory uint64
		disk   int64
	}{
		{"cpu", 0, 2, 512}, {"memory", 2, 0, 512}, {"overflow", 2, 1 << 63, 512},
		{"disk", 2, 2, -512}, {"unaligned", 2, 2, 513},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "phone")
			if err := CreateBundle(dir, bundle.DefaultConfig(), tt.cpu, tt.memory, tt.disk); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatalf("created destination: %v", err)
			}
		})
	}
}

func ExampleCreateBundle() {
	parent, err := os.MkdirTemp("", "cove-ios-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(parent)
	dir := filepath.Join(parent, "phone.covevm")
	err = CreateBundle(dir, bundle.DefaultConfig(), 2, 2, 1<<30)
	fmt.Println(err, vmconfig.DetectOSType(dir))
	// Output: <nil> iOS
}
