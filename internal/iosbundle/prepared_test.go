package iosbundle

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ExampleConfig_ValidatePrepared() {
	cfg := DefaultConfig()
	cfg.SchemaVersion = 2
	fmt.Println(cfg.ValidatePrepared("unused"))
	// Output:
	// ios configuration: unsupported ios schema version 2
}

func preparedFixture(t *testing.T) (string, Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.ROM = "firmware/boot.bin"
	cfg.SEPROM = "firmware/sep.bin"
	if err := os.Mkdir(filepath.Join(dir, "firmware"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hw.model", "machine.id", "aux.img", "sep.img", "disk.img", cfg.ROM, cfg.SEPROM} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, cfg
}

func snapshotPrepared(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := fmt.Sprint(info.Mode(), info.Size(), info.ModTime())
		if info.Mode().IsRegular() && info.Mode().Perm()&0400 != 0 {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += string(data)
		}
		got[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestValidatePrepared(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, *Config)
		want   string
	}{
		{"valid", func(t *testing.T, dir string, c *Config) {}, ""},
		{"optional sep rom", func(t *testing.T, dir string, c *Config) { c.SEPROM = "" }, ""},
		{"schema", func(t *testing.T, dir string, c *Config) { c.SchemaVersion = 2 }, "unsupported ios schema version 2"},
		{"missing rom setting", func(t *testing.T, dir string, c *Config) { c.ROM = "" }, "ios rom: configure a prepared boot rom"},
		{"boot args", func(t *testing.T, dir string, c *Config) { c.BootArgs = "-v" }, "prepare nvram externally and clear bootArgs"},
		{"path traversal", func(t *testing.T, dir string, c *Config) { c.ROM = "../boot.bin" }, "bundle-relative path"},
		{"missing state", func(t *testing.T, dir string, c *Config) { mustPrepared(t, os.Remove(filepath.Join(dir, "aux.img"))) }, `ios file "aux.img": resolve existing file`},
		{"missing rom", func(t *testing.T, dir string, c *Config) { c.ROM = "missing.bin" }, `ios file "missing.bin": resolve existing file`},
		{"empty state", func(t *testing.T, dir string, c *Config) {
			mustPrepared(t, os.Truncate(filepath.Join(dir, "sep.img"), 0))
		}, `ios file "sep.img": expected a nonempty regular file`},
		{"directory", func(t *testing.T, dir string, c *Config) { c.ROM = "firmware" }, `ios file "firmware": expected a nonempty regular file`},
		{"same path", func(t *testing.T, dir string, c *Config) { c.ROM = "disk.img" }, `aliases "disk.img"`},
		{"hard link", func(t *testing.T, dir string, c *Config) {
			mustPrepared(t, os.Remove(filepath.Join(dir, c.ROM)))
			mustPrepared(t, os.Link(filepath.Join(dir, "aux.img"), filepath.Join(dir, c.ROM)))
		}, `aliases "aux.img"`},
		{"contained symlink", func(t *testing.T, dir string, c *Config) {
			mustPrepared(t, os.Symlink("boot.bin", filepath.Join(dir, "firmware/link.bin")))
			c.ROM = "firmware/link.bin"
		}, ""},
		{"escaping symlink", func(t *testing.T, dir string, c *Config) {
			outside := t.TempDir()
			mustPrepared(t, os.WriteFile(filepath.Join(outside, "boot.bin"), []byte("boot"), 0600))
			mustPrepared(t, os.Symlink(outside, filepath.Join(dir, "external")))
			c.ROM = "external/boot.bin"
		}, `ios file "external/boot.bin": resolve existing file inside bundle`},
		{"read only state", func(t *testing.T, dir string, c *Config) {
			p := filepath.Join(dir, "disk.img")
			mustPrepared(t, os.Chmod(p, 0400))
			f, err := os.OpenFile(p, os.O_RDWR, 0)
			if err == nil {
				f.Close()
				t.Skip("process can write read-only files")
			}
		}, `ios file "disk.img": read/write access required`},
		{"unreadable rom", func(t *testing.T, dir string, c *Config) {
			p := filepath.Join(dir, c.ROM)
			mustPrepared(t, os.Chmod(p, 0000))
			f, err := os.Open(p)
			if err == nil {
				f.Close()
				t.Skip("process can read inaccessible files")
			}
		}, `ios file "firmware/boot.bin": read access required`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, cfg := preparedFixture(t)
			tt.change(t, dir, &cfg)
			before := snapshotPrepared(t, dir)
			err := cfg.ValidatePrepared(dir)
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("ValidatePrepared() = %v, want %q", err, tt.want)
			}
			if after := snapshotPrepared(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatal("validation changed bundle contents, metadata or entries")
			}
		})
	}
}

func mustPrepared(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidatePreparedReportsMissingArtifacts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ROM = "boot.bin"
	err := cfg.ValidatePrepared(t.TempDir())
	for _, name := range []string{"hw.model", "machine.id", "aux.img", "sep.img", "disk.img", "boot.bin"} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("missing diagnostic for %s: %v", name, err)
		}
	}
}

func TestValidatePreparedBundleRoot(t *testing.T) {
	for _, name := range []string{"missing", "not a directory"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bundle")
			if name == "not a directory" {
				mustPrepared(t, os.WriteFile(path, []byte("file"), 0600))
			}
			cfg := DefaultConfig()
			cfg.ROM = "boot.bin"
			if err := cfg.ValidatePrepared(path); err == nil || !strings.Contains(err.Error(), "open directory") {
				t.Fatalf("ValidatePrepared() = %v", err)
			}
			if name == "missing" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("validation created bundle: %v", err)
				}
			}
		})
	}
}
