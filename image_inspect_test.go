package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/imagestore"
)

func TestInspectImage_Manifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, err := ParseImageRef("base:1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	fixed := time.Date(2026, 5, 2, 10, 30, 0, 0, time.UTC)
	if _, err := BuildImage(BuildImageOptions{
		SourceVM: "src",
		Ref:      ref,
		Now:      func() time.Time { return fixed },
	}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	out, err := InspectImage(ref)
	if err != nil {
		t.Fatalf("InspectImage: %v", err)
	}
	if out.Ref != "base:1" {
		t.Errorf("Ref = %q, want base:1", out.Ref)
	}
	if out.Name != "base" || out.Tag != "1" {
		t.Errorf("Name/Tag = %q/%q, want base/1", out.Name, out.Tag)
	}
	if out.DiskSize != int64(len("image-source-disk-bytes")) {
		t.Errorf("DiskSize = %d, want %d", out.DiskSize, len("image-source-disk-bytes"))
	}
	if len(out.DiskSHA256) != 64 {
		t.Errorf("DiskSHA256 length = %d, want 64", len(out.DiskSHA256))
	}
	if !strings.HasSuffix(out.ManifestPath, "/manifest.json") {
		t.Errorf("ManifestPath = %q, want suffix /manifest.json", out.ManifestPath)
	}
	if out.Created != fixed.Format(time.RFC3339) {
		t.Errorf("Created = %q, want %q", out.Created, fixed.Format(time.RFC3339))
	}
	if len(out.MachineModelID) != 64 {
		t.Errorf("MachineModelID len = %d, want 64", len(out.MachineModelID))
	}
	if out.LegacyManifest {
		t.Fatal("LegacyManifest = true, want false for fresh image")
	}
	if out.CoveCommit == "" || out.AgentCommit == "" {
		t.Fatalf("provenance incomplete: cove=%q agent=%q", out.CoveCommit, out.AgentCommit)
	}
	if !strings.Contains(out.BuildRecipe, "cove image build") {
		t.Fatalf("BuildRecipe = %q, want build command", out.BuildRecipe)
	}
	if out.ForkCount != 0 || len(out.Forks) != 0 {
		t.Errorf("ForkCount/Forks = %d/%v, want 0/[]", out.ForkCount, out.Forks)
	}
}

func TestInspectImage_WithForks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("base:1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	for _, name := range []string{"worker-a", "worker-b"} {
		if _, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: name}); err != nil {
			t.Fatalf("MaterializeImage %s: %v", name, err)
		}
	}

	out, err := InspectImage(ref)
	if err != nil {
		t.Fatalf("InspectImage: %v", err)
	}
	if out.ForkCount != 2 {
		t.Fatalf("ForkCount = %d, want 2", out.ForkCount)
	}
	want := map[string]bool{"worker-a": true, "worker-b": true}
	for _, f := range out.Forks {
		if !want[f] {
			t.Errorf("unexpected fork %q (forks=%v)", f, out.Forks)
		}
		delete(want, f)
	}
	if len(want) != 0 {
		t.Errorf("missing forks: %v (got %v)", want, out.Forks)
	}
}

func TestInspectImage_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref, _ := ParseImageRef("ghost:1")
	_, err := InspectImage(ref)
	if err == nil {
		t.Fatal("InspectImage on missing image succeeded; want error")
	}
	if !strings.Contains(err.Error(), "ghost:1") {
		t.Errorf("error %q does not mention ref ghost:1", err.Error())
	}
}

func TestRunImageInspect_JSONFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("snap:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	out, err := InspectImage(ref)
	if err != nil {
		t.Fatalf("InspectImage: %v", err)
	}
	var buf bytes.Buffer
	if err := writeInspectJSON(&buf, out); err != nil {
		t.Fatalf("writeInspectJSON: %v", err)
	}
	var roundTrip ImageInspectOutput
	if err := json.Unmarshal(buf.Bytes(), &roundTrip); err != nil {
		t.Fatalf("Unmarshal: %v\nraw:\n%s", err, buf.String())
	}
	if roundTrip.Ref != "snap:v1" {
		t.Errorf("round-trip Ref = %q, want snap:v1", roundTrip.Ref)
	}
	if roundTrip.DiskSHA256 != out.DiskSHA256 {
		t.Errorf("round-trip DiskSHA256 mismatch")
	}
	if roundTrip.CoveCommit == "" || roundTrip.AgentCommit == "" {
		t.Fatalf("round-trip provenance incomplete: %#v", roundTrip)
	}
	if roundTrip.Forks == nil {
		t.Error("round-trip Forks is nil; want empty slice (json `forks` always present)")
	}
}

func TestInspectImageDiff_IdenticalRefs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("snap:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	diff, err := InspectImageDiff(ref, ref)
	if err != nil {
		t.Fatalf("InspectImageDiff: %v", err)
	}
	if len(diff.Changed) != 0 || len(diff.Added) != 0 || len(diff.Removed) != 0 {
		t.Fatalf("diff changed=%v added=%v removed=%v, want none", diff.Changed, diff.Added, diff.Removed)
	}
	if len(diff.Layers) == 0 {
		t.Fatal("Layers is empty")
	}
	for _, layer := range diff.Layers {
		if layer.Status != "UNCHANGED" {
			t.Fatalf("layer %s status = %s, want UNCHANGED", layer.Name, layer.Status)
		}
	}
	var buf bytes.Buffer
	if err := writeInspectDiffText(&buf, diff); err != nil {
		t.Fatalf("writeInspectDiffText: %v", err)
	}
	if !strings.Contains(buf.String(), "[UNCHANGED]") {
		t.Fatalf("text diff missing UNCHANGED marker:\n%s", buf.String())
	}
}

func TestInspectImageDiff_LinuxLayers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageLinuxVMForImage(t, "linux-src")
	ref, _ := ParseImageRef("linux:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "linux-src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	diff, err := InspectImageDiff(ref, ref)
	if err != nil {
		t.Fatalf("InspectImageDiff: %v", err)
	}
	layer := findDiffLayer(t, diff, "linux-disk.img")
	if layer.Status != "UNCHANGED" {
		t.Fatalf("linux-disk.img status = %s, want UNCHANGED", layer.Status)
	}
	for _, layer := range diff.Layers {
		if layer.Name == "disk.img" {
			t.Fatalf("unexpected macOS disk layer in Linux diff: %#v", diff.Layers)
		}
	}
	var buf bytes.Buffer
	if err := writeInspectDiffText(&buf, diff); err != nil {
		t.Fatalf("writeInspectDiffText: %v", err)
	}
	text := buf.String()
	if !strings.Contains(text, "linux-disk.img") || strings.Contains(text, "disk.img\t<missing>") {
		t.Fatalf("text diff has wrong Linux layer display:\n%s", text)
	}
}

func TestInspectImageDiff_ChangedFieldsAndLayers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src-a")
	stageMacOSVMForImage(t, "src-b")
	refA, _ := ParseImageRef("agentkit/linux-base:old")
	refB, _ := ParseImageRef("agentkit/linux-base:new")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-a", Ref: refA}); err != nil {
		t.Fatalf("BuildImage old: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-b", Ref: refB}); err != nil {
		t.Fatalf("BuildImage new: %v", err)
	}
	patchManifest(t, refA, map[string]any{
		"cove_commit":    "abc123",
		"agent_features": []any{"shell.v1"},
	})
	patchManifest(t, refB, map[string]any{
		"cove_commit":    "c390eb9",
		"agent_features": []any{"shell.v1", "execattach.v3"},
	})
	if err := os.WriteFile(filepath.Join(refB.Path(), "disk.img"), []byte("different disk"), 0o644); err != nil {
		t.Fatalf("write disk.img: %v", err)
	}

	diff, err := InspectImageDiff(refA, refB)
	if err != nil {
		t.Fatalf("InspectImageDiff: %v", err)
	}
	for _, field := range []string{"tag", "cove_commit", "agent_features"} {
		if _, ok := diff.Changed[field]; !ok {
			t.Fatalf("Changed[%q] missing; changed=%v", field, diff.Changed)
		}
	}
	layer := findDiffLayer(t, diff, "disk.img")
	if layer.Status != "CHANGED" {
		t.Fatalf("disk.img status = %s, want CHANGED", layer.Status)
	}
	layer = findDiffLayer(t, diff, "aux.img")
	if layer.Status != "UNCHANGED" {
		t.Fatalf("aux.img status = %s, want UNCHANGED", layer.Status)
	}
	var buf bytes.Buffer
	if err := writeInspectDiffText(&buf, diff); err != nil {
		t.Fatalf("writeInspectDiffText: %v", err)
	}
	text := buf.String()
	for _, want := range []string{"[CHANGED]", "[UNCHANGED]", "[shell.v1]", "[shell.v1, execattach.v3]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text diff missing %q:\n%s", want, text)
		}
	}
}

func TestInspectImageDiff_JSONMissingFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src-a")
	stageMacOSVMForImage(t, "src-b")
	refA, _ := ParseImageRef("base:legacy")
	refB, _ := ParseImageRef("base:fresh")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-a", Ref: refA}); err != nil {
		t.Fatalf("BuildImage legacy: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-b", Ref: refB}); err != nil {
		t.Fatalf("BuildImage fresh: %v", err)
	}
	removeManifestFields(t, refA, "cove_commit", "agent_features", "built_at")
	patchManifest(t, refB, map[string]any{"cove_commit": "new", "agent_features": []any{"execattach.v3"}})

	diff, err := InspectImageDiff(refA, refB)
	if err != nil {
		t.Fatalf("InspectImageDiff: %v", err)
	}
	var buf bytes.Buffer
	if err := writeInspectDiffJSON(&buf, diff); err != nil {
		t.Fatalf("writeInspectDiffJSON: %v", err)
	}
	var got struct {
		Added map[string]struct {
			Old any `json:"old"`
			New any `json:"new"`
		} `json:"added"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v\nraw:\n%s", err, buf.String())
	}
	if got.Added["cove_commit"].Old != "<missing>" || got.Added["cove_commit"].New != "new" {
		t.Fatalf("added cove_commit = %#v", got.Added["cove_commit"])
	}
	if got.Added["agent_features"].Old != "<missing>" {
		t.Fatalf("added agent_features old = %#v, want <missing>", got.Added["agent_features"].Old)
	}
}

func TestInspectImageDiff_MissingRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	refA, _ := ParseImageRef("base:1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: refA}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	ghost, _ := ParseImageRef("ghost:1")

	if _, err := InspectImageDiff(refA, ghost); err == nil {
		t.Fatal("InspectImageDiff with missing ref-b succeeded; want error")
	} else if !strings.Contains(err.Error(), "ghost:1") {
		t.Errorf("error %q does not mention missing ref ghost:1", err.Error())
	}
	if _, err := InspectImageDiff(ghost, refA); err == nil {
		t.Fatal("InspectImageDiff with missing ref-a succeeded; want error")
	} else if !strings.Contains(err.Error(), "ghost:1") {
		t.Errorf("error %q does not mention missing ref ghost:1", err.Error())
	}
}

func TestRunImageInspectDiffJSONMissingRefWritesError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("base:1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	out, err := captureStdoutResult(t, func() error {
		return runImageInspect(commandEnv{Stdout: os.Stdout, Stderr: os.Stderr}, []string{"-json", "-diff", "base:1", "ghost:1"})
	})
	if err == nil {
		t.Fatal("runImageInspect diff missing ref succeeded")
	}
	var got cliJSONError
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jsonErr, out)
	}
	if got.OK || got.Command != "image inspect" || !strings.Contains(got.Error, "ghost:1") || got.Hint == "" {
		t.Fatalf("image inspect diff JSON error = %#v", got)
	}
}

func TestInspectImageDiff_MalformedManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src-a")
	stageMacOSVMForImage(t, "src-b")
	refA, _ := ParseImageRef("base:a")
	refB, _ := ParseImageRef("base:b")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-a", Ref: refA}); err != nil {
		t.Fatalf("BuildImage a: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-b", Ref: refB}); err != nil {
		t.Fatalf("BuildImage b: %v", err)
	}
	for _, tc := range []struct {
		name string
		ref  imagestore.Ref
	}{
		{"side-a", refA},
		{"side-b", refB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			good, err := os.ReadFile(filepath.Join(tc.ref.Path(), "manifest.json"))
			if err != nil {
				t.Fatalf("snapshot manifest: %v", err)
			}
			defer os.WriteFile(filepath.Join(tc.ref.Path(), "manifest.json"), good, 0o644)
			if err := os.WriteFile(filepath.Join(tc.ref.Path(), "manifest.json"), []byte("{not json"), 0o644); err != nil {
				t.Fatalf("corrupt manifest: %v", err)
			}
			if _, err := InspectImageDiff(refA, refB); err == nil {
				t.Fatal("InspectImageDiff on malformed manifest succeeded; want error")
			}
		})
	}
}

func patchManifest(t *testing.T, ref imagestore.Ref, values map[string]any) {
	t.Helper()
	m := readManifestMapForTest(t, ref)
	for k, v := range values {
		m[k] = v
	}
	writeManifestMapForTest(t, ref, m)
}

func removeManifestFields(t *testing.T, ref imagestore.Ref, fields ...string) {
	t.Helper()
	m := readManifestMapForTest(t, ref)
	for _, field := range fields {
		delete(m, field)
	}
	writeManifestMapForTest(t, ref, m)
}

func readManifestMapForTest(t *testing.T, ref imagestore.Ref) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ref.Path(), "manifest.json"))
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal manifest: %v", err)
	}
	return m
}

func writeManifestMapForTest(t *testing.T, ref imagestore.Ref, m map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ref.Path(), "manifest.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}
}

func findDiffLayer(t *testing.T, diff imageInspectDiff, name string) imageInspectLayerDiff {
	t.Helper()
	for _, layer := range diff.Layers {
		if layer.Name == name {
			return layer
		}
	}
	t.Fatalf("layer %s not found in %#v", name, diff.Layers)
	return imageInspectLayerDiff{}
}
