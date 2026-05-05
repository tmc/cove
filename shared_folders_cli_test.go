package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
	"google.golang.org/protobuf/encoding/protojson"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestAddSharedFolderEntry(t *testing.T) {
	vmDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "Data Set")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}

	entry, added, err := addSharedFolderEntry(vmDir, hostDir, "", false)
	if err != nil {
		t.Fatalf("addSharedFolderEntry() error = %v", err)
	}
	if !added {
		t.Fatalf("expected added=true")
	}
	wantPath := resolvePath(hostDir)
	if entry.Path != wantPath {
		t.Fatalf("path = %q, want %q", entry.Path, wantPath)
	}
	if entry.Tag != "data-set" {
		t.Fatalf("tag = %q, want %q", entry.Tag, "data-set")
	}

	folders := LoadSharedFolders(vmDir)
	if len(folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1", len(folders))
	}
}

func TestUniqueTagSanitizesAndDeduplicates(t *testing.T) {
	existing := []SharedFolderEntry{{Tag: "data-set"}}
	if got := uniqueTag("Data Set", existing); got != "data-set-2" {
		t.Fatalf("uniqueTag() = %q, want %q", got, "data-set-2")
	}
	if got := uniqueTag("   ", nil); got != "share" {
		t.Fatalf("uniqueTag(blank) = %q, want %q", got, "share")
	}
	if got := uniqueTag("%%% ", []SharedFolderEntry{{Tag: "share"}}); got != "share-2" {
		t.Fatalf("uniqueTag(symbols) = %q, want %q", got, "share-2")
	}
}

func TestAddSharedFolderEntryDuplicatePath(t *testing.T) {
	vmDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "data", false); err != nil {
		t.Fatalf("first add error = %v", err)
	}
	entry, added, err := addSharedFolderEntry(vmDir, hostDir, "other", true)
	if err != nil {
		t.Fatalf("second add error = %v", err)
	}
	if added {
		t.Fatalf("expected added=false for duplicate path")
	}
	if entry.Tag != "data" {
		t.Fatalf("duplicate should keep original tag, got %q", entry.Tag)
	}

	folders := LoadSharedFolders(vmDir)
	if len(folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1", len(folders))
	}
}

func TestAddSharedFolderEntryTagCollision(t *testing.T) {
	vmDir := t.TempDir()
	hostA := filepath.Join(t.TempDir(), "a")
	hostB := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(hostA, 0755); err != nil {
		t.Fatalf("mkdir hostA: %v", err)
	}
	if err := os.MkdirAll(hostB, 0755); err != nil {
		t.Fatalf("mkdir hostB: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostA, "shared", false); err != nil {
		t.Fatalf("first add error = %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostB, "shared", false); err == nil {
		t.Fatalf("expected tag collision error")
	}
}

func TestDefaultSharedFolderMountPoint(t *testing.T) {
	macDir := shortSharedFolderVMDir(t)
	linuxDir := linuxTestVMDir(t)

	tests := []struct {
		name string
		dir  string
		tag  string
		want string
	}{
		{name: "macos", dir: macDir, tag: "work", want: "/Volumes/My Shared Files/work"},
		{name: "linux", dir: linuxDir, tag: "work", want: "/mnt/work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultSharedFolderMountPoint(tt.dir, tt.tag); got != tt.want {
				t.Fatalf("defaultSharedFolderMountPoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultSharedFoldersMountRoot(t *testing.T) {
	macDir := shortSharedFolderVMDir(t)
	linuxDir := linuxTestVMDir(t)

	if got := defaultSharedFoldersMountRoot(macDir); got != defaultSharedFoldersMountPoint {
		t.Fatalf("macOS mount root = %q, want %q", got, defaultSharedFoldersMountPoint)
	}
	if got := defaultSharedFoldersMountRoot(linuxDir); got != "" {
		t.Fatalf("Linux mount root = %q, want empty", got)
	}
}

func TestRemoveSharedFolderEntry(t *testing.T) {
	vmDir := t.TempDir()
	hostA := filepath.Join(t.TempDir(), "a")
	hostB := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(hostA, 0755); err != nil {
		t.Fatalf("mkdir hostA: %v", err)
	}
	if err := os.MkdirAll(hostB, 0755); err != nil {
		t.Fatalf("mkdir hostB: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostA, "one", false); err != nil {
		t.Fatalf("add hostA: %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostB, "two", false); err != nil {
		t.Fatalf("add hostB: %v", err)
	}

	removed, err := removeSharedFolderEntry(vmDir, "one")
	if err != nil {
		t.Fatalf("remove by tag: %v", err)
	}
	if !removed {
		t.Fatalf("expected removed=true")
	}

	folders := LoadSharedFolders(vmDir)
	if len(folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1", len(folders))
	}
	if folders[0].Tag != "two" {
		t.Fatalf("remaining tag = %q, want %q", folders[0].Tag, "two")
	}
}

func TestRemoveSharedFolderEntryByPath(t *testing.T) {
	vmDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "workspace", false); err != nil {
		t.Fatalf("addSharedFolderEntry() error = %v", err)
	}

	removed, err := removeSharedFolderEntry(vmDir, hostDir)
	if err != nil {
		t.Fatalf("removeSharedFolderEntry() error = %v", err)
	}
	if !removed {
		t.Fatalf("expected removed=true")
	}
	if folders := LoadSharedFolders(vmDir); len(folders) != 0 {
		t.Fatalf("len(folders) = %d, want 0", len(folders))
	}
}

func TestExpectedSharedFolderTagsFiltersInvalidEntries(t *testing.T) {
	vmDir := t.TempDir()
	validDir := filepath.Join(t.TempDir(), "valid")
	otherDir := filepath.Join(t.TempDir(), "other")
	missingDir := filepath.Join(t.TempDir(), "missing")
	if err := os.MkdirAll(validDir, 0755); err != nil {
		t.Fatalf("mkdir valid dir: %v", err)
	}
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatalf("mkdir other dir: %v", err)
	}

	folders := []SharedFolderEntry{
		{Path: validDir, Tag: "valid", ReadOnly: false},
		{Path: otherDir, Tag: "   ", ReadOnly: false},
		{Path: missingDir, Tag: "missing", ReadOnly: false},
		{Path: otherDir, Tag: "other", ReadOnly: true},
	}
	if err := saveSharedFolders(vmDir, folders); err != nil {
		t.Fatalf("saveSharedFolders() error = %v", err)
	}

	got := expectedSharedFolderTags(vmDir)
	if len(got) != 2 {
		t.Fatalf("len(expectedSharedFolderTags()) = %d, want 2 (%v)", len(got), got)
	}
	if got[0] != "valid" || got[1] != "other" {
		t.Fatalf("expectedSharedFolderTags() = %v, want [valid other]", got)
	}
}

func TestNormalizeSharedFolderPathRejectsFiles(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := normalizeSharedFolderPath(filePath); err == nil {
		t.Fatalf("normalizeSharedFolderPath(%q): got nil error, want error", filePath)
	}
}

func TestApplySharedFoldersAndPrintWithoutRunningVM(t *testing.T) {
	if err := applySharedFoldersAndPrint(t.TempDir()); err != nil {
		t.Fatalf("applySharedFoldersAndPrint() error = %v, want nil", err)
	}
}

func TestSharedFolderCommandVMDirHonorsVMFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "ubuntu-gh-runner-headed"
	vmDir = "default"

	got, err := sharedFolderCommandVMDir()
	if err != nil {
		t.Fatalf("sharedFolderCommandVMDir() error = %v", err)
	}
	want := resolvePath(filepath.Join(vmconfig.BaseDir(), "ubuntu-gh-runner-headed"))
	if got != want {
		t.Fatalf("sharedFolderCommandVMDir() = %q, want %q", got, want)
	}
	if sock := GetControlSocketPathForVM(got); sock != filepath.Join(want, "control.sock") {
		t.Fatalf("socket path = %q", sock)
	}
}

func TestApplySharedFoldersWarningNamesTargetVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "ubuntu-gh-runner-headed"
	vmDir = "default"
	vmDirectory, err := sharedFolderCommandVMDir()
	if err != nil {
		t.Fatalf("sharedFolderCommandVMDir() error = %v", err)
	}

	out := captureStdout(t, func() error {
		return applySharedFoldersAndPrint(vmDirectory)
	})
	for _, want := range []string{
		`VM "ubuntu-gh-runner-headed"`,
		filepath.Join(vmDirectory, "control.sock"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, filepath.Join("vz-macos", "default", "control.sock")) {
		t.Fatalf("output used cwd-relative default socket:\n%s", out)
	}
}

func TestSharedFolderAddSkipsHotApplyWithoutVirtioFS(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	hostDir := t.TempDir()
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "shared-folders-runtime-status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"running":true,"virtiofs":false,"message":"no directory sharing devices configured"}`,
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return handleVMSharedFolderAdd(vmDir, []string{hostDir, "work", "rw"})
	})
	for _, want := range []string{
		"shared folder saved; will mount on next boot of " + filepath.Base(vmDir) + " (this VM was not booted with VirtioFS device, so live attach is not possible)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Added shared folder:") || strings.Contains(out, "could not live-apply") || strings.Contains(out, "applied to running VM") {
		t.Fatalf("output attempted live apply:\n%s", out)
	}
}

func TestSharedFolderAddLiveAppliesAndMounts(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	hostDir := t.TempDir()
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "shared-folders-runtime-status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"running":true,"virtiofs":true,"message":"shared folders VirtioFS device present"}`,
			},
		},
		{
			wantType: "shared-folders-apply",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "applied 1 shared folder(s)"}},
			},
		},
		{
			wantType: "agent-ping",
			resp:     &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test-agent"}}},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount_virtiofs", SharedFoldersVirtioFSTag, defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return handleVMSharedFolderAdd(vmDir, []string{hostDir, "work", "rw"})
	})
	for _, want := range []string{
		"Added shared folder: " + resolvePath(hostDir) + " (tag=work, rw)",
		"shared folder saved; applying to running VM ...",
		"applied to running VM: applied 1 shared folder(s)",
		"mounted in guest at " + defaultSharedFoldersMountPoint,
		"Guest path for this folder: " + defaultSharedFoldersMountPoint + "/work",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPendingSharedFoldersWithoutLiveVMListsConfiguredFolders(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	hostDir := t.TempDir()
	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "work", false); err != nil {
		t.Fatalf("addSharedFolderEntry() error = %v", err)
	}

	out := captureStdout(t, func() error {
		return pendingSharedFolders(vmDir, defaultSharedFoldersMountPoint)
	})
	for _, want := range []string{
		"Running VM mount status unavailable:",
		"Pending shared folders for next boot of " + filepath.Base(vmDir) + ":",
		"work\trw\t" + resolvePath(hostDir),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPendingSharedFoldersOmitsMountedTags(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	hostDir := t.TempDir()
	otherDir := t.TempDir()
	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "work", false); err != nil {
		t.Fatalf("addSharedFolderEntry(work) error = %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, otherDir, "other", true); err != nil {
		t.Fatalf("addSharedFolderEntry(other) error = %v", err)
	}
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "shared-folders-runtime-status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"running":true,"virtiofs":true,"message":"shared folders VirtioFS device present"}`,
			},
		},
		{
			wantType: "agent-ping",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test"}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "/dev/virtiofs on " + defaultSharedFoldersMountPoint + " (virtiofs)\n",
				}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"ls", "-1", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "work\n",
				}},
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return pendingSharedFolders(vmDir, defaultSharedFoldersMountPoint)
	})
	if strings.Contains(out, "work\trw") {
		t.Fatalf("output listed mounted tag:\n%s", out)
	}
	if !strings.Contains(out, "other\tro\t"+resolvePath(otherDir)) {
		t.Fatalf("output missing pending tag:\n%s", out)
	}
}

func TestHandleVMSharedFolderCommandUnknown(t *testing.T) {
	if err := handleVMSharedFolderCommand([]string{"bogus"}); err == nil {
		t.Fatalf("handleVMSharedFolderCommand() error = nil, want error")
	}
}

func TestSaveLoadSharedFoldersRoundTrip(t *testing.T) {
	vmDir := t.TempDir()
	hostA := filepath.Join(t.TempDir(), "alpha")
	hostB := filepath.Join(t.TempDir(), "beta")
	if err := os.MkdirAll(hostA, 0755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.MkdirAll(hostB, 0755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}

	want := []SharedFolderEntry{
		{Path: hostA, Tag: "alpha", ReadOnly: false},
		{Path: hostB, Tag: "beta", ReadOnly: true},
	}
	if err := saveSharedFolders(vmDir, want); err != nil {
		t.Fatalf("saveSharedFolders() error = %v", err)
	}

	got := LoadSharedFolders(vmDir)
	if len(got) != len(want) {
		t.Fatalf("len(LoadSharedFolders()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("LoadSharedFolders()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSharedFoldersMountedAndSynced(t *testing.T) {
	mounted := "/dev/virtiofs on /Volumes/My Shared Files (virtiofs)\n"
	lsOK := &controlpb.AgentExecResponse{ExitCode: 0, Stdout: "alpha\nbeta\n"}
	lsMissing := &controlpb.AgentExecResponse{ExitCode: 0, Stdout: "alpha\n"}
	lsFail := &controlpb.AgentExecResponse{ExitCode: 1, Stderr: "permission denied"}

	tests := []struct {
		name        string
		mountOutput string
		mountPoint  string
		tags        []string
		lsRes       *controlpb.AgentExecResponse
		lsErr       error
		want        bool
	}{
		{
			name:        "mounted and tags present",
			mountOutput: mounted,
			mountPoint:  defaultSharedFoldersMountPoint,
			tags:        []string{"alpha", "beta"},
			lsRes:       lsOK,
			want:        true,
		},
		{
			name:        "mounted but tag missing",
			mountOutput: mounted,
			mountPoint:  defaultSharedFoldersMountPoint,
			tags:        []string{"alpha", "beta"},
			lsRes:       lsMissing,
			want:        false,
		},
		{
			name:        "mounted but ls failed",
			mountOutput: mounted,
			mountPoint:  defaultSharedFoldersMountPoint,
			tags:        []string{"alpha"},
			lsRes:       lsFail,
			want:        false,
		},
		{
			name:        "mounted but ls rpc errored",
			mountOutput: mounted,
			mountPoint:  defaultSharedFoldersMountPoint,
			tags:        []string{"alpha"},
			lsErr:       os.ErrPermission,
			want:        false,
		},
		{
			name:        "not mounted",
			mountOutput: "/dev/virtiofs on /Volumes/Elsewhere (virtiofs)\n",
			mountPoint:  defaultSharedFoldersMountPoint,
			tags:        []string{"alpha"},
			lsRes:       lsOK,
			want:        false,
		},
		{
			name:        "mounted with no expected tags",
			mountOutput: mounted,
			mountPoint:  defaultSharedFoldersMountPoint,
			tags:        nil,
			lsRes:       &controlpb.AgentExecResponse{ExitCode: 0, Stdout: ""},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sharedFoldersMountedAndSynced(tt.mountOutput, tt.mountPoint, tt.tags, tt.lsRes, tt.lsErr); got != tt.want {
				t.Fatalf("sharedFoldersMountedAndSynced() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMountSharedFoldersInGuestMountedLSTimeoutIsBounded(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")

	vmDir := shortSharedFolderVMDir(t)
	hostDir := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "alpha", false); err != nil {
		t.Fatalf("addSharedFolderEntry(): %v", err)
	}

	steps := []sharedFolderControlStep{
		{
			wantType: "agent-ping",
			resp:     &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test-agent"}}},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "/dev/virtiofs on " + defaultSharedFoldersMountPoint + " (virtiofs)\n",
				}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"ls", "-1", defaultSharedFoldersMountPoint},
			delay:    250 * time.Millisecond,
		},
	}
	verify := serveSharedFolderControlSteps(t, vmDir, "test-token", steps)

	start := time.Now()
	_, err := mountSharedFoldersInGuestWithTimeouts(vmDir, defaultSharedFoldersMountPoint, sharedFolderMountTimeouts{
		agentPing: 200 * time.Millisecond,
		mkdir:     200 * time.Millisecond,
		mounts:    200 * time.Millisecond,
		listTags:  40 * time.Millisecond,
		unmount:   200 * time.Millisecond,
		mount:     200 * time.Millisecond,
	})
	elapsed := time.Since(start)
	verify()

	if err == nil {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts(): got nil error, want bounded inspect error")
	}
	if !strings.Contains(err.Error(), "inspect mounted shared folders at") {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts() error = %v, want inspect error", err)
	}
	if elapsed > time.Second {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts() took %s, want bounded failure", elapsed)
	}
}

func TestMountSharedFoldersInGuestMountedStaleTagsRemounts(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")

	vmDir := shortSharedFolderVMDir(t)
	hostA := filepath.Join(t.TempDir(), "alpha")
	hostB := filepath.Join(t.TempDir(), "beta")
	if err := os.MkdirAll(hostA, 0755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.MkdirAll(hostB, 0755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostA, "alpha", false); err != nil {
		t.Fatalf("addSharedFolderEntry(alpha): %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostB, "beta", false); err != nil {
		t.Fatalf("addSharedFolderEntry(beta): %v", err)
	}

	verify := serveSharedFolderControlSteps(t, vmDir, "test-token", []sharedFolderControlStep{
		{
			wantType: "agent-ping",
			resp:     &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test-agent"}}},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "/dev/virtiofs on " + defaultSharedFoldersMountPoint + " (virtiofs)\n",
				}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"ls", "-1", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "alpha\n",
				}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"umount", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount_virtiofs", SharedFoldersVirtioFSTag, defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
	})

	mounted, err := mountSharedFoldersInGuestWithTimeouts(vmDir, defaultSharedFoldersMountPoint, sharedFolderMountTimeouts{
		agentPing: 200 * time.Millisecond,
		mkdir:     200 * time.Millisecond,
		mounts:    200 * time.Millisecond,
		listTags:  200 * time.Millisecond,
		unmount:   200 * time.Millisecond,
		mount:     200 * time.Millisecond,
	})
	verify()

	if err != nil {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts(): %v", err)
	}
	if !mounted {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts() = false, want true after remount")
	}
}

func TestMountSharedFoldersInGuestLinuxUsesMountVirtioFS(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")

	vmDir := linuxTestVMDir(t)
	cfg, err := vmconfig.Load(vmDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.GuestUserUID = 1001
	cfg.GuestUserGID = 1002
	if err := vmconfig.Save(vmDir, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	hostDir := t.TempDir()
	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "work", false); err != nil {
		t.Fatalf("addSharedFolderEntry(): %v", err)
	}

	verify := serveSharedFolderControlSteps(t, vmDir, "test-token", []sharedFolderControlStep{
		{
			wantType: "agent-ping",
			resp:     &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test-agent"}}},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount", "-t", "virtiofs", "-o", "cache=none,uid=1001,gid=1002", SharedFoldersVirtioFSTag, defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
	})

	mounted, err := mountSharedFoldersInGuestWithTimeouts(vmDir, defaultSharedFoldersMountPoint, sharedFolderMountTimeouts{
		agentPing: 200 * time.Millisecond,
		mkdir:     200 * time.Millisecond,
		mounts:    200 * time.Millisecond,
		listTags:  200 * time.Millisecond,
		unmount:   200 * time.Millisecond,
		mount:     200 * time.Millisecond,
	})
	verify()

	if err != nil {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts(): %v", err)
	}
	if !mounted {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts() = false, want true")
	}
}

func TestControlClientAgentExecTypedTimeoutHonorsOverride(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")

	vmDir := shortSharedFolderVMDir(t)
	verify := serveSharedFolderControlSteps(t, vmDir, "test-token", []sharedFolderControlStep{
		{
			wantType: "agent-exec",
			wantArgs: []string{"echo", "hello"},
			delay:    250 * time.Millisecond,
		},
	})

	client := NewControlClient(GetControlSocketPathForVM(vmDir))
	start := time.Now()
	_, err := client.AgentExecTypedTimeout([]string{"echo", "hello"}, nil, "", 40*time.Millisecond)
	elapsed := time.Since(start)
	verify()

	if err == nil {
		t.Fatalf("AgentExecTypedTimeout(): got nil error, want timeout")
	}
	if elapsed > time.Second {
		t.Fatalf("AgentExecTypedTimeout() took %s, want timeout override to bound the request", elapsed)
	}
}

func shortSharedFolderVMDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp(os.TempDir(), "vzsf-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type sharedFolderControlStep struct {
	wantType string
	wantArgs []string
	delay    time.Duration
	resp     *controlpb.ControlResponse
}

func serveSharedFolderControlSteps(t *testing.T, vmDir, token string, steps []sharedFolderControlStep) func() {
	t.Helper()
	t.Setenv(controlTokenEnvVar, token)

	tokenPath := GetControlTokenPathForVM(vmDir)
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		t.Fatalf("write control token: %v", err)
	}

	sock := GetControlSocketPathForVM(vmDir)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen %q: %v", sock, err)
	}

	var (
		mu    sync.Mutex
		index int
	)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()

				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					t.Errorf("read control request: %v", err)
					return
				}

				var req controlpb.ControlRequest
				if err := protojson.Unmarshal([]byte(line), &req); err != nil {
					t.Errorf("unmarshal control request: %v", err)
					return
				}
				if req.GetAuthToken() != token {
					t.Errorf("auth token = %q, want %q", req.GetAuthToken(), token)
					return
				}

				mu.Lock()
				if index >= len(steps) {
					mu.Unlock()
					t.Errorf("unexpected request type %q", req.GetType())
					return
				}
				step := steps[index]
				index++
				mu.Unlock()

				if req.GetType() != step.wantType {
					t.Errorf("request type = %q, want %q", req.GetType(), step.wantType)
					return
				}
				if step.wantArgs != nil {
					got := req.GetAgentExec().GetArgs()
					if !reflect.DeepEqual(got, step.wantArgs) {
						t.Errorf("agent-exec args = %v, want %v", got, step.wantArgs)
						return
					}
				}
				if step.delay > 0 {
					time.Sleep(step.delay)
				}
				if step.resp == nil {
					return
				}

				data, err := protojsonMarshaler.Marshal(step.resp)
				if err != nil {
					t.Errorf("marshal control response: %v", err)
					return
				}
				if _, err := conn.Write(append(data, '\n')); err != nil {
					t.Errorf("write control response: %v", err)
				}
			}(conn)
		}
	}()

	return func() {
		t.Helper()
		ln.Close()

		mu.Lock()
		defer mu.Unlock()
		if index != len(steps) {
			t.Fatalf("handled %d control requests, want %d", index, len(steps))
		}
	}
}

func TestMountContainsAllTags(t *testing.T) {
	listing := "ml-explore\nmlx-go\ntmc\n"
	if !mountContainsAllTags(listing, []string{"ml-explore", "tmc"}) {
		t.Fatalf("expected tags to be found")
	}
	if mountContainsAllTags(listing, []string{"missing"}) {
		t.Fatalf("unexpected success for missing tag")
	}
	if !mountContainsAllTags(listing, nil) {
		t.Fatalf("empty tag set should always pass")
	}
}

func BenchmarkMountContainsAllTagsLarge(b *testing.B) {
	const count = 1024
	tags := make([]string, 0, count)
	var listing strings.Builder
	for i := 0; i < count; i++ {
		tag := sanitizeSharedFolderTag("Dataset " + strings.Repeat("x", i%7) + string(rune('a'+(i%26))))
		tags = append(tags, tag)
		listing.WriteString(tag)
		listing.WriteByte('\n')
	}
	data := listing.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !mountContainsAllTags(data, tags) {
			b.Fatal("expected all tags to be present")
		}
	}
}

func BenchmarkSharedFoldersMountedAndSyncedLarge(b *testing.B) {
	const count = 512
	tags := make([]string, 0, count)
	var listing strings.Builder
	for i := 0; i < count; i++ {
		tag := sanitizeSharedFolderTag("Project " + strings.Repeat("y", i%5) + string(rune('a'+(i%26))))
		tags = append(tags, tag)
		listing.WriteString(tag)
		listing.WriteByte('\n')
	}
	lsRes := &controlpb.AgentExecResponse{ExitCode: 0, Stdout: listing.String()}
	mountOutput := "/dev/virtiofs on " + defaultSharedFoldersMountPoint + " (virtiofs)\n"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !sharedFoldersMountedAndSynced(mountOutput, defaultSharedFoldersMountPoint, tags, lsRes, nil) {
			b.Fatal("expected mounted and synced")
		}
	}
}
