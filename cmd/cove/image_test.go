package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/imagestore"
	"github.com/tmc/cove/internal/vmconfig"
)

func imageTestEnv() commandEnv {
	return commandEnv{
		Stdin:  strings.NewReader(""),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
}

func imageTestEnvWithStdout(out *bytes.Buffer) commandEnv {
	env := imageTestEnv()
	env.Stdout = out
	return env
}

// stageMacOSVMForImage writes a stopped VM directory with the minimal
// files BuildImage needs: disk.img, aux.img, hw.model, machine.id.
// Mirrors stageParentVMForEphemeralFork in fork_ephemeral_test.go but
// keeps the contents distinct so collisions are visible.
func stageMacOSVMForImage(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(vmconfig.BaseDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	files := map[string][]byte{
		"disk.img":   []byte("image-source-disk-bytes"),
		"aux.img":    []byte("image-source-aux"),
		"hw.model":   []byte("image-source-hwmodel"),
		"machine.id": []byte("IMAGE-SRC-MACHINE-IDENTIFIER-OK"),
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	if err := vmconfig.Save(dir, &vmconfig.Config{Agent: &vmconfig.AgentConfig{
		Platform:  "macos",
		Requested: true,
		Verified:  true,
		Version:   hostVersion(),
		Commit:    hostVersion(),
		Features:  []string{"exec_attach", "exec_attach_v3", "shell"},
	}}); err != nil {
		t.Fatalf("save macos config: %v", err)
	}
	return dir
}

func stageLinuxVMForImage(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(vmconfig.BaseDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	files := map[string][]byte{
		"linux-disk.img":      []byte("linux-image-source-disk-bytes"),
		"linux-machine.id":    []byte("LINUX-SRC-MACHINE-IDENTIFIER-OK"),
		"vmlinuz":             []byte("kernel"),
		"initrd":              []byte("initrd"),
		linuxRootUUIDFileName: []byte("root=UUID=test\n"),
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	if err := vmconfig.Save(dir, &vmconfig.Config{Agent: &vmconfig.AgentConfig{
		Platform:  "linux",
		Requested: true,
		Verified:  true,
		Version:   hostVersion(),
		Commit:    hostVersion(),
		Features:  []string{"exec_attach_v3", "shell"},
	}}); err != nil {
		t.Fatalf("save linux config: %v", err)
	}
	return dir
}

func sliceContainsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantTag  string
		wantErr  bool
	}{
		{"foo", "foo", "latest", false},
		{"foo:bar", "foo", "bar", false},
		{"my-img:1.2.3", "my-img", "1.2.3", false},
		{"agentkit/linux-base:v1", "agentkit/linux-base", "v1", false},
		{"", "", "", true},
		{":bar", "", "", true},
		{"foo:", "", "", true},
		{"a:b:c", "", "", true},
		{"/foo:tag", "", "", true},
		{"foo/:tag", "", "", true},
		{"foo//bar:tag", "", "", true},
		{"foo:bad tag", "", "", true},
		{"..:tag", "", "", true},
		{"agentkit/..:tag", "", "", true},
		{"ghcr.io/acme/vm:v1", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			ref, err := ParseImageRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseImageRef(%q) succeeded; want error", tc.in)
				}
				if !errors.Is(err, ErrImageRefInvalid) {
					t.Fatalf("ParseImageRef(%q) err = %v, want errors.Is(err, ErrImageRefInvalid)", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseImageRef(%q): %v", tc.in, err)
			}
			if ref.Name != tc.wantName || ref.Tag != tc.wantTag {
				t.Errorf("ParseImageRef(%q) = %s:%s, want %s:%s",
					tc.in, ref.Name, ref.Tag, tc.wantName, tc.wantTag)
			}
		})
	}
}

func TestBuildImage_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := "img-src"
	srcDir := stageMacOSVMForImage(t, src)

	ref, err := ParseImageRef("my-snap")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	fixed := time.Date(2026, 5, 2, 10, 30, 0, 0, time.UTC)
	manifest, err := BuildImage(BuildImageOptions{
		SourceVM: src,
		Ref:      ref,
		Now:      func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	// Manifest fields.
	if manifest.Name != "my-snap" || manifest.Tag != "latest" {
		t.Errorf("manifest ref = %s:%s, want my-snap:latest", manifest.Name, manifest.Tag)
	}
	if manifest.SourceVM != src {
		t.Errorf("manifest.SourceVM = %q, want %q", manifest.SourceVM, src)
	}
	if manifest.CoveCommit == "" {
		t.Error("manifest.CoveCommit is empty")
	}
	if manifest.AgentCommit == "" {
		t.Error("manifest.AgentCommit is empty")
	}
	if !sliceContainsString(manifest.AgentFeatures, "execattach.v3") {
		t.Fatalf("manifest.AgentFeatures = %v, want execattach.v3", manifest.AgentFeatures)
	}
	if manifest.BuildRecipe == "" {
		t.Error("manifest.BuildRecipe is empty")
	}
	if manifest.BuiltAt.IsZero() {
		t.Error("manifest.BuiltAt is zero")
	}
	if manifest.DefaultNetwork == "" {
		t.Error("manifest.DefaultNetwork is empty")
	}
	if manifest.DefaultSandbox == "" {
		t.Error("manifest.DefaultSandbox is empty")
	}
	if !manifest.CreatedAt.Equal(fixed) {
		t.Errorf("manifest.CreatedAt = %v, want %v", manifest.CreatedAt, fixed)
	}
	if manifest.DiskSize != int64(len("image-source-disk-bytes")) {
		t.Errorf("manifest.DiskSize = %d, want %d", manifest.DiskSize, len("image-source-disk-bytes"))
	}
	if len(manifest.DiskSHA256) != 64 {
		t.Errorf("manifest.DiskSHA256 length = %d, want 64", len(manifest.DiskSHA256))
	}

	// On-disk layout.
	imgDir := ref.Path()
	info, err := os.Stat(imgDir)
	if err != nil {
		t.Fatalf("stat image dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("image dir mode = %03o, want 700", got)
	}
	for _, f := range []string{"manifest.json", "disk.img", "aux.img", "hw.model", "machine.id"} {
		if _, err := os.Stat(filepath.Join(imgDir, f)); err != nil {
			t.Errorf("image missing %s: %v", f, err)
		}
	}
	// suspend.vmstate must NOT be in the image (design 024 line 80-84).
	if _, err := os.Stat(filepath.Join(imgDir, "suspend.vmstate")); !os.IsNotExist(err) {
		t.Error("image unexpectedly contains suspend.vmstate")
	}

	// Re-load the manifest to confirm JSON round-trips.
	got, err := LoadImageManifest(ref)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	if got.DiskSHA256 != manifest.DiskSHA256 {
		t.Errorf("loaded.DiskSHA256 = %q, want %q", got.DiskSHA256, manifest.DiskSHA256)
	}
	if got.CoveCommit != manifest.CoveCommit || got.AgentCommit != manifest.AgentCommit {
		t.Fatalf("loaded provenance = %q/%q, want %q/%q", got.CoveCommit, got.AgentCommit, manifest.CoveCommit, manifest.AgentCommit)
	}

	// Source VM unchanged.
	if data, err := os.ReadFile(filepath.Join(srcDir, "disk.img")); err != nil {
		t.Errorf("source disk gone: %v", err)
	} else if string(data) != "image-source-disk-bytes" {
		t.Errorf("source disk mutated: %q", string(data))
	}
}

func TestBuildImage_RefusesDuplicate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src1")
	ref, _ := ParseImageRef("dup:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src1", Ref: ref}); err != nil {
		t.Fatalf("first BuildImage: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src1", Ref: ref}); err == nil {
		t.Fatal("second BuildImage succeeded; want already-exists error")
	}
}

func TestListImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	for _, tag := range []string{"a:1", "a:2", "agentkit/linux-base:latest", "b:1"} {
		ref, _ := ParseImageRef(tag)
		if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
			t.Fatalf("BuildImage %s: %v", tag, err)
		}
	}
	entries, err := ListImages()
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("ListImages returned %d entries, want 4", len(entries))
	}
	want := []string{"a:1", "a:2", "agentkit/linux-base:latest", "b:1"}
	for i, e := range entries {
		if e.Ref.String() != want[i] {
			t.Errorf("entries[%d] = %s, want %s", i, e.Ref, want[i])
		}
		if e.Manifest.DiskSize <= 0 {
			t.Errorf("entries[%d].Manifest.DiskSize = %d, want > 0", i, e.Manifest.DiskSize)
		}
	}
}

func TestBuildImage_LinuxNamespacedAgentkitLayout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageLinuxVMForImage(t, "linux-src")
	ref, err := ParseImageRef("agentkit/linux-base:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	manifest, err := BuildImage(BuildImageOptions{SourceVM: "linux-src", Ref: ref})
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if manifest.OSType != "Linux" {
		t.Errorf("manifest.OSType = %q, want Linux", manifest.OSType)
	}
	if ref.Path() != filepath.Join(ImagesBaseDir(), "agentkit", "linux-base", "v1") {
		t.Errorf("ref.Path = %q, want agentkit layout", ref.Path())
	}
	for _, name := range []string{"manifest.json", "linux-disk.img", "vmlinuz", "initrd", linuxRootUUIDFileName, "config.json"} {
		if _, err := os.Stat(filepath.Join(ref.Path(), name)); err != nil {
			t.Errorf("image missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ref.Path(), "disk.img")); !os.IsNotExist(err) {
		t.Errorf("linux image unexpectedly has disk.img: %v", err)
	}
}

func TestMaterializeImage_LinuxImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageLinuxVMForImage(t, "linux-src")
	ref, _ := ParseImageRef("agentkit/linux-base:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "linux-src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	childDir, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: "linux-worker"})
	if err != nil {
		t.Fatalf("MaterializeImage: %v", err)
	}
	for _, name := range []string{"linux-disk.img", "vmlinuz", "initrd", linuxRootUUIDFileName, "config.json"} {
		if _, err := os.Stat(filepath.Join(childDir, name)); err != nil {
			t.Errorf("child missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(childDir, "linux-machine.id")); !os.IsNotExist(err) {
		t.Errorf("linux-machine.id = %v, want absent before first boot", err)
	}
	cfg, err := vmconfig.Load(childDir)
	if err != nil {
		t.Fatalf("load child config: %v", err)
	}
	if cfg.ParentImage != "agentkit/linux-base:v1" {
		t.Errorf("child ParentImage = %q, want agentkit/linux-base:v1", cfg.ParentImage)
	}
	if cfg.Agent == nil || cfg.Agent.Platform != "linux" {
		t.Errorf("child agent config = %#v, want linux agent config", cfg.Agent)
	}
}

func TestMaterializeImage_FreshIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("base:1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	childDir, err := MaterializeImage(MaterializeImageOptions{
		Ref:       ref,
		ChildName: "worker-1",
		Ephemeral: true,
		Now:       func() time.Time { return time.Date(2026, 5, 2, 11, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("MaterializeImage: %v", err)
	}
	if vmconfig.NameForPath(childDir) != "worker-1" {
		t.Errorf("child dir name = %q, want worker-1", vmconfig.NameForPath(childDir))
	}
	info, err := os.Stat(childDir)
	if err != nil {
		t.Fatalf("stat child dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("child dir mode = %03o, want 700", got)
	}

	// Required files present.
	for _, f := range []string{"disk.img", "aux.img", "hw.model", "machine.id", ephemeralSentinel} {
		if _, err := os.Stat(filepath.Join(childDir, f)); err != nil {
			t.Errorf("child missing %s: %v", f, err)
		}
	}

	// Disk is identical bytes to the image disk (clonefile semantics).
	srcDisk, _ := os.ReadFile(filepath.Join(ref.Path(), "disk.img"))
	dstDisk, _ := os.ReadFile(filepath.Join(childDir, "disk.img"))
	if string(srcDisk) != string(dstDisk) {
		t.Error("materialized disk differs from image disk")
	}

	// machine.id is fresh (not the source VM's "IMAGE-SRC-..." string).
	machineID, _ := os.ReadFile(filepath.Join(childDir, "machine.id"))
	if strings.HasPrefix(string(machineID), "IMAGE-SRC") {
		t.Errorf("machine.id was copied verbatim; want fresh identity")
	}

	// Config records ParentImage so `cove image rm` can refuse.
	cfg, err := vmconfig.Load(childDir)
	if err != nil {
		t.Fatalf("load child config: %v", err)
	}
	if cfg.ParentImage != "base:1" {
		t.Errorf("child ParentImage = %q, want base:1", cfg.ParentImage)
	}
}

func TestVMsForkedFromImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("base:1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	for _, name := range []string{"worker-1", "worker-2"} {
		if _, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: name}); err != nil {
			t.Fatalf("MaterializeImage %s: %v", name, err)
		}
	}
	got, err := VMsForkedFromImage(ref)
	if err != nil {
		t.Fatalf("VMsForkedFromImage: %v", err)
	}
	if len(got) != 2 || got[0] != "worker-1" || got[1] != "worker-2" {
		t.Errorf("VMsForkedFromImage = %v, want [worker-1 worker-2]", got)
	}
}

func TestDeleteImage_RefusesWhileForksLive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("base:1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	childDir, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: "worker-1"})
	if err != nil {
		t.Fatalf("MaterializeImage: %v", err)
	}
	// While worker-1 lives, rm must refuse.
	if err := DeleteImage(ref); err == nil {
		t.Fatal("DeleteImage succeeded with live fork; want refusal")
	} else if !strings.Contains(err.Error(), "worker-1") {
		t.Errorf("DeleteImage error = %v, want substring 'worker-1'", err)
	}
	if !ImageExists(ref) {
		t.Fatal("image unexpectedly removed despite refusal")
	}
	// Remove the child VM, then rm must succeed.
	if err := os.RemoveAll(childDir); err != nil {
		t.Fatalf("remove child: %v", err)
	}
	if err := DeleteImage(ref); err != nil {
		t.Fatalf("DeleteImage after child gone: %v", err)
	}
	if ImageExists(ref) {
		t.Error("image still exists after DeleteImage")
	}
}

func TestIsImageForkFromRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// VM "src" exists; image "base:1" exists. We check both are dispatched
	// the right way by isImageForkFromRef.
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("base:1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	cases := []struct {
		ref  string
		want bool
	}{
		{"src", false},     // VM name → RAM-overlay path
		{"base:1", true},   // image ref → image-fork path
		{"missing", false}, // neither → false (caller errors out later)
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			if got := isImageForkFromRef(tc.ref); got != tc.want {
				t.Errorf("isImageForkFromRef(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestWriteImageManifestAtomic verifies writeImageManifest leaves no
// partial manifest.json: the destination is either fully present with
// valid JSON or absent. Closes R5 of image-gc-race-audit-2026-05-08.
func TestWriteImageManifestAtomic(t *testing.T) {
	dir := t.TempDir()
	m := &imagestore.Manifest{Name: "n", Tag: "t", SchemaVersion: 1}
	if err := writeImageManifest(dir, m); err != nil {
		t.Fatalf("writeImageManifest: %v", err)
	}
	// No leftover tmp file.
	if _, err := os.Stat(filepath.Join(dir, "manifest.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("manifest.json.tmp leaked: err=%v", err)
	}
	// Final file parses as valid JSON with expected fields.
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("manifest.json is empty")
	}
	var got imagestore.Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got.Name != "n" || got.Tag != "t" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestWriteImageManifestNoPartialOnRenameFailure verifies that if the
// rename step cannot proceed (target dir missing), no partial
// manifest.json is left behind.
func TestWriteImageManifestNoPartialOnDirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	m := &imagestore.Manifest{Name: "n", Tag: "t", SchemaVersion: 1}
	if err := writeImageManifest(dir, m); err == nil {
		t.Fatalf("writeImageManifest: expected error for missing dir")
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest.json should not exist: err=%v", err)
	}
}

func TestMaterializeImageRejectsMissingImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref, _ := ParseImageRef("ghost:v1")
	_, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: "kid"})
	if err == nil {
		t.Fatal("MaterializeImage(missing image) = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want 'not found'", err)
	}
}

func TestMaterializeImageRejectsExistingChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src-existing")
	ref, _ := ParseImageRef("collide:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-existing", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if err := os.MkdirAll(vmconfig.Path("already-here"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: "already-here"})
	if err == nil {
		t.Fatal("MaterializeImage(existing child) = nil, want already-exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want 'already exists'", err)
	}
}
