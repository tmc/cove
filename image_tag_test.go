package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTagImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	src, _ := ParseImageRef("base:old")
	dst, _ := ParseImageRef("base:new")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: src}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src.Path(), "LABELS"), []byte("role=runner\n"), 0o644); err != nil {
		t.Fatalf("write labels: %v", err)
	}
	if err := TagImage(ImageTagOptions{Source: src, Target: dst}); err != nil {
		t.Fatalf("TagImage: %v", err)
	}
	if !ImageExists(src) {
		t.Fatalf("source image was removed")
	}
	if !ImageExists(dst) {
		t.Fatalf("target image missing")
	}
	m, err := LoadImageManifest(dst)
	if err != nil {
		t.Fatalf("LoadImageManifest target: %v", err)
	}
	if m.Name != "base" || m.Tag != "new" {
		t.Fatalf("target manifest ref = %s:%s, want base:new", m.Name, m.Tag)
	}
	if m.DiskSHA256 == "" || m.DiskSize == 0 {
		t.Fatalf("target manifest lost disk metadata: %#v", m)
	}
	for _, name := range []string{"disk.img", "aux.img", "hw.model", "machine.id", "LABELS"} {
		if _, err := os.Stat(filepath.Join(dst.Path(), name)); err != nil {
			t.Fatalf("target missing %s: %v", name, err)
		}
	}
}

func TestTagImageRejectsExistingTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	src, _ := ParseImageRef("base:old")
	dst, _ := ParseImageRef("base:new")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: src}); err != nil {
		t.Fatalf("BuildImage src: %v", err)
	}
	stageMacOSVMForImage(t, "dst")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "dst", Ref: dst}); err != nil {
		t.Fatalf("BuildImage dst: %v", err)
	}
	if err := TagImage(ImageTagOptions{Source: src, Target: dst}); err == nil {
		t.Fatalf("TagImage succeeded with existing target")
	}
}

func TestTagImageRejectsMissingSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src, _ := ParseImageRef("missing:old")
	dst, _ := ParseImageRef("base:new")
	if err := TagImage(ImageTagOptions{Source: src, Target: dst}); err == nil {
		t.Fatalf("TagImage succeeded with missing source")
	}
}

func TestRunImageTagUsage(t *testing.T) {
	if err := runImageTag(nil); err == nil {
		t.Fatalf("runImageTag with no args succeeded")
	}
}

func TestTagImageRejectsEmptyRefs(t *testing.T) {
	good, _ := ParseImageRef("base:tag")
	tests := []struct {
		name string
		opts ImageTagOptions
		want string
	}{
		{"empty source", ImageTagOptions{Source: ImageRef{}, Target: good}, "source ref required"},
		{"source missing tag", ImageTagOptions{Source: ImageRef{Name: "base"}, Target: good}, "source ref required"},
		{"empty target", ImageTagOptions{Source: good, Target: ImageRef{}}, "target ref required"},
		{"target missing tag", ImageTagOptions{Source: good, Target: ImageRef{Name: "base"}}, "target ref required"},
		{"same ref", ImageTagOptions{Source: good, Target: good}, "source and target are the same"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TagImage(tt.opts)
			if err == nil {
				t.Fatalf("TagImage(%+v) = nil, want error containing %q", tt.opts, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("TagImage(%+v) error = %v, want substring %q", tt.opts, err, tt.want)
			}
		})
	}
}

func TestRunImageTagHelpAndUnknownFlag(t *testing.T) {
	t.Run("help flag returns nil", func(t *testing.T) {
		if err := runImageTag([]string{"-h"}); err != nil {
			t.Fatalf("runImageTag -h: %v, want nil", err)
		}
	})
	t.Run("unknown flag returns parse error", func(t *testing.T) {
		err := runImageTag([]string{"-not-a-real-flag"})
		if err == nil {
			t.Fatal("runImageTag(-not-a-real-flag) = nil, want parse error")
		}
		if strings.Contains(err.Error(), "usage:") {
			t.Fatalf("expected parse error, got usage error: %v", err)
		}
	})
}

func TestRunImageTagBadRef(t *testing.T) {
	if err := runImageTag([]string{"::bad", "base:new"}); err == nil {
		t.Fatalf("runImageTag with malformed src ref succeeded")
	}
	if err := runImageTag([]string{"base:old", "::bad"}); err == nil {
		t.Fatalf("runImageTag with malformed dst ref succeeded")
	}
}
