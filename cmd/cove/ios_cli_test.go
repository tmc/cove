package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/iosbundle"
	"github.com/tmc/cove/internal/vmconfig"
	"golang.org/x/sys/unix"
)

func iosPreparedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	ios.ROM = "boot.rom"
	if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 2, MemoryGB: 4, IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hw.model", "machine.id", "aux.img", "sep.img", "disk.img", "boot.rom"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("static fixture "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestIOSValidateCLI(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*testing.T, string)
		want   string
	}{
		{"valid", func(*testing.T, string) {}, "Prepared iOS files validated"},
		{"missing disk", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "disk.img")); err != nil {
				t.Fatal(err)
			}
		}, "disk.img"},
		{"suspend", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "suspend.vmstate"), []byte("saved"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "save and resume"},
		{"config fifo", func(t *testing.T, dir string) {
			path := filepath.Join(dir, "config.json")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
		}, "config.json must be a nonempty regular file"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := iosPreparedFixture(t)
			tt.change(t, dir)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().IsRegular() {
					data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					before[entry.Name()] = data
				}
			}
			var out bytes.Buffer
			env := commandTestEnv()
			env.Stdout = &out
			env.Stderr = &out
			status := runIOSCommand(env, "ios", []string{"validate", "-vm-dir", dir})
			if (status == 0) != (tt.name == "valid") || !strings.Contains(out.String(), tt.want) {
				t.Fatalf("status=%d output=%q", status, out.String())
			}
			after, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(entries) {
				t.Fatal("validation changed directory entries")
			}
			for name, want := range before {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("validation changed %s", name)
				}
			}
		})
	}
}
