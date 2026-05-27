//go:build darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/imagestore"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestAppSandboxEntitlementFixture(t *testing.T) {
	path := filepath.Join("internal", "autosign", "app_sandbox.entitlements")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := xml.Unmarshal(data, new(any)); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, key := range []string{
		"com.apple.security.app-sandbox",
		"com.apple.security.files.bookmarks.app-scope",
		"com.apple.security.files.user-selected.read-write",
		"com.apple.security.network.client",
		"com.apple.security.network.server",
		"com.apple.security.virtualization",
	} {
		if !bytes.Contains(data, []byte("<key>"+key+"</key>")) {
			t.Fatalf("%s missing entitlement %s", path, key)
		}
	}
}

func TestAppSandboxSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_SMOKE=1 to build and run a sandbox-signed cove binary")
	}
	bin := buildAppSandboxSmokeBinary(t)

	cases := []struct {
		name string
		args []string
	}{
		{name: "version", args: []string{"--version"}},
		{name: "help", args: []string{"help"}},
		{name: "list", args: []string{"list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runSandboxedCoveSmokeCommand(t, bin, tc.args...)
			t.Logf("%s %s err=%v output:\n%s", filepath.Base(bin), strings.Join(tc.args, " "), err, out)
			if os.Getenv("COVE_APP_SANDBOX_SMOKE_EXPECT_START") == "1" && err != nil {
				t.Fatalf("%s %v: %v\n%s", bin, tc.args, err, out)
			}
		})
	}
}

func TestAppSandboxDoctorSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_SMOKE=1 to build and run a sandbox-signed cove binary")
	}
	bin := buildAppSandboxSmokeBinary(t)

	out, err := runSandboxedCoveSmokeCommand(t, bin, "doctor", "host", "-json")
	t.Logf("raw sandbox doctor host err=%v output:\n%s", err, out)
	if err != nil {
		if os.Getenv("COVE_APP_SANDBOX_SMOKE_EXPECT_START") == "1" {
			t.Fatalf("raw sandbox doctor host: %v\n%s", err, out)
		}
		return
	}
	assertSandboxDoctorCommands(t, func(args ...string) (string, error) {
		return runSandboxedCoveSmokeCommand(t, bin, args...)
	})
}

func TestAppSandboxMacgoBundleSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "security", "status")
	t.Logf("sandboxed macgo bundle security status err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo bundle security status: %v\n%s", err, out)
	}
	for _, want := range []string{
		"apple app sandbox: true",
		"apple app sandbox id: com.tmc.cove",
		"/Library/Containers/com.tmc.cove/Data/.vz",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("security status missing %q:\n%s", want, out)
		}
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "list")
	t.Logf("sandboxed macgo bundle list err=%v output:\n%s", err, out)
	if err != nil && !isCommandExit(err) {
		t.Fatalf("sandboxed macgo bundle list: %v\n%s", err, out)
	}
	if !strings.Contains(out, errPowerboxGrantRequired.Error()) {
		t.Fatalf("sandboxed macgo bundle list missing grant-required error:\n%s", out)
	}
	if strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("sandboxed macgo bundle list crashed instead of returning Go error:\n%s", out)
	}
}

func TestAppSandboxMacgoBundleDoctorSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	assertSandboxDoctorCommands(t, func(args ...string) (string, error) {
		return runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, args...)
	})
}

func TestAppSandboxMacgoBundleServeSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	tokenDir := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data", "tmp")
	if err := os.MkdirAll(tokenDir, 0o700); err != nil {
		t.Fatalf("create token dir: %v", err)
	}
	tokenFile := filepath.Join(tokenDir, fmt.Sprintf("cove-serve-smoke-%d.token", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(tokenFile) })
	addr := freeLocalTCPAddr(t)

	output := startSandboxServeSmoke(t, env, tokenFile, "http://"+addr+"/healthz", bin, "serve",
		"-listen", "tcp://"+addr,
		"-token-file", tokenFile,
		"-vms", "__cove_sandbox_smoke_no_vms__",
	)
	t.Logf("sandboxed macgo serve output:\n%s", output)
}

func TestAppSandboxMacgoBundleStateSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "image", "list", "-json")
	t.Logf("sandboxed macgo image list err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo image list: %v\n%s", err, out)
	}
	var images []any
	if err := json.Unmarshal([]byte(firstJSONArray(out)), &images); err != nil {
		t.Fatalf("image list json: %v\n%s", err, out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "storage", "census")
	t.Logf("sandboxed macgo storage census err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo storage census: %v\n%s", err, out)
	}
	var census map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &census); err != nil {
		t.Fatalf("storage census json: %v\n%s", err, out)
	}
	root, ok := census["root"].(string)
	if !ok || !strings.Contains(root, "/Library/Containers/com.tmc.cove/Data/.vz") {
		t.Fatalf("storage census root = %v, want App Sandbox container root\n%s", census["root"], out)
	}
}

func TestAppSandboxMacgoBundleStateDirGrantSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	grantState := filepath.Join(containerHome, "tmp", fmt.Sprintf("cove-state-grant-%d", os.Getpid()))
	ambientName := fmt.Sprintf("ambient-decoy-%d", os.Getpid())
	grantedName := fmt.Sprintf("granted-vm-%d", os.Getpid())
	ambientVM := filepath.Join(containerHome, ".vz", "vms", ambientName)
	grantedVM := filepath.Join(grantState, "vms", grantedName)
	t.Cleanup(func() {
		_ = os.RemoveAll(ambientVM)
		_ = os.RemoveAll(grantState)
	})
	stageAppSandboxListVM(t, ambientVM)
	stageAppSandboxListVM(t, grantedVM)

	grantEnv := append(append([]string{}, env...), vmconfig.StateDirEnv+"="+grantState)
	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "status", "-json")
	t.Logf("sandboxed macgo state grant status err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo state grant status: %v\n%s", err, out)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &status); err != nil {
		t.Fatalf("security status json: %v\n%s", err, out)
	}
	if got, _ := status["state_root"].(string); got != grantState {
		t.Fatalf("security status state_root = %q, want %q\n%s", got, grantState, out)
	}
	if got, _ := status["vm_root"].(string); got != filepath.Join(grantState, "vms") {
		t.Fatalf("security status vm_root = %q, want grant vms root\n%s", got, out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "list")
	t.Logf("sandboxed macgo state grant list err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo state grant list: %v\n%s", err, out)
	}
	if !strings.Contains(out, grantedName) {
		t.Fatalf("list missing granted VM %q:\n%s", grantedName, out)
	}
	if strings.Contains(out, ambientName) {
		t.Fatalf("list included ambient container VM %q despite explicit state grant:\n%s", ambientName, out)
	}
}

func TestAppSandboxMacgoBundleHostPathDenialSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	cases := []struct {
		name string
		args []string
	}{
		{name: "disk resize", args: []string{"disk", "resize", "__missing__", "128G"}},
		{name: "shared folder add", args: []string{"shared-folder", "add", "/tmp", "tmp"}},
		{name: "provision", args: []string{"provision", "-stage-only"}},
		{name: "helper install", args: []string{"helper", "install"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, tc.args...)
			t.Logf("sandboxed macgo denial %s err=%v output:\n%s", strings.Join(tc.args, " "), err, out)
			if err != nil && !isCommandExit(err) {
				t.Fatalf("%s err = %v, want clean return or command exit\n%s", tc.name, err, out)
			}
			if !strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) {
				t.Fatalf("%s output missing App Sandbox denial:\n%s", tc.name, out)
			}
			if strings.Contains(out, "Trace/BPT trap") {
				t.Fatalf("%s crashed instead of returning Go error:\n%s", tc.name, out)
			}
		})
	}
}

func TestAppSandboxDirectoryGrantBoundarySmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	root := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data", "tmp", fmt.Sprintf("cove-dir-grant-%d", os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	share := filepath.Join(root, "share")
	if err := os.MkdirAll(share, 0700); err != nil {
		t.Fatalf("create share: %v", err)
	}
	absShare, err := filepath.Abs(share)
	if err != nil {
		t.Fatalf("abs share: %v", err)
	}
	store := filepath.Join(root, "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "run", "-headless", "-vol", share+":share", "__missing__")
	t.Logf("sandboxed macgo missing directory grant err=%v output:\n%s", err, out)
	if err != nil && !isCommandExit(err) {
		t.Fatalf("missing directory grant err = %v, want clean return or command exit\n%s", err, out)
	}
	if !strings.Contains(out, errPowerboxGrantRequired.Error()) || !strings.Contains(out, "dir:"+absShare) {
		t.Fatalf("missing directory grant output = %q, want typed directory grant", out)
	}
	if strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("missing directory grant crashed instead of returning Go error:\n%s", out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-store", store, "-key", "dir:"+absShare, "-kind", "host-dir", "-path", share)
	t.Logf("sandboxed macgo directory bookmark save err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo directory bookmark save: %v\n%s", err, out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "run", "-headless", "-vol", share+":share", "__missing__")
	t.Logf("sandboxed macgo directory grant run boundary err=%v output:\n%s", err, out)
	if err != nil && !isCommandExit(err) {
		t.Fatalf("directory grant run boundary err = %v, want clean return or command exit\n%s", err, out)
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) {
		t.Fatalf("directory grant run still requested grant:\n%s", out)
	}
	if !strings.Contains(out, "no VM named") && !strings.Contains(out, "run: no VM selected") && !strings.Contains(out, "is invalid under") {
		t.Fatalf("directory grant run output = %q, want VM selection boundary", out)
	}
}

func TestAppSandboxInstallMediaGrantBoundarySmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	root := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data", "tmp", fmt.Sprintf("cove-media-grant-%d", os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatalf("create media grant root: %v", err)
	}
	iso := filepath.Join(root, "install.iso")
	if err := os.WriteFile(iso, []byte("iso"), 0600); err != nil {
		t.Fatalf("write ISO: %v", err)
	}
	absISO, err := filepath.Abs(iso)
	if err != nil {
		t.Fatalf("abs ISO: %v", err)
	}
	store := filepath.Join(root, "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "install", "-linux", "-iso", iso)
	t.Logf("sandboxed macgo missing media grant err=%v output:\n%s", err, out)
	if err != nil && !isCommandExit(err) {
		t.Fatalf("missing media grant err = %v, want clean return or command exit\n%s", err, out)
	}
	if !strings.Contains(out, errPowerboxGrantRequired.Error()) || !strings.Contains(out, "iso:"+absISO) {
		t.Fatalf("missing media grant output = %q, want typed ISO grant", out)
	}
	if strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("missing media grant crashed instead of returning Go error:\n%s", out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-store", store, "-key", "iso:"+absISO, "-kind", "iso", "-path", iso)
	t.Logf("sandboxed macgo media bookmark save err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo media bookmark save: %v\n%s", err, out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "install", "-linux", "-iso", iso)
	t.Logf("sandboxed macgo media grant install boundary err=%v output:\n%s", err, out)
	if err != nil && !isCommandExit(err) {
		t.Fatalf("media grant install boundary err = %v, want clean return or command exit\n%s", err, out)
	}
	if !strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) {
		t.Fatalf("media grant install output = %q, want install mutation denial", out)
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) {
		t.Fatalf("media grant install still requested grant:\n%s", out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "install", "-linux", "-iso", iso, "-preflight")
	t.Logf("sandboxed macgo media grant install preflight err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("media grant install preflight: %v\n%s", err, out)
	}
	if !strings.Contains(out, "install preflight: iso readable: "+iso) {
		t.Fatalf("media grant install preflight output = %q, want readable proof", out)
	}
	if strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) || strings.Contains(out, errPowerboxGrantRequired.Error()) {
		t.Fatalf("media grant install preflight hit denial/grant error:\n%s", out)
	}
}

func TestAppSandboxRunWorkerSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	env = withoutEnv(env, coveAppSandboxMacgoEnv)

	out, err := runSandboxSmokeCommandEnv(t, 90*time.Second, env, bin, "__run-worker", "probe", "-json")
	t.Logf("sandboxed run-worker probe err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed run-worker probe: %v\n%s", err, out)
	}
	var report runWorkerProbeReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &report); err != nil {
		t.Fatalf("run-worker probe json: %v\n%s", err, out)
	}
	if report.ParentAppSandbox {
		t.Fatalf("run-worker parent unexpectedly sandboxed:\n%s", out)
	}
	if !report.Child.AppSandbox {
		t.Fatalf("run-worker child apple_app_sandbox = false:\n%s", out)
	}
	if !report.Child.ReceivedFD || report.Child.Bytes == 0 || report.Child.SHA256 == "" {
		t.Fatalf("run-worker child descriptor proof incomplete: %+v\n%s", report.Child, out)
	}
	if report.Child.Command != "probe" || report.Child.VMName == "" || report.Child.BookmarkLen == 0 {
		t.Fatalf("run-worker child handoff proof incomplete: %+v\n%s", report.Child, out)
	}
	if strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("run-worker crashed instead of returning proof:\n%s", out)
	}
}

func TestAppSandboxRunWorkerStatusPreflightSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	name := fmt.Sprintf("run-worker-status-%d", os.Getpid())
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	storeRoot := filepath.Join(containerHome, "tmp", fmt.Sprintf("cove-run-worker-status-%d", os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(storeRoot) })
	hostVM := filepath.Join(storeRoot, "outside-vm-root", name)
	if err := os.MkdirAll(hostVM, 0700); err != nil {
		t.Fatalf("create run-worker status VM: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostVM, "linux-disk.img"), []byte("disk"), 0600); err != nil {
		t.Fatalf("write run-worker status VM disk: %v", err)
	}
	if err := vmconfig.Save(hostVM, &vmconfig.Config{CPU: 2, MemoryGB: 4}); err != nil {
		t.Fatalf("write run-worker status VM config: %v", err)
	}
	if err := writeVMRuntimeState(hostVM, "stopped"); err != nil {
		t.Fatalf("write run-worker status VM runtime: %v", err)
	}
	store := filepath.Join(storeRoot, "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-key", "vm:"+name, "-kind", "vm-root", "-path", hostVM)
	t.Logf("sandboxed macgo run-worker status bookmark save err=%v output:\n%s", err, out)
	if err != nil || strings.Contains(out, "error:") {
		t.Fatalf("sandboxed macgo run-worker status bookmark save: %v\n%s", err, out)
	}

	workerEnv := withoutEnv(grantEnv, coveAppSandboxMacgoEnv)
	out, err = runSandboxSmokeCommandEnv(t, 90*time.Second, workerEnv, bin, "__run-worker", "status-preflight", "-json", "-vm", name)
	t.Logf("sandboxed run-worker status preflight err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed run-worker status preflight: %v\n%s", err, out)
	}
	var report runWorkerProbeReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &report); err != nil {
		t.Fatalf("run-worker status preflight json: %v\n%s", err, out)
	}
	if report.ParentAppSandbox {
		t.Fatalf("run-worker status parent unexpectedly sandboxed:\n%s", out)
	}
	if !report.Child.AppSandbox || report.Child.Command != "status-preflight" {
		t.Fatalf("run-worker status child did not run sandboxed status preflight: %+v\n%s", report.Child, out)
	}
	if report.Child.VMName != name || report.Child.ResolvedDir != hostVM || report.Child.State != "stopped" || report.Child.OSType != "Linux" {
		t.Fatalf("run-worker status child metadata mismatch: %+v\n%s", report.Child, out)
	}
	if !report.Child.ConfigRead || !report.Child.RuntimeRead || report.Child.BookmarkLen == 0 {
		t.Fatalf("run-worker status child metadata proof incomplete: %+v\n%s", report.Child, out)
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) || strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) || strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("run-worker status preflight hit sandbox/grant failure:\n%s", out)
	}
}

func TestAppSandboxStatusWorkerDelegationSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	name := fmt.Sprintf("status-worker-%d", os.Getpid())
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	storeRoot := filepath.Join(containerHome, "tmp", fmt.Sprintf("cove-status-worker-%d", os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(storeRoot) })
	hostVM := filepath.Join(storeRoot, "outside-vm-root", name)
	if err := os.MkdirAll(hostVM, 0700); err != nil {
		t.Fatalf("create status worker VM: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostVM, "linux-disk.img"), []byte("disk"), 0600); err != nil {
		t.Fatalf("write status worker VM disk: %v", err)
	}
	if err := vmconfig.Save(hostVM, &vmconfig.Config{CPU: 2, MemoryGB: 4}); err != nil {
		t.Fatalf("write status worker VM config: %v", err)
	}
	if err := writeVMRuntimeState(hostVM, "stopped"); err != nil {
		t.Fatalf("write status worker VM runtime: %v", err)
	}
	store := filepath.Join(storeRoot, "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-key", "vm:"+name, "-kind", "vm-root", "-path", hostVM)
	t.Logf("sandboxed macgo status worker bookmark save err=%v output:\n%s", err, out)
	if err != nil || strings.Contains(out, "error:") {
		t.Fatalf("sandboxed macgo status worker bookmark save: %v\n%s", err, out)
	}

	workerEnv := withoutEnv(grantEnv, coveAppSandboxMacgoEnv)
	workerEnv = append(workerEnv, statusWorkerDelegationEnv+"=1")
	out, err = runSandboxSmokeCommandEnv(t, 90*time.Second, workerEnv, bin, "status", name)
	t.Logf("sandboxed status worker delegation err=%v output:\n%s", err, out)
	if !strings.Contains(out, `vm "`+name+`" is stopped`) {
		t.Fatalf("status worker delegation missing stopped VM proof:\n%s", out)
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) || strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) || strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("status worker delegation hit sandbox/grant failure:\n%s", out)
	}
}

func TestAppSandboxRunWorkerListPreflightSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	stateRoot := filepath.Join(containerHome, "tmp", fmt.Sprintf("cove-list-worker-%d", os.Getpid()), "state")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(stateRoot)) })
	vmRoot := filepath.Join(stateRoot, "vms")
	for _, name := range []string{"list-worker-b", "list-worker-a"} {
		dir := filepath.Join(vmRoot, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("create list worker VM: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0600); err != nil {
			t.Fatalf("write list worker VM disk: %v", err)
		}
		if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 2, MemoryGB: 4}); err != nil {
			t.Fatalf("write list worker VM config: %v", err)
		}
	}
	store := filepath.Join(filepath.Dir(stateRoot), "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store, vmconfig.StateDirEnv+"="+stateRoot)
	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-key", "dir:"+vmRoot, "-kind", "host-dir", "-path", vmRoot)
	t.Logf("sandboxed macgo list worker bookmark save err=%v output:\n%s", err, out)
	if err != nil || strings.Contains(out, "error:") {
		t.Fatalf("sandboxed macgo list worker bookmark save: %v\n%s", err, out)
	}

	workerEnv := withoutEnv(grantEnv, coveAppSandboxMacgoEnv)
	out, err = runSandboxSmokeCommandEnv(t, 90*time.Second, workerEnv, bin, "__run-worker", "list-preflight", "-json")
	t.Logf("sandboxed run-worker list preflight err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed run-worker list preflight: %v\n%s", err, out)
	}
	var report runWorkerProbeReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &report); err != nil {
		t.Fatalf("run-worker list preflight json: %v\n%s", err, out)
	}
	if report.ParentAppSandbox {
		t.Fatalf("run-worker list parent unexpectedly sandboxed:\n%s", out)
	}
	if !report.Child.AppSandbox || report.Child.Command != "list-preflight" || report.Child.VMCount != 2 {
		t.Fatalf("run-worker list child proof incomplete: %+v\n%s", report.Child, out)
	}
	if len(report.Child.VMs) != 2 || report.Child.VMs[0].Name != "list-worker-a" || report.Child.VMs[1].Name != "list-worker-b" {
		t.Fatalf("run-worker list VMs = %+v\n%s", report.Child.VMs, out)
	}
	for _, vm := range report.Child.VMs {
		if vm.OSType != "Linux" || vm.State != "stopped" || !vm.ConfigRead {
			t.Fatalf("run-worker list VM metadata = %+v\n%s", vm, out)
		}
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) || strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) || strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("run-worker list preflight hit sandbox/grant failure:\n%s", out)
	}
}

func TestAppSandboxListWorkerDelegationSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	stateRoot := filepath.Join(containerHome, "tmp", fmt.Sprintf("cove-list-delegate-%d", os.Getpid()), "state")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(stateRoot)) })
	vmRoot := filepath.Join(stateRoot, "vms")
	for _, name := range []string{"list-delegate-b", "list-delegate-a"} {
		dir := filepath.Join(vmRoot, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("create list delegate VM: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0600); err != nil {
			t.Fatalf("write list delegate VM disk: %v", err)
		}
		if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 2, MemoryGB: 4}); err != nil {
			t.Fatalf("write list delegate VM config: %v", err)
		}
	}
	store := filepath.Join(filepath.Dir(stateRoot), "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store, vmconfig.StateDirEnv+"="+stateRoot)
	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-key", "dir:"+vmRoot, "-kind", "host-dir", "-path", vmRoot)
	t.Logf("sandboxed macgo list delegation bookmark save err=%v output:\n%s", err, out)
	if err != nil || strings.Contains(out, "error:") {
		t.Fatalf("sandboxed macgo list delegation bookmark save: %v\n%s", err, out)
	}

	workerEnv := withoutEnv(grantEnv, coveAppSandboxMacgoEnv)
	workerEnv = append(workerEnv, listWorkerDelegationEnv+"=1")
	out, err = runSandboxSmokeCommandEnv(t, 90*time.Second, workerEnv, bin, "list")
	t.Logf("sandboxed list worker delegation err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed list worker delegation: %v\n%s", err, out)
	}
	for _, want := range []string{"VMs:", "list-delegate-a", "list-delegate-b", "Linux", "stopped", "GUI state: cove ctl -vm <name> gui status"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list worker delegation output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) || strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) || strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("list worker delegation hit sandbox/grant failure:\n%s", out)
	}
}

func TestAppSandboxRunWorkerImageListPreflightSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	imageRoot, _, grantEnv := setupImageListWorkerSmoke(t, env, "preflight")
	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-key", "dir:"+imageRoot, "-kind", "host-dir", "-path", imageRoot)
	t.Logf("sandboxed macgo image list preflight bookmark save err=%v output:\n%s", err, out)
	if err != nil || strings.Contains(out, "error:") {
		t.Fatalf("sandboxed macgo image list preflight bookmark save: %v\n%s", err, out)
	}

	workerEnv := withoutEnv(grantEnv, coveAppSandboxMacgoEnv)
	out, err = runSandboxSmokeCommandEnv(t, 90*time.Second, workerEnv, bin, "__run-worker", "image-list-preflight", "-json")
	t.Logf("sandboxed run-worker image list preflight err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed run-worker image list preflight: %v\n%s", err, out)
	}
	var report runWorkerProbeReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &report); err != nil {
		t.Fatalf("run-worker image list preflight json: %v\n%s", err, out)
	}
	if report.ParentAppSandbox {
		t.Fatalf("run-worker image list parent unexpectedly sandboxed:\n%s", out)
	}
	if !report.Child.AppSandbox || report.Child.Command != "image-list-preflight" || report.Child.ImageCount != 2 {
		t.Fatalf("run-worker image list child proof incomplete: %+v\n%s", report.Child, out)
	}
	if len(report.Child.Images) != 2 || report.Child.Images[0].Ref != "base:v1" || report.Child.Images[1].Ref != "nested/image:latest" {
		t.Fatalf("run-worker image list images = %+v\n%s", report.Child.Images, out)
	}
	for _, image := range report.Child.Images {
		if image.DiskSize == 0 || image.SourceVM == "" || !image.ManifestRead {
			t.Fatalf("run-worker image metadata = %+v\n%s", image, out)
		}
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) || strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) || strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("run-worker image list preflight hit sandbox/grant failure:\n%s", out)
	}
}

func TestAppSandboxImageListWorkerDelegationSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	imageRoot, _, grantEnv := setupImageListWorkerSmoke(t, env, "delegate")
	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-key", "dir:"+imageRoot, "-kind", "host-dir", "-path", imageRoot)
	t.Logf("sandboxed macgo image list delegation bookmark save err=%v output:\n%s", err, out)
	if err != nil || strings.Contains(out, "error:") {
		t.Fatalf("sandboxed macgo image list delegation bookmark save: %v\n%s", err, out)
	}

	workerEnv := withoutEnv(grantEnv, coveAppSandboxMacgoEnv)
	workerEnv = append(workerEnv, imageListWorkerDelegationEnv+"=1")
	out, err = runSandboxSmokeCommandEnv(t, 90*time.Second, workerEnv, bin, "image", "list")
	t.Logf("sandboxed image list worker delegation err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed image list worker delegation: %v\n%s", err, out)
	}
	for _, want := range []string{"NAME", "TAG", "SIZE", "SOURCE", "base", "v1", "nested/image", "latest", "source-vm"} {
		if !strings.Contains(out, want) {
			t.Fatalf("image list worker delegation output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) || strings.Contains(out, errAppleAppSandboxHostAccessDenied.Error()) || strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("image list worker delegation hit sandbox/grant failure:\n%s", out)
	}
}

func setupImageListWorkerSmoke(t *testing.T, env []string, suffix string) (imageRoot, store string, grantEnv []string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	root := filepath.Join(containerHome, "tmp", fmt.Sprintf("cove-image-list-%s-%d", suffix, os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	imageRoot = filepath.Join(root, "images")
	created := time.Date(2026, 5, 27, 1, 2, 3, 0, time.UTC)
	for _, image := range []struct {
		name string
		tag  string
		size int64
	}{
		{name: "nested/image", tag: "latest", size: 20},
		{name: "base", tag: "v1", size: 10},
	} {
		dir := filepath.Join(append([]string{imageRoot}, append(strings.Split(image.name, "/"), image.tag)...)...)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("create image dir: %v", err)
		}
		if err := imagestore.WriteManifest(dir, &imagestore.Manifest{
			SchemaVersion: 1,
			Name:          image.name,
			Tag:           image.tag,
			SourceVM:      "source-vm",
			DiskSize:      image.size,
			CreatedAt:     created,
		}); err != nil {
			t.Fatalf("write image manifest: %v", err)
		}
	}
	store = filepath.Join(root, "bookmarks.json")
	grantEnv = append(append([]string{}, env...), imagestore.BaseDirEnv+"="+imageRoot, securityBookmarkStoreEnv+"="+store)
	return imageRoot, store, grantEnv
}

func TestAppSandboxBookmarkProbeSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "security", "bookmark-probe", "-json")
	t.Logf("sandboxed macgo security bookmark-probe err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo security bookmark-probe: %v\n%s", err, out)
	}
	var report securityScopedBookmarkReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &report); err != nil {
		t.Fatalf("security bookmark-probe json: %v\n%s", err, out)
	}
	if !report.AppSandbox {
		t.Fatalf("security bookmark-probe apple_app_sandbox = false:\n%s", out)
	}
	if report.BookmarkSize == 0 || !report.Started || report.ReadBytes == 0 || report.SHA256 == "" {
		t.Fatalf("security bookmark-probe proof incomplete: %+v\n%s", report, out)
	}
	if report.Path == "" || report.ResolvedPath != report.Path {
		t.Fatalf("security bookmark-probe path mismatch: %+v\n%s", report, out)
	}
	if strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("security bookmark-probe crashed instead of returning proof:\n%s", out)
	}
}

func TestAppSandboxDurableBookmarkStorageSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	root := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data", "tmp", fmt.Sprintf("cove-bookmark-store-%d", os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatalf("create bookmark store root: %v", err)
	}
	target := filepath.Join(root, "grant.txt")
	if err := os.WriteFile(target, []byte("cove durable bookmark proof\n"), 0600); err != nil {
		t.Fatalf("write bookmark grant: %v", err)
	}
	store := filepath.Join(root, "bookmarks.json")
	key := "vm:durable-smoke"

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "security", "bookmark-store", "save",
		"-json", "-store", store, "-key", key, "-kind", "vm-root", "-path", target)
	t.Logf("sandboxed macgo security bookmark-store save err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo security bookmark-store save: %v\n%s", err, out)
	}
	var saved securityBookmarkStoreReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &saved); err != nil {
		t.Fatalf("security bookmark-store save json: %v\n%s", err, out)
	}
	if saved.Key != key || saved.Entry.BookmarkSize == 0 || saved.Entry.Path == "" {
		t.Fatalf("security bookmark-store save incomplete: %+v\n%s", saved, out)
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatalf("bookmark store was not written: %v", err)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "security", "bookmark-store", "resolve",
		"-json", "-store", store, "-key", key)
	t.Logf("sandboxed macgo security bookmark-store resolve err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo security bookmark-store resolve: %v\n%s", err, out)
	}
	var resolved securityBookmarkStoreReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &resolved); err != nil {
		t.Fatalf("security bookmark-store resolve json: %v\n%s", err, out)
	}
	if resolved.Key != key || resolved.Proof == nil || resolved.Proof.BookmarkSize == 0 || !resolved.Proof.Started || resolved.Proof.ReadBytes == 0 {
		t.Fatalf("security bookmark-store resolve incomplete: %+v\n%s", resolved, out)
	}
	if resolved.Proof.ResolvedPath != resolved.Proof.Path {
		t.Fatalf("security bookmark-store resolved path mismatch: %+v\n%s", *resolved.Proof, out)
	}
	if strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("security bookmark-store crashed instead of returning proof:\n%s", out)
	}
}

func TestAppSandboxMacgoBundleBookmarkConsumeSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	name := fmt.Sprintf("bookmark-consume-%d", os.Getpid())
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	storeRoot := filepath.Join(containerHome, "tmp", fmt.Sprintf("cove-bookmark-consume-%d", os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(storeRoot) })
	if err := os.MkdirAll(storeRoot, 0700); err != nil {
		t.Fatalf("create bookmark consume root: %v", err)
	}
	hostVM := filepath.Join(storeRoot, "outside-vm-root", name)
	if err := os.MkdirAll(hostVM, 0700); err != nil {
		t.Fatalf("create bookmarked VM: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostVM, "linux-disk.img"), []byte("disk"), 0600); err != nil {
		t.Fatalf("write bookmarked VM disk: %v", err)
	}
	store := filepath.Join(storeRoot, "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "security", "bookmark-store", "save",
		"-json", "-key", "vm:"+name, "-kind", "vm-root", "-path", hostVM)
	t.Logf("sandboxed macgo bookmark consume save err=%v output:\n%s", err, out)
	if err != nil || strings.Contains(out, "error:") {
		t.Fatalf("sandboxed macgo bookmark consume save: %v\n%s", err, out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "status", name)
	t.Logf("sandboxed macgo bookmarked status err=%v output:\n%s", err, out)
	if !strings.Contains(out, `vm "`+name+`" is stopped`) {
		t.Fatalf("sandboxed macgo status missing stopped VM proof:\n%s", out)
	}
	if strings.Contains(out, errPowerboxGrantRequired.Error()) || strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("sandboxed macgo status failed to consume bookmark cleanly:\n%s", out)
	}

	out, err = runSandboxSmokeCommandEnv(t, 45*time.Second, grantEnv, bin, "status", name+"-missing")
	t.Logf("sandboxed macgo missing bookmark status err=%v output:\n%s", err, out)
	if !strings.Contains(out, errPowerboxGrantRequired.Error()) {
		t.Fatalf("sandboxed macgo missing bookmark status missing grant-required error:\n%s", out)
	}
	if strings.Contains(out, "Trace/BPT trap") {
		t.Fatalf("sandboxed macgo missing bookmark status trapped:\n%s", out)
	}
}

func TestAppSandboxMacgoBundleSocketAndSubprocessSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_SMOKE=1 to build and run a sandboxed macgo bundle")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)

	out, err := runSandboxSmokeCommandEnv(t, 45*time.Second, env, bin, "security", "probe-sandbox", "-json")
	t.Logf("sandboxed macgo security probe-sandbox err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo security probe-sandbox: %v\n%s", err, out)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &probe); err != nil {
		t.Fatalf("security probe-sandbox json: %v\n%s", err, out)
	}
	if probe["apple_app_sandbox"] != true {
		t.Fatalf("security probe-sandbox apple_app_sandbox = %v, want true\n%s", probe["apple_app_sandbox"], out)
	}
	if root, _ := probe["vm_root"].(string); !strings.Contains(root, "/Library/Containers/com.tmc.cove/Data/.vz/vms") {
		t.Fatalf("security probe-sandbox vm_root = %v, want App Sandbox container VM root\n%s", probe["vm_root"], out)
	}
	if tempDir, _ := probe["temp_dir"].(string); !strings.Contains(tempDir, "/Library/Containers/com.tmc.cove/Data/tmp") {
		t.Fatalf("security probe-sandbox temp_dir = %v, want App Sandbox container temp dir\n%s", probe["temp_dir"], out)
	}
	helper, ok := probe["helper_ipc"].(map[string]any)
	if !ok {
		t.Fatalf("security probe-sandbox helper_ipc missing or wrong type: %#v\n%s", probe["helper_ipc"], out)
	}
	switch helper["status"] {
	case "pass", "skip", "blocked":
	default:
		t.Fatalf("security probe-sandbox helper_ipc = %#v, want pass, skip, or blocked\n%s", helper, out)
	}
	for _, name := range []string{"unix_socket", "loopback_tcp", "subprocess"} {
		check, ok := probe[name].(map[string]any)
		if !ok || check["status"] != "pass" {
			t.Fatalf("security probe-sandbox %s = %#v, want pass\n%s", name, probe[name], out)
		}
	}
}

func TestAppSandboxMacgoBundleScratchBootSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_MACGO_BOOT_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_MACGO_BOOT_SMOKE=1 to start and stop a sandboxed scratch VM")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	scratch := stageAppSandboxScratchBootVM(t)

	out, err := runSandboxSmokeCommandEnv(t, 2*time.Minute, env, bin, "security", "probe-sandbox",
		"-json",
		"-vz-start-vm-dir", scratch.vmDir,
		"-vz-start-disk", scratch.disk,
		"-vz-start-linux",
		"-vz-start-timeout", "45s",
	)
	t.Logf("sandboxed macgo scratch boot err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo scratch boot: %v\n%s", err, out)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &probe); err != nil {
		t.Fatalf("security probe-sandbox json: %v\n%s", err, out)
	}
	check, ok := probe["vz_start"].(map[string]any)
	if !ok || check["status"] != "pass" {
		t.Fatalf("security probe-sandbox vz_start = %#v, want pass\n%s", probe["vz_start"], out)
	}
}

func stageAppSandboxListVM(t *testing.T, vmDir string) {
	t.Helper()
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		t.Fatalf("create list VM %s: %v", vmDir, err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "linux-disk.img"), []byte("disk"), 0600); err != nil {
		t.Fatalf("write list VM disk: %v", err)
	}
}

type appSandboxScratchBootVM struct {
	vmDir string
	disk  string
}

func stageAppSandboxScratchBootVM(t *testing.T) appSandboxScratchBootVM {
	t.Helper()

	base := os.Getenv("COVE_APP_SANDBOX_BOOT_BASE")
	if base == "" {
		base = "vz-linux-test"
	}
	baseDir, ok := vmconfig.ExistingPath(base)
	if !ok {
		t.Skipf("scratch boot base VM %q not found; set COVE_APP_SANDBOX_BOOT_BASE", base)
	}
	baseDisk := filepath.Join(baseDir, "linux-disk.img")
	if _, err := os.Stat(baseDisk); err != nil {
		t.Skipf("scratch boot base VM %q missing linux-disk.img: %v", base, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	root := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data", "tmp", fmt.Sprintf("cvbt%d", os.Getpid()))
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	vmDir := filepath.Join(root, "vm-"+strings.Repeat("deep", 10))
	if direct := filepath.Join(vmDir, "control.sock"); len(direct) <= controlSocketMaxPath {
		t.Fatalf("scratch VM dir too short for socket fallback proof: %s", direct)
	}
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		t.Fatalf("create scratch vm dir: %v", err)
	}

	for _, name := range []string{"config.json", "mac.address", "linux-machine.id", "cloud-init.iso", "efi.nvram", "efi-vars.img"} {
		src := filepath.Join(baseDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, filepath.Join(vmDir, name)); err != nil {
			t.Fatalf("copy %s: %v", name, err)
		}
	}
	disk := filepath.Join(vmDir, "linux-disk.img")
	if err := cloneFile(baseDisk, disk); err != nil {
		t.Skipf("clone scratch boot disk: %v", err)
	}

	return appSandboxScratchBootVM{
		vmDir: vmDir,
		disk:  disk,
	}
}

func buildAppSandboxSmokeBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("codesign"); err != nil {
		t.Skipf("codesign unavailable: %v", err)
	}

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "cove-sandboxed")
	entitlements, err := filepath.Abs(filepath.Join("internal", "autosign", "app_sandbox.entitlements"))
	if err != nil {
		t.Fatalf("resolve app sandbox entitlements: %v", err)
	}

	out, err := runSandboxSmokeCommand(t, 3*time.Minute, "go", "build", "-o", bin, ".")
	if err != nil {
		t.Fatalf("build sandbox smoke binary: %v\n%s", err, out)
	}
	out, err = runSandboxSmokeCommand(t, time.Minute, "codesign", "-s", "-", "-f", "--entitlements", entitlements, bin)
	if err != nil {
		t.Fatalf("sign sandbox smoke binary: %v\n%s", err, out)
	}
	out, err = runSandboxSmokeCommand(t, time.Minute, "codesign", "-d", "--entitlements", ":-", bin)
	if err != nil {
		t.Fatalf("inspect entitlements: %v\n%s", err, out)
	}
	t.Logf("entitlements:\n%s", out)
	out, err = runSandboxSmokeCommand(t, time.Minute, "spctl", "--assess", "--type", "execute", "-vv", bin)
	t.Logf("spctl err=%v output:\n%s", err, out)

	return bin
}

func buildMacgoBundleSmokeBinary(t *testing.T) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("codesign"); err != nil {
		t.Skipf("codesign unavailable: %v", err)
	}
	if _, err := exec.LookPath("open"); err != nil {
		t.Skipf("open unavailable: %v", err)
	}

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "cove")
	out, err := runSandboxSmokeCommand(t, 3*time.Minute, "go", "build", "-o", bin, ".")
	if err != nil {
		t.Fatalf("build cove: %v\n%s", err, out)
	}
	return bin, []string{
		coveAppSandboxMacgoEnv + "=1",
		"GOPATH=" + tmp,
		"MACGO_KEEP_BUNDLE=0",
	}
}

func withoutEnv(env []string, key string) []string {
	prefix := key + "="
	out := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func assertSandboxDoctorCommands(t *testing.T, run func(args ...string) (string, error)) {
	t.Helper()

	out, err := run("doctor", "host", "-json")
	t.Logf("sandbox doctor host err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandbox doctor host: %v\n%s", err, out)
	}
	var host map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &host); err != nil {
		t.Fatalf("doctor host json: %v\n%s", err, out)
	}
	if _, ok := host["checks"].([]any); !ok {
		t.Fatalf("doctor host json missing checks array:\n%s", out)
	}

	out, err = run("doctor", "sckit-preauth", "-json")
	t.Logf("sandbox doctor sckit-preauth err=%v output:\n%s", err, out)
	if err != nil && !isCommandExit(err) {
		t.Fatalf("sandbox doctor sckit-preauth: %v\n%s", err, out)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &probe); err != nil {
		t.Fatalf("doctor sckit-preauth json: %v\n%s", err, out)
	}
	for _, key := range []string{"SCKitAvailable", "ScreenRecordingAuthorized", "MacOSVersion"} {
		if _, ok := probe[key]; !ok {
			t.Fatalf("doctor sckit-preauth json missing %q:\n%s", key, out)
		}
	}
}

func firstJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

func firstJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

func isCommandExit(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit)
}

func freeLocalTCPAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free tcp addr: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func startSandboxServeSmoke(t *testing.T, env []string, tokenFile, healthURL, name string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("serve stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("serve stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		stopSandboxServeSmoke(tokenFile)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	})

	lines := make(chan string, 64)
	scan := func(r io.Reader) {
		s := bufio.NewScanner(r)
		for s.Scan() {
			lines <- s.Text()
		}
	}
	go scan(stdout)
	go scan(stderr)

	var output strings.Builder
	deadline := time.After(45 * time.Second)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	client := http.Client{Timeout: time.Second}
	for {
		select {
		case line := <-lines:
			output.WriteString(line)
			output.WriteByte('\n')
		case <-tick.C:
			resp, err := client.Get(healthURL)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return output.String()
			}
		case err := <-done:
			t.Fatalf("serve exited before listening: %v\n%s", err, output.String())
		case <-deadline:
			t.Fatalf("serve did not report listener before timeout\n%s", output.String())
		}
	}
}

func stopSandboxServeSmoke(tokenFile string) {
	_ = exec.Command("pkill", "-TERM", "-f", tokenFile).Run()
}

func runSandboxSmokeCommand(t *testing.T, timeout time.Duration, name string, args ...string) (string, error) {
	t.Helper()

	return runSandboxSmokeCommandEnv(t, timeout, nil, name, args...)
}

func runSandboxSmokeCommandEnv(t *testing.T, timeout time.Duration, env []string, name string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = "."
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%s %s: timeout after %s", name, strings.Join(args, " "), timeout)
	}
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func runSandboxedCoveSmokeCommand(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%s %s: timeout after 15s", bin, strings.Join(args, " "))
	}
	return string(out), err
}
