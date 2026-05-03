package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
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
	if roundTrip.Forks == nil {
		t.Error("round-trip Forks is nil; want empty slice (json `forks` always present)")
	}
}
