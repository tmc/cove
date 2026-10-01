package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDiskDF(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		free        uint64
		wantErr     bool
	}{
		{"macOS", "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/disk3s5 94908416 71230848 110924 100% /System/Volumes/Data\n", 110924 * 1024, false},
		{"spaces", "Filesystem 1024-blocks Used Available Capacity Mounted on\nvirtio-fs 10000000 2000000 8000000 20% /Volumes/My Shared Files\n", 8000000 * 1024, false},
		{"malformed", "Filesystem\n/dev/disk0 nope 2 3 90% /", 0, true},
		{"overflow", "Filesystem\n/dev/disk0 18446744073709551615 2 3 90% /", 0, true},
		{"multiple", "header\n/dev/a 1 1 0 100% /\n/dev/b 1 1 0 100% /mnt", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseDiskDF(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if err == nil && r.FreeBytes != tt.free {
				t.Fatalf("free = %d, want %d", r.FreeBytes, tt.free)
			}
		})
	}
}

func TestDiskSpaceStatus(t *testing.T) {
	for _, tt := range []struct {
		name        string
		total, free uint64
		want        string
	}{
		{"full", 100 << 30, 109 << 20, "critical"},
		{"percent critical", 1 << 40, 15 << 30, "critical"},
		{"low absolute", 50 << 30, 5 << 30, "low"},
		{"low percent", 1 << 40, 50 << 30, "low"},
		{"healthy", 100 << 30, 20 << 30, "ok"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskSpaceStatus(tt.total, tt.free); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteDiskUsageUnavailableGuest(t *testing.T) {
	var out bytes.Buffer
	r := diskUsageReport{VM: "test", Host: diskSpace{FreeBytes: 30 << 30, Status: "ok"}, Warnings: []string{"guest capacity unavailable"}}
	if err := writeDiskUsage(&out, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "guest capacity unavailable") || strings.Contains(out.String(), "Guest: 0") {
		t.Fatalf("misleading report: %s", out.String())
	}
}

func TestDiskRecoveryBlocker(t *testing.T) {
	for _, tt := range []struct{ name, out, store, want string }{
		{"after", "3: Apple_APFS_Recovery Container disk2 5.4 GB disk0s3", "disk0s2", "disk0s3"},
		{"before", "1: Apple_APFS_Recovery Container disk2 5.4 GB disk0s1", "disk0s2", ""},
		{"other disk", "3: Apple_APFS_Recovery Container disk2 5.4 GB disk1s3", "disk0s2", ""},
		{"malformed", "3: Apple_APFS_Recovery", "invalid", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskRecoveryBlocker(tt.out, tt.store); got != tt.want {
				t.Fatalf("blocker = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitDiskPartitionInvalid(t *testing.T) {
	for _, id := range []string{"disk", "disks", "diskas2", "disk0", "other0s2"} {
		if _, _, ok := splitDiskPartition(id); ok {
			t.Fatalf("accepted invalid partition %q", id)
		}
	}
}

func TestDiskCleanScript(t *testing.T) {
	for _, tt := range []struct {
		name, cache string
		apply       bool
		wantFlag    string
	}{
		{"preview", "go-build", false, ""},
		{"build", "go-build", true, "-cache"},
		{"modules", "go-modules", true, "-modcache"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cache := filepath.Join(dir, "cache with spaces")
			log := filepath.Join(dir, "calls")
			if err := os.Mkdir(cache, 0700); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(cache, "entry")
			if err := os.WriteFile(entry, []byte("cache data"), 0600); err != nil {
				t.Fatal(err)
			}
			tool := `#!/bin/sh
printf '%s %s\n' "$1" "$2" >> "$COVE_TEST_LOG"
case "$1" in
 env) printf '%s\n' "$COVE_TEST_CACHE" ;;
 clean) rm -f "$COVE_TEST_CACHE/entry" ;;
 *) exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte(tool), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/sh", "-c", diskCleanScript(tt.cache, "user", tt.apply))
			cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin", "COVE_TEST_CACHE="+cache, "COVE_TEST_LOG="+log)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("script: %v: %s", err, out)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if tt.apply {
				if !strings.Contains(string(calls), "clean "+tt.wantFlag) {
					t.Fatalf("wrong cleanup: %s", calls)
				}
				if _, err := os.Stat(entry); !os.IsNotExist(err) {
					t.Fatalf("cache not cleared: %v", err)
				}
			} else {
				if strings.Contains(string(calls), "clean") {
					t.Fatalf("preview deleted cache: %s", calls)
				}
				if _, err := os.Stat(entry); err != nil {
					t.Fatalf("preview changed cache: %v", err)
				}
			}
		})
	}
}

func TestCollectDiskUsageStoppedGuest(t *testing.T) {
	r, err := collectDiskUsage("stopped", makeTestVMDir(t), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Guest != nil || len(r.Warnings) == 0 || r.Host.TotalBytes == 0 || r.ImageFileBytes == 0 {
		t.Fatalf("invalid stopped report: %+v", r)
	}
}

func TestDiskCleanScriptRejectsUnsafeCache(t *testing.T) {
	dir := t.TempDir()
	tool := `#!/bin/sh
case "$1" in
 env) printf '/\n' ;;
 *) echo 'unexpected cleanup' >&2; exit 99 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(tool), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", diskCleanScript("go-build", "user", true))
	cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "refusing unsafe cache root") {
		t.Fatalf("unsafe cache result: %v: %s", err, out)
	}
}
