package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestImageHistoryFor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "base-src")
	base, _ := ParseImageRef("base:v1")
	baseTime := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	if _, err := BuildImage(BuildImageOptions{
		SourceVM: "base-src",
		Ref:      base,
		Now:      func() time.Time { return baseTime },
	}); err != nil {
		t.Fatalf("BuildImage base: %v", err)
	}
	if _, err := MaterializeImage(MaterializeImageOptions{Ref: base, ChildName: "child"}); err != nil {
		t.Fatalf("MaterializeImage: %v", err)
	}
	child, _ := ParseImageRef("child:v2")
	childTime := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	if _, err := BuildImage(BuildImageOptions{
		SourceVM:    "child",
		Ref:         child,
		BuildRecipe: "cove image build -from child -tag child:v2",
		Now:         func() time.Time { return childTime },
	}); err != nil {
		t.Fatalf("BuildImage child: %v", err)
	}

	history, err := ImageHistoryFor(child)
	if err != nil {
		t.Fatalf("ImageHistoryFor: %v", err)
	}
	if len(history.Entries) != 2 {
		t.Fatalf("Entries len = %d, want 2: %#v", len(history.Entries), history)
	}
	got := history.Entries[0]
	if got.Ref != "child:v2" || got.ParentRef != "base:v1" {
		t.Fatalf("entry ref/parent = %q/%q, want child:v2/base:v1", got.Ref, got.ParentRef)
	}
	if got.Timestamp != childTime.Format(time.RFC3339) {
		t.Fatalf("Timestamp = %q, want %q", got.Timestamp, childTime.Format(time.RFC3339))
	}
	if got.Size == 0 || len(got.Layers) == 0 {
		t.Fatalf("entry missing size/layers: %#v", got)
	}
	if got.SourceCommand != "cove image build -from child -tag child:v2" {
		t.Fatalf("SourceCommand = %q", got.SourceCommand)
	}
}

func TestWriteImageHistoryJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("hist:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	history, err := ImageHistoryFor(ref)
	if err != nil {
		t.Fatalf("ImageHistoryFor: %v", err)
	}
	var buf bytes.Buffer
	if err := writeImageHistoryJSON(&buf, history); err != nil {
		t.Fatalf("writeImageHistoryJSON: %v", err)
	}
	var got ImageHistory
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, buf.String())
	}
	if got.Ref != "hist:v1" || len(got.Entries) != 1 {
		t.Fatalf("round trip = %#v", got)
	}
	if len(got.Entries[0].Layers) == 0 {
		t.Fatalf("round trip missing layers: %#v", got.Entries[0])
	}
}

func TestImageHistoryForMissingRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing, _ := ParseImageRef("ghost:v1")
	if _, err := ImageHistoryFor(missing); err == nil {
		t.Fatalf("ImageHistoryFor(missing) err = nil, want error")
	}
}

func TestWriteImageHistoryTextEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := writeImageHistoryText(&buf, ImageHistory{Ref: "empty:v1"}); err != nil {
		t.Fatalf("writeImageHistoryText empty: %v", err)
	}
	if !strings.Contains(buf.String(), "REF") {
		t.Fatalf("empty history text missing header:\n%s", buf.String())
	}
}

func TestWriteImageHistoryText(t *testing.T) {
	history := ImageHistory{Ref: "base:v1", Entries: []ImageHistoryEntry{{
		Ref:           "base:v1",
		Timestamp:     "2026-05-05T12:00:00Z",
		Size:          123,
		ParentRef:     "parent:v1",
		SourceCommand: "cove image build -from vm -tag base:v1",
		Layers:        []ImageHistoryLayer{{Name: "disk.img", Digest: "sha256:abc", Size: 123}},
	}}}
	var buf bytes.Buffer
	if err := writeImageHistoryText(&buf, history); err != nil {
		t.Fatalf("writeImageHistoryText: %v", err)
	}
	text := buf.String()
	for _, want := range []string{"REF", "TIMESTAMP", "base:v1", "parent:v1", "disk.img", "sha256:abc"} {
		if !strings.Contains(text, want) {
			t.Fatalf("history text missing %q:\n%s", want, text)
		}
	}
}
