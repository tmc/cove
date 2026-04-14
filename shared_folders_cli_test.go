package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
