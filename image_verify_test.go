package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestVerifyImagePassesOnFreshImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, err := ParseImageRef("fresh:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	report := VerifyImage(ref, imageVerifyOptions{})
	if report.Verdict != imageVerifyPass {
		t.Fatalf("Verdict = %s, want PASS (%#v)", report.Verdict, report.Checks)
	}
	if report.Legacy {
		t.Fatal("report marked legacy on fresh image")
	}
	if report.ForkCount != 0 {
		t.Fatalf("ForkCount = %d, want 0", report.ForkCount)
	}
	if report.Manifest == nil {
		t.Fatal("missing manifest in verify report")
	}
	if report.Manifest.CoveCommit == "" || report.Manifest.AgentCommit == "" {
		t.Fatalf("manifest provenance incomplete: %#v", report.Manifest)
	}
	if !strings.Contains(report.Checks[0].Name, "manifest") {
		t.Fatalf("first check = %#v, want manifest", report.Checks[0])
	}

	var jsonBuf strings.Builder
	if err := writeImageVerifyJSON(&jsonBuf, report); err != nil {
		t.Fatalf("writeImageVerifyJSON: %v", err)
	}
	var roundTrip imageVerifyReport
	if err := json.Unmarshal([]byte(jsonBuf.String()), &roundTrip); err != nil {
		t.Fatalf("unmarshal verify JSON: %v", err)
	}
	if roundTrip.Verdict != imageVerifyPass || roundTrip.Ref != ref.String() {
		t.Fatalf("round-trip report = %#v", roundTrip)
	}
}

func TestVerifyImageStrictFailsWithoutExecAttachV3(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, err := ParseImageRef("strict:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	manifest, err := LoadImageManifest(ref)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	manifest.AgentFeatures = []string{"shell.v1"}
	if err := writeImageManifest(ref.Path(), manifest); err != nil {
		t.Fatalf("writeImageManifest: %v", err)
	}

	report := VerifyImage(ref, imageVerifyOptions{Strict: true})
	if report.Verdict != imageVerifyFail {
		t.Fatalf("Verdict = %s, want FAIL (%#v)", report.Verdict, report.Checks)
	}
	found := false
	for _, check := range report.Checks {
		if check.Name == "agent features" {
			found = true
			if check.Status != imageVerifyFail {
				t.Fatalf("agent features check = %#v, want FAIL", check)
			}
		}
	}
	if !found {
		t.Fatal("missing agent features check")
	}
}

func TestVerifyImageWarnsOnLegacyManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "legacy-src")
	ref, err := ParseImageRef("legacy:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "legacy-src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	legacy, err := LoadImageManifest(ref)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	legacy.CoveCommit = ""
	legacy.AgentCommit = ""
	legacy.AgentFeatures = nil
	legacy.BuildRecipe = ""
	legacy.SourceImage = ""
	legacy.BuiltAt = time.Time{}
	legacy.DefaultNetwork = ""
	legacy.DefaultSandbox = ""
	if err := writeImageManifest(ref.Path(), legacy); err != nil {
		t.Fatalf("writeImageManifest: %v", err)
	}

	report := VerifyImage(ref, imageVerifyOptions{})
	if report.Verdict != imageVerifyWarn {
		t.Fatalf("Verdict = %s, want WARN (%#v)", report.Verdict, report.Checks)
	}
	if !report.Legacy {
		t.Fatal("report did not mark legacy manifest")
	}
}

func TestVerifyImageNewerThan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, err := ParseImageRef("freshness:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	manifest, err := LoadImageManifest(ref)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	manifest.BuiltAt = mustParseTime(t, "2026-05-05T12:00:00Z")
	manifest.CreatedAt = manifest.BuiltAt
	if err := writeImageManifest(ref.Path(), manifest); err != nil {
		t.Fatalf("writeImageManifest: %v", err)
	}

	report := VerifyImage(ref, imageVerifyOptions{
		NewerThan: 24 * time.Hour,
		Now:       mustParseTime(t, "2026-05-06T11:00:00Z"),
	})
	if got := imageVerifyCheckStatus(report, "freshness"); got != imageVerifyPass {
		t.Fatalf("freshness = %s, want PASS (%#v)", got, report.Checks)
	}

	report = VerifyImage(ref, imageVerifyOptions{
		NewerThan: 24 * time.Hour,
		Now:       mustParseTime(t, "2026-05-06T13:00:00Z"),
	})
	if got := imageVerifyCheckStatus(report, "freshness"); got != imageVerifyFail {
		t.Fatalf("freshness = %s, want FAIL (%#v)", got, report.Checks)
	}
	if report.Verdict != imageVerifyFail {
		t.Fatalf("Verdict = %s, want FAIL", report.Verdict)
	}
}

func TestRunImageVerifyQuiet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, err := ParseImageRef("quiet:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	if err := runImageVerify([]string{"--quiet", ref.String()}); err != nil {
		t.Fatalf("runImageVerify quiet pass: %v", err)
	}
}

func TestRunImageForkFromWithConfigRefusesFailedVerify(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, err := ParseImageRef("stale:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	manifest, err := LoadImageManifest(ref)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	manifest.DiskSize++
	if err := writeImageManifest(ref.Path(), manifest); err != nil {
		t.Fatalf("writeImageManifest: %v", err)
	}

	oldRunLinux := runLinuxVMHook
	t.Cleanup(func() { runLinuxVMHook = oldRunLinux })
	runLinuxVMHook = func() error {
		t.Fatal("runLinuxVMHook should not be called on failed verify")
		return nil
	}

	err = runImageForkFromWithConfig(RunConfig{
		Linux:               true,
		EphemeralForkParent: ref.String(),
	}, "", "")
	if err == nil {
		t.Fatal("runImageForkFromWithConfig succeeded; want refusal")
	}
	if !strings.Contains(err.Error(), "failed verify") {
		t.Fatalf("error = %v, want failed verify refusal", err)
	}
}

func TestRunImageForkFromWithConfigWarnsAndProceeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, err := ParseImageRef("warn:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	manifest, err := LoadImageManifest(ref)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	manifest.AgentFeatures = nil
	if err := writeImageManifest(ref.Path(), manifest); err != nil {
		t.Fatalf("writeImageManifest: %v", err)
	}

	oldRunLinux := runLinuxVMHook
	t.Cleanup(func() { runLinuxVMHook = oldRunLinux })
	runLinuxVMHook = func() error { return nil }

	err = runImageForkFromWithConfig(RunConfig{
		Linux:               true,
		EphemeralForkParent: ref.String(),
		EphemeralForkName:   "warn-child",
	}, "", "")
	if err != nil {
		t.Fatalf("runImageForkFromWithConfig: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), "warn-child")); statErr != nil {
		t.Fatalf("expected materialized child: %v", statErr)
	}
}

func mustParseTime(t *testing.T, value string) (tm time.Time) {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return tm
}

func imageVerifyCheckStatus(report imageVerifyReport, name string) imageVerifyStatus {
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Status
		}
	}
	return ""
}
