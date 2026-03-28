//go:build integration && darwin && arm64

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

var (
	flagIntegrationDiskBenchSizes = flag.String("integration.disk-bench-sizes", envOrString("VZ_TEST_DISK_BENCH_SIZES", defaultDiskBenchSizes), "comma-separated disk benchmark sizes (for example: 8MiB,64MiB)")
	flagIntegrationDiskBenchTag   = flag.String("integration.disk-bench-shared-tag", strings.TrimSpace(os.Getenv("VZ_TEST_DISK_BENCH_SHARED_TAG")), "shared folder tag to benchmark (defaults to the first configured writable shared folder)")
	flagIntegrationDiskBenchDisk  = flag.String("integration.disk-bench-disk-label", envOrString("VZ_TEST_DISK_BENCH_DISK_LABEL", "default"), "disk configuration label to embed in benchmark names for benchstat")
	flagIntegrationDiskBenchMount = flag.String("integration.disk-bench-mount-label", envOrString("VZ_TEST_DISK_BENCH_MOUNT_LABEL", "default"), "mount configuration label to embed in benchmark names for benchstat")
)

const diskBenchTimeout = 10 * time.Minute

type diskBenchHostTarget struct {
	Location string
	Tag      string
	Path     string
}

type diskBenchGuestTarget struct {
	Location string
	Tag      string
	Path     string
	HostPath string
	ReadOnly bool
}

// BenchmarkDiskIO emits slash-separated key=value sub-benchmark names so
// benchstat can pivot on /scope,/location,/op,/size and compare /disk,/mount
// labels across different experiment runs.
func BenchmarkDiskIO(b *testing.B) {
	vm := acquireIntegrationVM(b)
	b.Cleanup(func() { vm.cleanupTB(b) })

	sizes, err := parseDiskBenchSizes(*flagIntegrationDiskBenchSizes)
	if err != nil {
		b.Fatalf("parse disk benchmark sizes: %v", err)
	}
	if testing.Short() && len(sizes) > 1 {
		sizes = sizes[:1]
	}

	hostTargets, guestTargets := resolveDiskBenchTargets(b, vm)
	for _, target := range hostTargets {
		for _, size := range sizes {
			size := size
			b.Run(diskBenchName("host", target.Location, target.Tag, "write-sync", size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
				benchmarkHostWriteSync(b, target.Path, size)
			})
			b.Run(diskBenchName("host", target.Location, target.Tag, "read", size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
				benchmarkHostRead(b, target.Path, size)
			})
		}
	}

	for _, target := range guestTargets {
		for _, size := range sizes {
			size := size
			if !target.ReadOnly {
				b.Run(diskBenchName("guest", target.Location, target.Tag, "write-sync", size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
					benchmarkGuestWriteSync(b, vm, target, size)
				})
			}
			b.Run(diskBenchName("guest", target.Location, target.Tag, "read", size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
				benchmarkGuestRead(b, vm, target, size)
			})
		}
	}
}

func envOrString(name, defaultValue string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return defaultValue
}

func resolveDiskBenchTargets(b *testing.B, vm *testVM) ([]diskBenchHostTarget, []diskBenchGuestTarget) {
	b.Helper()

	vmBenchRoot := filepath.Join(vm.dir, ".vz-bench", "host-vm-dir")
	hostTargets := []diskBenchHostTarget{{
		Location: "vm-dir",
		Tag:      "none",
		Path:     vmBenchRoot,
	}}
	guestTargets := []diskBenchGuestTarget{{
		Location: "guest-local",
		Tag:      "none",
		Path:     "/private/tmp/vz-disk-bench-local",
	}}

	sharedFolders := LoadSharedFolders(vm.dir)
	if len(sharedFolders) == 0 {
		b.Log("disk benchmark: no shared folders configured; skipping shared-folder cases")
		return hostTargets, guestTargets
	}

	shared, sharedHostPath, ok := selectDiskBenchSharedFolder(b, sharedFolders, *flagIntegrationDiskBenchTag, vm.name)
	if !ok {
		if tag := strings.TrimSpace(*flagIntegrationDiskBenchTag); tag != "" {
			b.Logf("disk benchmark: shared folder tag %q not usable; skipping shared-folder cases", tag)
		} else {
			b.Log("disk benchmark: no writable shared folder source found; skipping shared-folder cases")
		}
		return hostTargets, guestTargets
	}
	hostTargets = append(hostTargets, diskBenchHostTarget{
		Location: "shared-host",
		Tag:      shared.Tag,
		Path:     sharedHostPath,
	})

	if _, err := mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint); err != nil {
		b.Logf("disk benchmark: mount shared folders in guest: %v", err)
		return hostTargets, guestTargets
	}

	guestSharedRoot, ok := resolveGuestSharedRoot(b, vm, shared.Tag)
	if !ok {
		b.Logf("disk benchmark: guest shared folder tag %q not present; skipping guest shared-folder cases", shared.Tag)
		return hostTargets, guestTargets
	}

	guestTargets = append(guestTargets, diskBenchGuestTarget{
		Location: "shared-folder",
		Tag:      shared.Tag,
		Path:     filepath.Join(guestSharedRoot, ".vz-bench", sanitizeBenchConfigValue(vm.name)),
		HostPath: sharedHostPath,
		ReadOnly: shared.ReadOnly,
	})
	return hostTargets, guestTargets
}

func resolveGuestSharedRoot(b *testing.B, vm *testVM, tag string) (string, bool) {
	b.Helper()

	candidates := []string{
		filepath.Join(defaultSharedFoldersMountPoint, tag),
		filepath.Join("/Volumes", tag),
	}
	for _, candidate := range candidates {
		if guestDirExists(b, vm, candidate) {
			return candidate, true
		}
	}
	return "", false
}

func selectDiskBenchSharedFolder(b *testing.B, folders []SharedFolderEntry, wantTag, vmName string) (SharedFolderEntry, string, bool) {
	wantTag = strings.TrimSpace(wantTag)
	if wantTag != "" {
		for _, folder := range folders {
			if folder.Tag != wantTag {
				continue
			}
			root, ok := prepareSharedBenchRoot(b, folder, vmName)
			return folder, root, ok
		}
		return SharedFolderEntry{}, "", false
	}
	for _, folder := range folders {
		if folder.ReadOnly {
			continue
		}
		root, ok := prepareSharedBenchRoot(b, folder, vmName)
		if ok {
			return folder, root, true
		}
	}
	return SharedFolderEntry{}, "", false
}

func prepareSharedBenchRoot(b *testing.B, folder SharedFolderEntry, vmName string) (string, bool) {
	b.Helper()

	root := filepath.Join(folder.Path, ".vz-bench", sanitizeBenchConfigValue(vmName))
	if err := os.MkdirAll(root, 0755); err != nil {
		b.Logf("disk benchmark: shared folder tag %q host root %q unusable: %v", folder.Tag, root, err)
		return "", false
	}
	return root, true
}

func benchmarkHostWriteSync(b *testing.B, dir string, size diskBenchSize) {
	b.Helper()

	if err := os.MkdirAll(dir, 0755); err != nil {
		b.Fatalf("mkdir %q: %v", dir, err)
	}
	path := filepath.Join(dir, "write-"+size.Label+".bin")
	b.Cleanup(func() { _ = os.Remove(path) })
	buf := make([]byte, 1<<20)

	b.SetBytes(size.Bytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := writeSizedFileBuffer(path, size.Bytes, buf); err != nil {
			b.Fatalf("write %q: %v", path, err)
		}
	}
}

func benchmarkHostRead(b *testing.B, dir string, size diskBenchSize) {
	b.Helper()

	if err := os.MkdirAll(dir, 0755); err != nil {
		b.Fatalf("mkdir %q: %v", dir, err)
	}
	path := filepath.Join(dir, "read-"+size.Label+".bin")
	buf := make([]byte, 1<<20)
	if err := writeSizedFileBuffer(path, size.Bytes, buf); err != nil {
		b.Fatalf("prepare read file %q: %v", path, err)
	}
	b.Cleanup(func() { _ = os.Remove(path) })

	b.SetBytes(size.Bytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := readSizedFileBuffer(path, buf); err != nil {
			b.Fatalf("read %q: %v", path, err)
		}
	}
}

func benchmarkGuestWriteSync(b *testing.B, vm *testVM, target diskBenchGuestTarget, size diskBenchSize) {
	b.Helper()

	guestMkdirAll(b, vm, target.Path)
	path := filepath.Join(target.Path, "write-"+size.Label+".bin")
	b.Cleanup(func() { cleanupGuestPathsTB(b, vm, path, target.Path) })

	b.SetBytes(size.Bytes)
	script := guestWriteBenchScript(path, size.Bytes, b.N)
	b.ResetTimer()
	agentExecExpectCodeTimeoutTB(b, vm, diskBenchTimeout, 0, "/bin/sh", "-lc", script)
}

func benchmarkGuestRead(b *testing.B, vm *testVM, target diskBenchGuestTarget, size diskBenchSize) {
	b.Helper()

	guestMkdirAll(b, vm, target.Path)
	path := filepath.Join(target.Path, "read-"+size.Label+".bin")
	if target.HostPath != "" {
		if err := os.MkdirAll(target.HostPath, 0755); err != nil {
			b.Fatalf("mkdir %q: %v", target.HostPath, err)
		}
		hostPath := filepath.Join(target.HostPath, "read-"+size.Label+".bin")
		if err := writeSizedFile(hostPath, size.Bytes); err != nil {
			b.Fatalf("prepare host shared read file %q: %v", hostPath, err)
		}
		b.Cleanup(func() { _ = os.Remove(hostPath) })
	} else {
		agentExecExpectCodeTimeoutTB(b, vm, diskBenchTimeout, 0, "/bin/sh", "-lc", guestPrepareFileScript(path, size.Bytes))
		b.Cleanup(func() { cleanupGuestPathsTB(b, vm, path, target.Path) })
	}

	b.SetBytes(size.Bytes)
	script := guestReadBenchScript(path, size.Bytes, b.N)
	b.ResetTimer()
	agentExecExpectCodeTimeoutTB(b, vm, diskBenchTimeout, 0, "/bin/sh", "-lc", script)
}

func guestMkdirAll(b *testing.B, vm *testVM, path string) {
	b.Helper()
	agentExecExpectCodeTimeoutTB(b, vm, 2*time.Minute, 0, "/bin/mkdir", "-p", path)
}

func guestDirExists(b *testing.B, vm *testVM, path string) bool {
	b.Helper()
	result := agentExecResultTimeoutTB(b, vm, 30*time.Second, "/bin/test", "-d", path)
	return result.GetExitCode() == 0
}

func cleanupGuestPathsTB(tb testing.TB, vm *testVM, paths ...string) {
	tb.Helper()

	if len(paths) == 0 {
		return
	}
	args := append([]string{"/bin/rm", "-rf"}, paths...)
	result := agentExecResultTimeoutTB(tb, vm, 2*time.Minute, args...)
	if result.GetExitCode() != 0 {
		tb.Logf("cleanup %v: exit %d: %s", paths, result.GetExitCode(), result.GetStderr())
	}
}

func agentExecResultTimeoutTB(tb testing.TB, vm *testVM, timeout time.Duration, args ...string) *controlpb.AgentExecResponse {
	tb.Helper()

	req := &controlpb.ControlRequest{
		Type:      "agent-exec",
		AuthToken: vm.token,
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: args},
		},
	}
	resp, err := ctlSendRequest(vm.sock, req, timeout, req.Type)
	if err != nil {
		tb.Fatalf("agent-exec %v: %v", args, err)
	}
	if !resp.Success {
		tb.Fatalf("agent-exec %v: %s", args, resp.Error)
	}
	result := resp.GetAgentExecResult()
	if result == nil {
		tb.Fatalf("agent-exec %v: missing typed response", args)
	}
	return result
}

func agentExecExpectCodeTimeoutTB(tb testing.TB, vm *testVM, timeout time.Duration, want int32, args ...string) *controlpb.AgentExecResponse {
	tb.Helper()

	result := agentExecResultTimeoutTB(tb, vm, timeout, args...)
	if result.GetExitCode() != want {
		tb.Fatalf("agent-exec %v: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", args, result.GetExitCode(), want, result.GetStdout(), result.GetStderr())
	}
	return result
}

func guestWriteBenchScript(path string, sizeBytes int64, iterations int) string {
	return strings.Join([]string{
		"set -e",
		fmt.Sprintf("i=0; while [ \"$i\" -lt %d ]; do", iterations),
		fmt.Sprintf("  /bin/dd if=/dev/zero of=%s bs=%d count=1 >/dev/null 2>&1", shQuote(path), sizeBytes),
		"  /bin/sync",
		"  i=$((i+1))",
		"done",
		fmt.Sprintf("/bin/rm -f %s", shQuote(path)),
	}, "\n")
}

func guestReadBenchScript(path string, sizeBytes int64, iterations int) string {
	return strings.Join([]string{
		"set -e",
		fmt.Sprintf("i=0; while [ \"$i\" -lt %d ]; do", iterations),
		fmt.Sprintf("  /bin/dd if=%s of=/dev/null bs=%d count=1 >/dev/null 2>&1", shQuote(path), sizeBytes),
		"  i=$((i+1))",
		"done",
	}, "\n")
}

func guestPrepareFileScript(path string, sizeBytes int64) string {
	return strings.Join([]string{
		"set -e",
		fmt.Sprintf("  /bin/dd if=/dev/zero of=%s bs=%d count=1 >/dev/null 2>&1", shQuote(path), sizeBytes),
		"  /bin/sync",
	}, "\n")
}

func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeSizedFile(path string, sizeBytes int64) error {
	return writeSizedFileBuffer(path, sizeBytes, make([]byte, 1<<20))
}

func writeSizedFileBuffer(path string, sizeBytes int64, buf []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	for remaining := sizeBytes; remaining > 0; {
		n := int64(len(buf))
		if remaining < n {
			n = remaining
		}
		if _, err := f.Write(buf[:n]); err != nil {
			f.Close()
			return err
		}
		remaining -= n
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readSizedFile(path string) error {
	return readSizedFileBuffer(path, make([]byte, 1<<20))
}

func readSizedFileBuffer(path string, buf []byte) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.CopyBuffer(io.Discard, f, buf)
	return err
}
