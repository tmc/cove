package softreset

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestFilesystemAttributeProbePassesAfterRemoveAndRecreate(t *testing.T) {
	root := t.TempDir() + "-softreset"
	got, err := (FilesystemAttributeProbe{Root: root}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Probe != "filesystem-attributes" {
		t.Fatalf("Probe = %q", got.Probe)
	}
	if got.Status != StatusPass {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "sentinel=absent-after-reset") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestFilesystemAttributeProbeFailsWhenResidueSurvives(t *testing.T) {
	root := t.TempDir() + "-softreset"
	got, err := (FilesystemAttributeProbe{
		Root: root,
		Reset: func(context.Context, string) error {
			return nil
		},
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusFail {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	for _, want := range []string{"sentinel=present-after-reset", "mode-residue=present", "mtime-residue=present"} {
		if !hasEvidence(got.Evidence, want) {
			t.Fatalf("evidence missing %q: %v", want, got.Evidence)
		}
	}
}

func TestFilesystemAttributeProbeRejectsUnsafeRoot(t *testing.T) {
	_, err := (FilesystemAttributeProbe{Root: t.TempDir()}).Run(context.Background())
	if err == nil {
		t.Fatal("Run accepted unsafe root")
	}
	if !strings.Contains(err.Error(), "softreset") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoveAndRecreate(t *testing.T) {
	root := t.TempDir() + "-softreset"
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(root+"/sentinel", []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := RemoveAndRecreate(context.Background(), root); err != nil {
		t.Fatalf("RemoveAndRecreate: %v", err)
	}
	if _, err := os.Stat(root + "/sentinel"); !os.IsNotExist(err) {
		t.Fatalf("sentinel stat error = %v, want not exist", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("root stat = %v, %v", info, err)
	}
}

func hasEvidence(evidence []string, want string) bool {
	for _, got := range evidence {
		if got == want {
			return true
		}
	}
	return false
}
