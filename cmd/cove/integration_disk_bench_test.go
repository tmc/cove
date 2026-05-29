//go:build integration && darwin && arm64

package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
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

type diskBenchTreeFixture struct {
	Root      string
	HostRoot  string
	Files     []string
	Links     []string
	Paths     []string
	DataBytes int64
}

type diskBenchTreeSpec struct {
	fileCount int
	fileSize  int
	dirCount  int
	linkCount int
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

func BenchmarkDiskMetadata(b *testing.B) {
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
	ops := []string{"readdir", "find", "stat", "small-read", "readlink"}

	for _, target := range hostTargets {
		for _, size := range sizes {
			fixture := prepareHostDiskBenchFixture(b, filepath.Join(target.Path, "metadata-"+size.Label), size)
			for _, op := range ops {
				op := op
				b.Run(diskBenchName("host", target.Location, target.Tag, op, size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
					benchmarkHostMetadata(b, fixture, op)
				})
			}
		}
	}

	for _, target := range guestTargets {
		for _, size := range sizes {
			fixture := prepareGuestDiskBenchFixture(b, vm, target, "metadata-"+size.Label, size)
			for _, op := range ops {
				op := op
				b.Run(diskBenchName("guest", target.Location, target.Tag, op, size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
					benchmarkGuestMetadata(b, vm, fixture, op)
				})
			}
		}
	}
}

func BenchmarkDiskIngest(b *testing.B) {
	vm := acquireIntegrationVM(b)
	b.Cleanup(func() { vm.cleanupTB(b) })

	sizes, err := parseDiskBenchSizes(*flagIntegrationDiskBenchSizes)
	if err != nil {
		b.Fatalf("parse disk benchmark sizes: %v", err)
	}
	if testing.Short() && len(sizes) > 1 {
		sizes = sizes[:1]
	}

	_, guestTargets := resolveDiskBenchTargets(b, vm)
	localTarget, sharedTarget, ok := selectDiskBenchIngestTargets(guestTargets)
	if !ok {
		b.Skip("disk ingest benchmark requires a writable shared-folder target")
	}

	for _, size := range sizes {
		size := size
		source := prepareSharedDiskBenchSource(b, vm, sharedTarget, "ingest-"+size.Label, size)

		b.Run(diskBenchName("guest", localTarget.Location, sharedTarget.Tag, "ingest-agent-cp", size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
			dest := filepath.Join(localTarget.Path, "ingest-agent-cp-"+size.Label)
			benchmarkGuestIngestAgentCopy(b, vm, source.HostRoot, dest, source.DataBytes)
		})

		b.Run(diskBenchName("guest", localTarget.Location, sharedTarget.Tag, "ingest-virtiofs-copy", size, *flagIntegrationDiskBenchDisk, *flagIntegrationDiskBenchMount), func(b *testing.B) {
			dest := filepath.Join(localTarget.Path, "ingest-virtiofs-copy-"+size.Label)
			benchmarkGuestIngestVirtioFSCopy(b, vm, source.Root, dest, source.DataBytes)
		})
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

func benchmarkHostMetadata(b *testing.B, fixture *diskBenchTreeFixture, op string) {
	b.Helper()

	b.SetBytes(fixture.DataBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		switch op {
		case "readdir":
			err = hostReadDirTree(fixture.Root)
		case "find":
			err = filepath.WalkDir(fixture.Root, func(_ string, _ fs.DirEntry, err error) error { return err })
		case "stat":
			err = hostStatPaths(fixture.Paths)
		case "small-read":
			err = hostSmallReadFiles(fixture.Files)
		case "readlink":
			err = hostReadLinks(fixture.Links)
		default:
			b.Fatalf("unknown metadata benchmark op %q", op)
		}
		if err != nil {
			b.Fatalf("%s %q: %v", op, fixture.Root, err)
		}
	}
}

func benchmarkGuestMetadata(b *testing.B, vm *testVM, fixture *diskBenchTreeFixture, op string) {
	b.Helper()

	script, err := guestMetadataBenchScript(fixture.Root, op, b.N)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(fixture.DataBytes)
	b.ResetTimer()
	agentExecExpectCodeTimeoutTB(b, vm, diskBenchTimeout, 0, "/bin/sh", "-lc", script)
}

func benchmarkGuestIngestAgentCopy(b *testing.B, vm *testVM, hostRoot, guestRoot string, bytes int64) {
	b.Helper()

	guestMkdirAll(b, vm, filepath.Dir(guestRoot))
	b.Cleanup(func() { cleanupGuestPathsTB(b, vm, guestRoot) })
	b.SetBytes(bytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cleanupGuestPathsTB(b, vm, guestRoot)
		b.StartTimer()
		agentCopyDirToGuestTB(b, vm, hostRoot, guestRoot, true, diskBenchTimeout)
	}
}

func benchmarkGuestIngestVirtioFSCopy(b *testing.B, vm *testVM, sourceRoot, guestRoot string, bytes int64) {
	b.Helper()

	guestMkdirAll(b, vm, filepath.Dir(guestRoot))
	script := fmt.Sprintf("/usr/bin/ditto %s %s", shQuote(sourceRoot), shQuote(guestRoot))
	b.Cleanup(func() { cleanupGuestPathsTB(b, vm, guestRoot) })
	b.SetBytes(bytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cleanupGuestPathsTB(b, vm, guestRoot)
		b.StartTimer()
		agentExecExpectCodeTimeoutTB(b, vm, diskBenchTimeout, 0, "/bin/sh", "-lc", script)
	}
}

func prepareHostDiskBenchFixture(b *testing.B, root string, size diskBenchSize) *diskBenchTreeFixture {
	b.Helper()

	fixture, err := writeDiskBenchTree(root, size)
	if err != nil {
		b.Fatalf("prepare host benchmark fixture %q: %v", root, err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(root) })
	return fixture
}

func prepareGuestDiskBenchFixture(b *testing.B, vm *testVM, target diskBenchGuestTarget, prefix string, size diskBenchSize) *diskBenchTreeFixture {
	b.Helper()

	guestRoot := filepath.Join(target.Path, prefix)
	if target.HostPath != "" {
		hostRoot := filepath.Join(target.HostPath, prefix)
		fixture, err := writeDiskBenchTree(hostRoot, size)
		if err != nil {
			b.Fatalf("prepare shared benchmark fixture %q: %v", hostRoot, err)
		}
		b.Cleanup(func() { _ = os.RemoveAll(hostRoot) })
		waitForGuestDirTB(b, vm, guestRoot, 30*time.Second)
		return &diskBenchTreeFixture{
			Root:      guestRoot,
			HostRoot:  hostRoot,
			Files:     fixture.Files,
			Links:     fixture.Links,
			Paths:     fixture.Paths,
			DataBytes: fixture.DataBytes,
		}
	}

	hostRoot := filepath.Join(b.TempDir(), prefix)
	fixture, err := writeDiskBenchTree(hostRoot, size)
	if err != nil {
		b.Fatalf("prepare guest benchmark fixture %q: %v", hostRoot, err)
	}
	cleanupGuestPathsTB(b, vm, guestRoot)
	agentCopyDirToGuestTB(b, vm, hostRoot, guestRoot, true, diskBenchTimeout)
	b.Cleanup(func() { cleanupGuestPathsTB(b, vm, guestRoot) })
	return &diskBenchTreeFixture{
		Root:      guestRoot,
		HostRoot:  hostRoot,
		DataBytes: fixture.DataBytes,
	}
}

func prepareSharedDiskBenchSource(b *testing.B, vm *testVM, target diskBenchGuestTarget, prefix string, size diskBenchSize) *diskBenchTreeFixture {
	b.Helper()

	hostRoot := filepath.Join(target.HostPath, prefix)
	fixture, err := writeDiskBenchTree(hostRoot, size)
	if err != nil {
		b.Fatalf("prepare shared source fixture %q: %v", hostRoot, err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(hostRoot) })

	guestRoot := filepath.Join(target.Path, prefix)
	waitForGuestDirTB(b, vm, guestRoot, 30*time.Second)
	return &diskBenchTreeFixture{
		Root:      guestRoot,
		HostRoot:  hostRoot,
		DataBytes: fixture.DataBytes,
	}
}

func selectDiskBenchIngestTargets(targets []diskBenchGuestTarget) (diskBenchGuestTarget, diskBenchGuestTarget, bool) {
	var local, shared diskBenchGuestTarget
	for _, target := range targets {
		switch target.Location {
		case "guest-local":
			local = target
		case "shared-folder":
			if !target.ReadOnly {
				shared = target
			}
		}
	}
	if local.Path == "" || shared.Path == "" || shared.HostPath == "" {
		return diskBenchGuestTarget{}, diskBenchGuestTarget{}, false
	}
	return local, shared, true
}

func waitForGuestDirTB(tb testing.TB, vm *testVM, path string, timeout time.Duration) {
	tb.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if guestDirExistsTB(tb, vm, path) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	tb.Fatalf("guest dir %q not visible after %s", path, timeout)
}

func guestDirExistsTB(tb testing.TB, vm *testVM, path string) bool {
	tb.Helper()
	result := agentExecResultTimeoutTB(tb, vm, 30*time.Second, "/bin/test", "-d", path)
	return result.GetExitCode() == 0
}

func hostReadDirTree(root string) error {
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				stack = append(stack, filepath.Join(dir, entry.Name()))
			}
		}
	}
	return nil
}

func hostStatPaths(paths []string) error {
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			return err
		}
	}
	return nil
}

func hostSmallReadFiles(paths []string) error {
	buf := make([]byte, 256)
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, readErr := io.ReadFull(f, buf)
		closeErr := f.Close()
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func hostReadLinks(paths []string) error {
	for _, path := range paths {
		if _, err := os.Readlink(path); err != nil {
			return err
		}
	}
	return nil
}

func guestMetadataBenchScript(root, op string, iterations int) (string, error) {
	command := ""
	switch op {
	case "readdir":
		command = fmt.Sprintf("/bin/ls -1R %s >/dev/null", shQuote(root))
	case "find":
		command = fmt.Sprintf("/usr/bin/find -P %s -print >/dev/null", shQuote(root))
	case "stat":
		command = fmt.Sprintf("/usr/bin/find -P %s -exec /usr/bin/stat -f %%N {} + >/dev/null", shQuote(root))
	case "small-read":
		command = fmt.Sprintf("/usr/bin/find -P %s -type f -exec /bin/cat {} + >/dev/null", shQuote(root))
	case "readlink":
		command = fmt.Sprintf("/usr/bin/find -P %s -type l -exec /bin/readlink {} + >/dev/null", shQuote(root))
	default:
		return "", fmt.Errorf("unknown metadata benchmark op %q", op)
	}

	return strings.Join([]string{
		"set -e",
		fmt.Sprintf("i=0; while [ \"$i\" -lt %d ]; do", iterations),
		"  " + command,
		"  i=$((i+1))",
		"done",
	}, "\n"), nil
}

func agentCopyDirToGuestTB(tb testing.TB, vm *testVM, hostPath, guestPath string, overwrite bool, timeout time.Duration) {
	tb.Helper()

	req := &controlpb.ControlRequest{
		Type:      "agent-cp",
		AuthToken: vm.token,
		Command: &controlpb.ControlRequest_AgentCp{
			AgentCp: &controlpb.AgentCopyCommand{
				HostPath:  hostPath,
				GuestPath: guestPath,
				ToGuest:   true,
				Overwrite: overwrite,
			},
		},
	}
	resp, err := ctlSendRequest(vm.sock, req, timeout, req.Type)
	if err != nil {
		tb.Fatalf("agent-cp dir %s -> %s: %v", hostPath, guestPath, err)
	}
	if !resp.Success {
		tb.Fatalf("agent-cp dir %s -> %s: %s", hostPath, guestPath, resp.Error)
	}
}

func writeDiskBenchTree(root string, size diskBenchSize) (*diskBenchTreeFixture, error) {
	if err := os.RemoveAll(root); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}

	spec := diskBenchTreeSpecForSize(size)
	files := make([]string, 0, spec.fileCount)
	for i := 0; i < spec.fileCount; i++ {
		dir := filepath.Join(root, fmt.Sprintf("dir-%03d", i%spec.dirCount), fmt.Sprintf("shard-%02d", (i/spec.dirCount)%8))
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("file-%05d.dat", i))
		if err := writeSizedFile(path, int64(spec.fileSize)); err != nil {
			return nil, err
		}
		files = append(files, path)
	}

	linkDir := filepath.Join(root, "links")
	for i := 0; i < spec.linkCount; i++ {
		dir := filepath.Join(linkDir, fmt.Sprintf("set-%02d", i%16))
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		linkPath := filepath.Join(dir, fmt.Sprintf("link-%05d", i))
		target := files[i%len(files)]
		rel, err := filepath.Rel(dir, target)
		if err != nil {
			return nil, err
		}
		if err := os.Symlink(rel, linkPath); err != nil {
			return nil, err
		}
	}

	fixture := &diskBenchTreeFixture{Root: root, HostRoot: root}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		fixture.Paths = append(fixture.Paths, path)
		if entry.Type()&os.ModeSymlink != 0 {
			fixture.Links = append(fixture.Links, path)
			return nil
		}
		if !entry.IsDir() {
			fixture.Files = append(fixture.Files, path)
			info, err := entry.Info()
			if err != nil {
				return err
			}
			fixture.DataBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fixture, nil
}

func diskBenchTreeSpecForSize(size diskBenchSize) diskBenchTreeSpec {
	fileCount := int(size.Bytes / (16 << 10))
	if fileCount < 256 {
		fileCount = 256
	}
	if fileCount > 4096 {
		fileCount = 4096
	}
	fileSize := int((size.Bytes + int64(fileCount) - 1) / int64(fileCount))
	if fileSize < 1024 {
		fileSize = 1024
	}
	dirCount := fileCount / 32
	if dirCount < 8 {
		dirCount = 8
	}
	if dirCount > 128 {
		dirCount = 128
	}
	linkCount := fileCount / 8
	if linkCount < 16 {
		linkCount = 16
	}
	if linkCount > 512 {
		linkCount = 512
	}
	return diskBenchTreeSpec{
		fileCount: fileCount,
		fileSize:  fileSize,
		dirCount:  dirCount,
		linkCount: linkCount,
	}
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
