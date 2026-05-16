//go:build integration && darwin && arm64

package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

var (
	flagIntegrationVM          = flag.String("integration.vm", envOrString("VZ_TEST_VM", "cove-test"), "macOS VM name for integration tests")
	flagIntegrationLinuxVM     = flag.String("integration.linux-vm", envOrString("VZ_TEST_LINUX_VM", "vz-linux-test"), "Linux VM name for integration tests")
	flagIntegrationHeadless    = flag.Bool("integration.headless", integrationEnvBool("VZ_TEST_HEADLESS"), "skip GUI-dependent integration tests")
	flagIntegrationHeaded      = flag.Bool("integration.headed", integrationEnvBool("VZ_TEST_HEADED"), "force GUI mode for macOS integration provisioning and runtime")
	flagIntegrationSIP         = flag.Bool("integration.sip", integrationEnvBool("VZ_TEST_SIP"), "run SIP recovery integration test")
	flagIntegrationSIPUser     = flag.String("integration.sip-user", os.Getenv("VZ_TEST_SIP_USER"), "recovery auth username for SIP integration test")
	flagIntegrationSIPPassword = flag.String("integration.sip-password", os.Getenv("VZ_TEST_SIP_PASSWORD"), "recovery auth password for SIP integration test")
)

type testVM struct {
	name          string
	dir           string
	sock          string
	token         string
	linux         bool
	startedByTest bool
	cmd           *exec.Cmd
	waitCh        chan error
	logPath       string
}

var (
	integrationBinaryOnce sync.Once
	integrationBinaryPath string
	integrationBinaryErr  error
)

func TestIntegration(t *testing.T) {
	vm := acquireTestVM(t)
	t.Cleanup(func() { vm.Cleanup(t) })

	t.Run("agent", func(t *testing.T) { testAgent(t, vm) })
	t.Run("ctl", func(t *testing.T) { testCtl(t, vm) })
	t.Run("runtime-surface", func(t *testing.T) { testRuntimeSurface(t, vm) })
	t.Run("virtiofs-reliability", func(t *testing.T) { testVirtioFSReliability(t, vm) })
	t.Run("network", func(t *testing.T) { testNetwork(t, vm) })
	t.Run("port-forward", func(t *testing.T) { testPortForward(t, vm) })
	t.Run("vm-config", func(t *testing.T) { testVMConfig(t, vm) })
	t.Run("vzscript", func(t *testing.T) { testVZScript(t, vm) })
	t.Run("host-cp", func(t *testing.T) { testHostCp(t, vm) })
	if *flagIntegrationSIP {
		t.Run("sip", func(t *testing.T) { testSIP(t, vm) })
	}
}

func TestLinuxIntegration(t *testing.T) {
	vm := acquireLinuxTestVM(t)
	t.Cleanup(func() { vm.Cleanup(t) })

	t.Run("agent", func(t *testing.T) { testLinuxAgent(t, vm) })
	t.Run("ctl", func(t *testing.T) { testLinuxCtl(t, vm) })
	t.Run("runtime-surface", func(t *testing.T) { testRuntimeSurface(t, vm) })
	t.Run("network", func(t *testing.T) { testLinuxNetwork(t, vm) })
	t.Run("vm-config", func(t *testing.T) { testVMConfig(t, vm) })
}

func integrationEnvBool(name string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func acquireTestVM(t *testing.T) *testVM {
	return acquireIntegrationVM(t)
}

func acquireLinuxTestVM(t *testing.T) *testVM {
	t.Helper()

	name := strings.TrimSpace(*flagIntegrationLinuxVM)
	if name == "" {
		t.Skip("set -integration.linux-vm or VZ_TEST_LINUX_VM to a Linux VM name")
	}
	fresh := ensureIntegrationBaseVM(t, name, true)

	dir := resolvePath(vmconfig.Path(name))

	tokenPath := GetControlTokenPathForVM(dir)
	token, err := LoadControlTokenFromPath(tokenPath)
	if err != nil {
		t.Fatalf("load control token %q: %v", tokenPath, err)
	}

	vm := &testVM{
		name:  name,
		dir:   dir,
		sock:  GetControlSocketPathForVM(dir),
		token: token,
		linux: true,
	}
	if !controlSocketReady(vm.sock, vm.token) {
		startTestVM(t, vm)
	}
	timeout := integrationVMReadyTimeout(vm, fresh)
	waitVMReadyTB(t, vm, timeout)
	return vm
}

func (vm *testVM) Cleanup(t *testing.T) {
	vm.cleanupTB(t)
}

func acquireIntegrationVM(tb testing.TB) *testVM {
	tb.Helper()

	name := strings.TrimSpace(*flagIntegrationVM)
	if name == "" {
		tb.Skip("set -integration.vm or VZ_TEST_VM to a running VM name")
	}
	fresh := ensureIntegrationBaseVM(tb, name, false)

	dir := resolvePath(vmconfig.Path(name))

	tokenPath := GetControlTokenPathForVM(dir)
	token, err := LoadControlTokenFromPath(tokenPath)
	if err != nil {
		tb.Fatalf("load control token %q: %v", tokenPath, err)
	}

	vm := &testVM{
		name:  name,
		dir:   dir,
		sock:  GetControlSocketPathForVM(dir),
		token: token,
	}
	if !controlSocketReady(vm.sock, vm.token) {
		startTestVM(tb, vm)
	}
	timeout := integrationVMReadyTimeout(vm, fresh)
	waitVMReadyTB(tb, vm, timeout)
	return vm
}

func (vm *testVM) cleanupTB(tb testing.TB) {
	tb.Helper()

	if !vm.startedByTest {
		return
	}
	defer func() {
		vm.startedByTest = false
		vm.cmd = nil
		vm.waitCh = nil
	}()

	if vm.waitCh != nil {
		select {
		case err := <-vm.waitCh:
			if err != nil {
				tb.Logf("vm process %s exited: %v", vm.name, err)
			}
			vm.removeStaleSocket(tb)
			return
		default:
		}
	}

	if controlSocketReady(vm.sock, vm.token) {
		req := &controlpb.ControlRequest{Type: "stop", AuthToken: vm.token}
		if _, err := ctlSendRequest(vm.sock, req, 30*time.Second, req.Type); err != nil {
			tb.Logf("stop %s: %v", vm.name, err)
		}
	}

	if vm.waitCh != nil {
		select {
		case err := <-vm.waitCh:
			if err != nil {
				tb.Logf("vm process %s exited after stop: %v", vm.name, err)
			}
			vm.removeStaleSocket(tb)
			return
		case <-time.After(20 * time.Second):
		}
	}

	if vm.cmd != nil && vm.cmd.Process != nil {
		if err := vm.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
			tb.Logf("kill vm process %s: %v", vm.name, err)
		}
	}
	if vm.waitCh != nil {
		select {
		case err := <-vm.waitCh:
			if err != nil {
				tb.Logf("vm process %s killed: %v", vm.name, err)
			}
		case <-time.After(5 * time.Second):
			tb.Logf("vm process %s did not exit after kill", vm.name)
		}
	}
	vm.removeStaleSocket(tb)
}

func waitVMReady(t *testing.T, vm *testVM, timeout time.Duration) {
	waitVMReadyTB(t, vm, timeout)
}

func waitVMReadyTB(tb testing.TB, vm *testVM, timeout time.Duration) {
	tb.Helper()

	if err := waitControlReady(vm.sock, vm.token, timeout); err != nil {
		tb.Fatalf("wait for control socket: %v%s", err, integrationFailureContext(vm))
	}
	if err := waitAgentReady(vm.sock, vm.token, timeout); err != nil {
		tb.Fatalf("wait for guest agent: %v%s", err, integrationFailureContext(vm))
	}
}

func integrationVMReadyTimeout(vm *testVM, fresh bool) time.Duration {
	if vm != nil && vm.linux {
		if fresh {
			return 15 * time.Minute
		}
		return 6 * time.Minute
	}
	if fresh {
		return 12 * time.Minute
	}
	return 4 * time.Minute
}

func integrationFailureContext(vm *testVM) string {
	if vm == nil {
		return ""
	}

	var details []string
	if vm.logPath != "" {
		if tail := strings.TrimSpace(tailFile(vm.logPath, 80)); tail != "" {
			details = append(details, fmt.Sprintf("\nvm log (%s):\n%s", vm.logPath, tail))
		}
	}
	if vm.linux && vm.dir != "" {
		for _, name := range []string{"boot-serial.log", "install-serial.log"} {
			serialPath := filepath.Join(vm.dir, name)
			if tail := strings.TrimSpace(tailFile(serialPath, 80)); tail != "" && !strings.HasPrefix(tail, "read log ") {
				label := strings.TrimSuffix(name, filepath.Ext(name))
				details = append(details, fmt.Sprintf("\n%s (%s):\n%s", label, serialPath, tail))
			}
		}
	}
	if len(details) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(details, "\n")
}

func waitControlReady(sock, token string, timeout time.Duration) error {
	return waitForRequest(sock, token, timeout, &controlpb.ControlRequest{Type: "ping"})
}

func waitAgentReady(sock, token string, timeout time.Duration) error {
	return waitForRequest(sock, token, timeout, &controlpb.ControlRequest{Type: "agent-ping"})
}

func controlSocketReady(sock, token string) bool {
	req := &controlpb.ControlRequest{Type: "ping", AuthToken: token}
	resp, err := ctlSendRequest(sock, req, 2*time.Second, req.Type)
	return err == nil && resp.Success
}

func startTestVM(tb testing.TB, vm *testVM) {
	startTestVMWithArgs(tb, vm)
}

func startTestVMWithArgs(tb testing.TB, vm *testVM, extraArgs ...string) {
	tb.Helper()

	if vm.waitCh != nil {
		select {
		case err := <-vm.waitCh:
			if err != nil {
				tb.Logf("previous vm process %s exited: %v", vm.name, err)
			}
			vm.cmd = nil
			vm.waitCh = nil
		case <-time.After(10 * time.Second):
			tb.Fatalf("previous vm process %q is still running", vm.name)
		}
	}

	bin := buildIntegrationBinary(tb)
	logFile, err := os.CreateTemp("", "cove-integration-run-*.log")
	if err != nil {
		tb.Fatalf("create vm log file: %v", err)
	}
	vm.logPath = logFile.Name()

	args := []string{"-vm", vm.name}
	if vm.linux {
		args = append(args, "-linux")
	}
	modeExplicit := false
	for _, arg := range extraArgs {
		switch arg {
		case "-gui", "-headless":
			modeExplicit = true
		}
	}
	if !modeExplicit {
		if integrationHeadlessMode(vm.linux) {
			args = append(args, "-headless")
		} else {
			args = append(args, "-gui")
		}
	}
	if vm.linux {
		args = append(args, "-serial", filepath.Join(vm.dir, "boot-serial.log"))
	}
	args = append(args, extraArgs...)
	args = append(args, "run")

	cmd := exec.Command(bin, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		tb.Fatalf("start vm %q: %v", vm.name, err)
	}
	if err := logFile.Close(); err != nil {
		tb.Logf("close vm log %q: %v", vm.logPath, err)
	}

	vm.startedByTest = true
	vm.cmd = cmd
	vm.waitCh = make(chan error, 1)
	go func() {
		vm.waitCh <- cmd.Wait()
	}()
}

func integrationHeadlessMode(linux bool) bool {
	if linux {
		return true
	}
	if *flagIntegrationHeaded {
		return false
	}
	return *flagIntegrationHeadless
}

func (vm *testVM) removeStaleSocket(tb testing.TB) {
	tb.Helper()

	if controlSocketReady(vm.sock, vm.token) {
		return
	}
	if err := os.Remove(vm.sock); err != nil && !os.IsNotExist(err) {
		tb.Logf("remove stale socket %s: %v", vm.sock, err)
	}
}

func waitForRequest(sock, token string, timeout time.Duration, req *controlpb.ControlRequest) error {
	deadline := time.Now().Add(timeout)
	lastErr := fmt.Errorf("timeout after %s", timeout)

	for time.Now().Before(deadline) {
		req.AuthToken = token
		resp, err := ctlSendRequest(sock, req, 5*time.Second, req.Type)
		if err == nil && resp.Success {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("%s", resp.Error)
		}
		time.Sleep(2 * time.Second)
	}

	return lastErr
}

func waitVMState(t *testing.T, vm *testVM, want string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := statusOnce(vm)
		if err == nil && canonicalVMState(status.GetState()) == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	status, err := statusOnce(vm)
	if err != nil {
		t.Fatalf("wait for state %q: %v", want, err)
	}
	t.Fatalf("wait for state %q: got %q", want, status.GetState())
}

func waitSocketClosed(t *testing.T, sock string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("socket %q still accepting connections after %s", sock, timeout)
}

func ctlDo(t *testing.T, vm *testVM, req *controlpb.ControlRequest) *controlpb.ControlResponse {
	t.Helper()

	req.AuthToken = vm.token
	resp, err := ctlSendRequest(vm.sock, req, 30*time.Second, req.Type)
	if err != nil {
		t.Fatalf("ctl %s: %v", req.Type, err)
	}
	if !resp.Success {
		t.Fatalf("ctl %s: %s", req.Type, resp.Error)
	}
	return resp
}

func statusOnce(vm *testVM) (*controlpb.StatusResponse, error) {
	req := &controlpb.ControlRequest{
		Type:      "status",
		AuthToken: vm.token,
	}
	resp, err := ctlSendRequest(vm.sock, req, 5*time.Second, req.Type)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	status := resp.GetStatus()
	if status == nil {
		return nil, fmt.Errorf("missing status response")
	}
	return status, nil
}

func statusResponse(t *testing.T, vm *testVM) *controlpb.StatusResponse {
	t.Helper()

	status, err := statusOnce(vm)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return status
}

func requireGUI(t *testing.T) {
	t.Helper()
	if integrationHeadlessMode(false) {
		t.Skip("requires GUI mode")
	}
}

func requireUserAgent(t *testing.T, vm *testVM) {
	t.Helper()
	req := &controlpb.ControlRequest{Type: "agent-user-ping", AuthToken: vm.token}
	resp, err := ctlSendRequest(vm.sock, req, 5*time.Second, req.Type)
	if err != nil || !resp.Success {
		t.Skip("user agent not available (no GUI login session)")
	}
}

func screenshotPNG(t *testing.T, vm *testVM) []byte {
	t.Helper()

	resp := ctlDo(t, vm, &controlpb.ControlRequest{
		Type: "screenshot",
		Command: &controlpb.ControlRequest_Screenshot{
			Screenshot: &controlpb.ScreenshotCommand{
				Scale:   1,
				Quality: 100,
				Format:  "png",
			},
		},
	})
	if result := resp.GetScreenshotResult(); result != nil {
		return result.GetImageData()
	}
	if resp.Data == "" {
		t.Fatal("screenshot: missing image data")
	}
	data, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		t.Fatalf("decode screenshot: %v", err)
	}
	return data
}

func agentPingVersion(t *testing.T, vm *testVM) string {
	t.Helper()

	resp := ctlDo(t, vm, &controlpb.ControlRequest{Type: "agent-ping"})
	if ping := resp.GetAgentPing(); ping != nil {
		return ping.GetVersion()
	}
	return strings.TrimSpace(resp.Data)
}

func agentInfoResponse(t *testing.T, vm *testVM) *controlpb.AgentInfoResponse {
	t.Helper()

	resp := ctlDo(t, vm, &controlpb.ControlRequest{Type: "agent-info"})
	info := resp.GetAgentInfo()
	if info == nil {
		t.Fatal("agent-info: missing typed response")
	}
	return info
}

func agentExecResult(t *testing.T, vm *testVM, args ...string) *controlpb.AgentExecResponse {
	t.Helper()

	resp := ctlDo(t, vm, &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: args},
		},
	})
	result := resp.GetAgentExecResult()
	if result == nil {
		t.Fatal("agent-exec: missing typed response")
	}
	return result
}

func userAgentExecResult(t *testing.T, vm *testVM, args ...string) *controlpb.AgentExecResponse {
	t.Helper()

	resp := ctlDo(t, vm, &controlpb.ControlRequest{
		Type: "agent-user-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: args},
		},
	})
	result := resp.GetAgentExecResult()
	if result == nil {
		t.Fatal("agent-user-exec: missing typed response")
	}
	return result
}

func agentExecExpectCode(t *testing.T, vm *testVM, want int32, args ...string) *controlpb.AgentExecResponse {
	t.Helper()

	result := agentExecResult(t, vm, args...)
	if result.GetExitCode() != want {
		t.Fatalf("agent-exec %v: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", args, result.GetExitCode(), want, result.GetStdout(), result.GetStderr())
	}
	return result
}

func agentExec(t *testing.T, vm *testVM, args ...string) string {
	t.Helper()
	return agentExecExpectCode(t, vm, 0, args...).GetStdout()
}

func userAgentExecExpectCode(t *testing.T, vm *testVM, want int32, args ...string) *controlpb.AgentExecResponse {
	t.Helper()

	result := userAgentExecResult(t, vm, args...)
	if result.GetExitCode() != want {
		t.Fatalf("agent-user-exec %v: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", args, result.GetExitCode(), want, result.GetStdout(), result.GetStderr())
	}
	return result
}

func userAgentExec(t *testing.T, vm *testVM, args ...string) string {
	t.Helper()
	return userAgentExecExpectCode(t, vm, 0, args...).GetStdout()
}

func userAgentExecResultTimeoutTB(tb testing.TB, vm *testVM, timeout time.Duration, args ...string) *controlpb.AgentExecResponse {
	tb.Helper()

	req := &controlpb.ControlRequest{
		Type:      "agent-user-exec",
		AuthToken: vm.token,
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: args},
		},
	}
	resp, err := ctlSendRequest(vm.sock, req, timeout, req.Type)
	if err != nil {
		tb.Fatalf("agent-user-exec %v: %v", args, err)
	}
	if !resp.Success {
		tb.Fatalf("agent-user-exec %v: %s", args, resp.Error)
	}
	result := resp.GetAgentExecResult()
	if result == nil {
		tb.Fatalf("agent-user-exec %v: missing typed response", args)
	}
	return result
}

func agentRead(t *testing.T, vm *testVM, path string) []byte {
	t.Helper()

	resp := ctlDo(t, vm, &controlpb.ControlRequest{
		Type: "agent-read",
		Command: &controlpb.ControlRequest_AgentRead{
			AgentRead: &controlpb.AgentFileReadCommand{Path: path},
		},
	})
	if file := resp.GetAgentFile(); file != nil {
		return file.GetData()
	}
	if resp.Data == "" {
		t.Fatalf("agent-read %q: missing file data", path)
	}
	data, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		t.Fatalf("agent-read %q: decode: %v", path, err)
	}
	return data
}

func agentWrite(t *testing.T, vm *testVM, path string, data []byte, mode uint32) {
	t.Helper()

	ctlDo(t, vm, &controlpb.ControlRequest{
		Type: "agent-write",
		Command: &controlpb.ControlRequest_AgentWrite{
			AgentWrite: &controlpb.AgentFileWriteCommand{
				Path: path,
				Data: base64.StdEncoding.EncodeToString(data),
				Mode: mode,
			},
		},
	})
}

func agentCopyToGuest(t *testing.T, vm *testVM, hostPath, guestPath string) {
	t.Helper()

	ctlDo(t, vm, &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{
			AgentCp: &controlpb.AgentCopyCommand{
				HostPath:  hostPath,
				GuestPath: guestPath,
				ToGuest:   true,
			},
		},
	})
}

func agentCopyFromGuest(t *testing.T, vm *testVM, guestPath, hostPath string) {
	t.Helper()

	ctlDo(t, vm, &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{
			AgentCp: &controlpb.AgentCopyCommand{
				HostPath:  hostPath,
				GuestPath: guestPath,
				ToGuest:   false,
			},
		},
	})
}

func cleanupGuestPaths(t *testing.T, vm *testVM, paths ...string) {
	t.Helper()

	if len(paths) == 0 {
		return
	}
	args := append([]string{"/bin/rm", "-rf"}, paths...)
	req := &controlpb.ControlRequest{
		Type:      "agent-exec",
		AuthToken: vm.token,
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: args},
		},
	}
	resp, err := ctlSendRequest(vm.sock, req, 30*time.Second, req.Type)
	if err != nil {
		t.Logf("cleanup %v: %v", paths, err)
		return
	}
	if !resp.Success {
		t.Logf("cleanup %v: %s", paths, resp.Error)
		return
	}
	if result := resp.GetAgentExecResult(); result != nil && result.GetExitCode() != 0 {
		t.Logf("cleanup %v: exit %d: %s", paths, result.GetExitCode(), result.GetStderr())
	}
}

func portForwardRequest(action string, hostPort int, guestPort uint32) *controlpb.ControlRequest {
	return &controlpb.ControlRequest{
		Type: "port-forward",
		Command: &controlpb.ControlRequest_PortForward{
			PortForward: &controlpb.PortForwardCommand{
				Action:    action,
				HostPort:  uint32(hostPort),
				GuestPort: guestPort,
			},
		},
	}
}

func responseMessage(resp *controlpb.ControlResponse) string {
	if msg := resp.GetMessage(); msg != nil {
		return strings.TrimSpace(msg.GetMessage())
	}
	return strings.TrimSpace(resp.GetData())
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener addr type %T", ln.Addr())
	}
	return addr.Port
}

func withVMGlobals(tb testing.TB, vm *testVM, fn func()) {
	tb.Helper()

	prevVMDir := vmDir
	prevVMName := vmName
	vmDir = vm.dir
	vmName = vm.name
	tb.Cleanup(func() {
		vmDir = prevVMDir
		vmName = prevVMName
	})
	fn()
}

func buildIntegrationBinary(tb testing.TB) string {
	tb.Helper()

	integrationBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cove-integration-bin-*")
		if err != nil {
			integrationBinaryErr = err
			return
		}
		path := dir + "/cove-integration"
		cmd := exec.Command("go", "build", "-o", path, ".")
		cmd.Dir, err = os.Getwd()
		if err != nil {
			integrationBinaryErr = err
			return
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			integrationBinaryErr = fmt.Errorf("go build: %v\n%s", err, out)
			return
		}
		signCmd := exec.Command("codesign", "-s", "-", "-f", "--entitlements", "internal/autosign/vz.entitlements", path)
		signCmd.Dir = cmd.Dir
		out, err = signCmd.CombinedOutput()
		if err != nil {
			integrationBinaryErr = fmt.Errorf("codesign integration binary: %v\n%s", err, out)
			return
		}
		integrationBinaryPath = path
	})

	if integrationBinaryErr != nil {
		tb.Fatalf("build integration binary: %v", integrationBinaryErr)
	}
	return integrationBinaryPath
}

func runIntegrationBinaryCommand(tb testing.TB, args ...string) (string, error) {
	tb.Helper()

	bin := buildIntegrationBinary(tb)
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runIntegrationBinaryCommandExpectSuccess(tb testing.TB, args ...string) string {
	tb.Helper()

	out, err := runIntegrationBinaryCommand(tb, args...)
	if err != nil {
		tb.Fatalf("%s %v: %v\n%s", filepath.Base(buildIntegrationBinary(tb)), args, err, out)
	}
	return out
}

// cloneTestVM creates an isolated VM clone from the base test VM using APFS
// copy-on-write. The clone is started and cleaned up (stopped + deleted) when
// the test finishes. This enables per-test isolation for destructive operations
// like Xcode installation.
func cloneTestVM(t *testing.T, baseVM *testVM) *testVM {
	t.Helper()

	skipMacOSRunningSourceClone(t, baseVM)

	cloneName := integrationCloneName(t.Name())

	// Stop base VM if we started it, or verify it is stopped for cloning.
	// For safety we clone disk from the base VM directory directly.
	err := CloneVM(CloneOptions{
		Source: baseVM.name,
		Target: cloneName,
		Linked: true, // APFS copy-on-write
	})
	if err != nil {
		t.Fatalf("clone VM %s -> %s: %v", baseVM.name, cloneName, err)
	}

	vm := clonedTestVM(t, cloneName, baseVM.linux)
	startTestVM(t, vm)
	waitVMReadyTB(t, vm, integrationVMReadyTimeout(vm, false))
	return vm
}

func skipMacOSRunningSourceClone(t *testing.T, vm *testVM) {
	t.Helper()
	if vm.linux {
		return
	}
	status := statusResponse(t, vm)
	if status.GetState() == "running" {
		t.Skip("macOS linked clone from a running source is not reliable")
	}
}

func integrationCloneName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, name)
	name = strings.Trim(name, "-")
	if name == "" {
		name = "vm"
	}
	if len(name) > 24 {
		name = strings.Trim(name[:24], "-")
	}
	if name == "" {
		name = "vm"
	}
	return fmt.Sprintf("it-%s-%05d", name, time.Now().UnixNano()%100000)
}

func clonedTestVM(t *testing.T, cloneName string, linux bool) *testVM {
	t.Helper()

	dir := resolvePath(vmconfig.Path(cloneName))
	tokenPath := GetControlTokenPathForVM(dir)

	// CloneVM copies control.token as an optional file.
	token, err := LoadControlTokenFromPath(tokenPath)
	if err != nil {
		t.Fatalf("load control token for clone %q: %v", cloneName, err)
	}

	vm := &testVM{
		name:  cloneName,
		dir:   dir,
		sock:  GetControlSocketPathForVM(dir),
		token: token,
		linux: linux,
	}
	t.Cleanup(func() {
		vm.cleanupTB(t)
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("cleanup clone dir %s: %v", dir, err)
		}
	})
	return vm
}
