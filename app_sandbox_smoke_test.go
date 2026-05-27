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
	if err != nil {
		t.Fatalf("sandboxed macgo bundle list: %v\n%s", err, out)
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
	for _, name := range []string{"unix_socket", "subprocess"} {
		check, ok := probe[name].(map[string]any)
		if !ok || check["status"] != "pass" {
			t.Fatalf("security probe-sandbox %s = %#v, want pass\n%s", name, probe[name], out)
		}
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
