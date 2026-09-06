package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestHandleSupportCommandUsesEnvStdoutForUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	err := handleSupportCommand(env, nil)
	if err == nil || !strings.Contains(err.Error(), "support: command required") {
		t.Fatalf("err = %v, want command required", err)
	}
	if !strings.Contains(stdout.String(), "Usage: cove support <command>") {
		t.Fatalf("stdout missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunSupportBundleUsesEnvStderrForFlagErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	err := runSupportBundle(env, []string{"-nope"})
	if err == nil {
		t.Fatal("err = nil, want flag error")
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr missing flag error:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestCreateSupportBundleRedactsDiagnostics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER", "alice")
	oldRun := supportRunCommand
	oldHostRun := hostDoctorRunCommand
	oldCapture := supportCaptureWindowsQEMUImage
	t.Cleanup(func() {
		supportRunCommand = oldRun
		hostDoctorRunCommand = oldHostRun
		supportCaptureWindowsQEMUImage = oldCapture
	})
	supportRunCommand = func(ctx context.Context, args ...string) supportCommandResult {
		return supportCommandResult{
			Stdout:   "path=/Users/alice/project\nAuthorization: Bearer abc.def\npassword: swordfish\n",
			ExitCode: 0,
		}
	}
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		switch name {
		case "sw_vers":
			return []byte("15.5\n"), nil
		case "codesign":
			return []byte("<key>com.apple.security.virtualization</key><true/>"), nil
		case "xcode-select":
			return []byte("/Library/Developer/CommandLineTools\n"), nil
		default:
			return nil, nil
		}
	}
	if err := os.MkdirAll(filepath.Join(vmconfig.BaseDir(), "work"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	path, err := createSupportBundle(supportBundleOptions{VM: "work", Out: out})
	if err != nil {
		t.Fatalf("createSupportBundle: %v", err)
	}
	if path != out {
		t.Fatalf("path = %q, want %q", path, out)
	}
	files := readSupportBundleFiles(t, out)
	for _, name := range []string{
		"manifest.json",
		"doctor-host.json",
		"commands/commands.json",
		"commands/helper-status.txt",
		"commands/trace-capabilities.json",
		"commands/logs-help.txt",
		"vm/ctl-agent-status.txt",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("bundle missing %s; files=%v", name, supportBundleMapKeys(files))
		}
	}
	all := strings.Join(mapValues(files), "\n")
	for _, forbidden := range []string{"Bearer abc.def", "swordfish", "/Users/alice"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("bundle was not redacted; found %q in:\n%s", forbidden, all)
		}
	}
	for _, want := range []string{"Bearer REDACTED", "password: REDACTED", "$HOME"} {
		if !strings.Contains(all, want) {
			t.Fatalf("bundle missing redacted marker %q in:\n%s", want, all)
		}
	}
}

func TestCreateSupportBundleMissingVMIsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldRun := supportRunCommand
	oldHostRun := hostDoctorRunCommand
	t.Cleanup(func() {
		supportRunCommand = oldRun
		hostDoctorRunCommand = oldHostRun
	})

	var commands []string
	supportRunCommand = func(ctx context.Context, args ...string) supportCommandResult {
		commands = append(commands, strings.Join(args, " "))
		return supportCommandResult{ExitCode: 0}
	}
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}
	supportCaptureWindowsQEMUImage = func(string) (image.Image, error) {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		return img, nil
	}

	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := createSupportBundle(supportBundleOptions{VM: "missing", Out: out}); err != nil {
		t.Fatalf("createSupportBundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing VM dir stat error = %v, want not exist", err)
	}
	files := readSupportBundleFiles(t, out)
	body, ok := files["vm/not-found.txt"]
	if !ok {
		t.Fatalf("bundle missing vm/not-found.txt; files=%v", supportBundleMapKeys(files))
	}
	for _, want := range []string{`no VM named "missing"`, "cove list", "cove up -user <name>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("vm/not-found.txt missing %q:\n%s", want, body)
		}
	}
	if _, ok := files["vm/doctor.txt"]; ok {
		t.Fatalf("bundle included vm/doctor.txt for missing VM")
	}
	for _, cmd := range commands {
		if strings.Contains(cmd, " -vm missing") || strings.HasPrefix(cmd, "doctor -vm missing") || cmd == "trace status missing" {
			t.Fatalf("ran VM diagnostic for missing VM: %s", cmd)
		}
	}
}

func TestCreateSupportBundleIncludesQEMUWindowsArtifacts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "alice")
	oldRun := supportRunCommand
	oldHostRun := hostDoctorRunCommand
	t.Cleanup(func() {
		supportRunCommand = oldRun
		hostDoctorRunCommand = oldHostRun
	})
	supportRunCommand = func(ctx context.Context, args ...string) supportCommandResult {
		if strings.Join(args, " ") == "ctl -vm qemu-win gui status" {
			return supportCommandResult{ExitCode: 0, Stdout: "user: cove\npass: Cove123!\n"}
		}
		return supportCommandResult{ExitCode: 0}
	}
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}

	vmDir := filepath.Join(vmconfig.BaseDir(), "qemu-win")
	qemuDir := filepath.Join(vmDir, "qemu")
	if err := os.MkdirAll(qemuDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "windows.qcow2"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata := `{
  "backend": "qemu-hvf",
  "vncEndpoint": "127.0.0.1:5907",
  "vncURL": "vnc://127.0.0.1:5907",
  "guestUsername": "cove",
  "guestPassword": "Cove123!",
  "diskPath": "/Users/alice/.vz/vms/qemu-win/windows.qcow2",
  "serialOutput": "` + filepath.ToSlash(filepath.Join(qemuDir, "serial.log")) + `"
}`
	if err := os.WriteFile(filepath.Join(qemuDir, "metadata.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "process.json"), []byte(`{"qemuPid":123}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "README"), []byte("path=/Users/alice/.vz/vms/qemu-win\npassword: no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "serial.log"), []byte("boot\nAuthorization: Bearer abc.def\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := createSupportBundle(supportBundleOptions{VM: "qemu-win", Out: out}); err != nil {
		t.Fatalf("createSupportBundle: %v", err)
	}
	files := readSupportBundleFiles(t, out)
	for _, name := range []string{
		"vm/qemu-metadata.json",
		"vm/qemu-process.json",
		"vm/qemu-readme.txt",
		"vm/qemu-status.json",
		"vm/qemu-serial-files.txt",
		"vm/qemu-serial-tail.txt",
		"vm/qemu-screenshot.txt",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("bundle missing %s; files=%v", name, supportBundleMapKeys(files))
		}
	}
	if _, ok := files["vm/qemu-screenshot.png"]; ok {
		t.Fatalf("bundle included screenshot without opt-in")
	}
	if !strings.Contains(files["vm/qemu-screenshot.txt"], "-include-screenshot") {
		t.Fatalf("qemu screenshot note missing opt-in hint:\n%s", files["vm/qemu-screenshot.txt"])
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(files["vm/qemu-metadata.json"]), &got); err != nil {
		t.Fatalf("qemu metadata is not JSON: %v\n%s", err, files["vm/qemu-metadata.json"])
	}
	if got["guestPassword"] != "REDACTED" {
		t.Fatalf("guestPassword = %v, want REDACTED", got["guestPassword"])
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(files["vm/qemu-status.json"]), &status); err != nil {
		t.Fatalf("qemu status is not JSON: %v\n%s", err, files["vm/qemu-status.json"])
	}
	if status["gui"] != "qemu-vnc-external" || status["vncAuth"] != "none" {
		t.Fatalf("qemu status gui/vncAuth = %v/%v", status["gui"], status["vncAuth"])
	}
	if status["screenshotBackend"] != "rfb" || status["textBackend"] != "rfb" {
		t.Fatalf("qemu status backends = %v/%v", status["screenshotBackend"], status["textBackend"])
	}
	if status["guestPassword"] != "REDACTED" {
		t.Fatalf("qemu status guestPassword = %v, want REDACTED", status["guestPassword"])
	}
	all := strings.Join(mapValues(files), "\n")
	for _, forbidden := range []string{"Cove123!", "Bearer abc.def", "/Users/alice"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("bundle was not redacted; found %q in:\n%s", forbidden, all)
		}
	}
	if _, ok := files["vm/windows.qcow2"]; ok {
		t.Fatalf("bundle included qemu disk")
	}
}

func TestCreateSupportBundleIncludesQEMUWindowsScreenshotWhenRequested(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldRun := supportRunCommand
	oldHostRun := hostDoctorRunCommand
	oldCapture := supportCaptureWindowsQEMUImage
	t.Cleanup(func() {
		supportRunCommand = oldRun
		hostDoctorRunCommand = oldHostRun
		supportCaptureWindowsQEMUImage = oldCapture
	})
	supportRunCommand = func(ctx context.Context, args ...string) supportCommandResult {
		return supportCommandResult{ExitCode: 0}
	}
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}
	supportCaptureWindowsQEMUImage = func(string) (image.Image, error) {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{G: 255, A: 255})
		return img, nil
	}

	vmDir := filepath.Join(vmconfig.BaseDir(), "qemu-win")
	qemuDir := filepath.Join(vmDir, "qemu")
	if err := os.MkdirAll(qemuDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "windows.qcow2"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "metadata.json"), []byte(`{"backend":"qemu-hvf"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := createSupportBundle(supportBundleOptions{VM: "qemu-win", Out: out, IncludeScreenshot: true}); err != nil {
		t.Fatalf("createSupportBundle: %v", err)
	}
	files := readSupportBundleFiles(t, out)
	if _, ok := files["vm/qemu-screenshot.png"]; !ok {
		t.Fatalf("bundle missing qemu screenshot; files=%v", supportBundleMapKeys(files))
	}
	if _, ok := files["vm/qemu-screenshot.txt"]; ok {
		t.Fatalf("bundle included screenshot opt-in note with screenshot")
	}
	if !strings.Contains(files["manifest.json"], `"include_screenshot": true`) {
		t.Fatalf("manifest missing include_screenshot:\n%s", files["manifest.json"])
	}
	if !strings.Contains(files["manifest.json"], `"redacted": false`) {
		t.Fatalf("manifest did not mark screenshot bundle as not fully redacted:\n%s", files["manifest.json"])
	}
}

func readSupportBundleFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name] = string(data)
	}
	return files
}

func supportBundleMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
