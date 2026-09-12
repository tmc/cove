package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmrun"
)

// This test runs real QEMU and requires an offline, installed Windows disk with
// both TCP agents. It never boots the source disk or copies a live disk.
func TestWindowsQEMULiveLifecycle(t *testing.T) {
	source := os.Getenv("COVE_WINDOWS_SMOKE_SOURCE_DISK")
	if source == "" {
		t.Skip("set COVE_WINDOWS_SMOKE_SOURCE_DISK to an offline installed Windows disk to run the disposable live smoke")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(source)
	if err != nil || !before.Mode().IsRegular() {
		t.Fatalf("source must be a regular disk image: %v", err)
	}
	if out, err := exec.Command("lsof", "-t", "--", source).CombinedOutput(); err == nil || len(out) != 0 {
		t.Fatalf("source must be offline; lsof: %s (%v)", out, err)
	} else if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
		t.Fatalf("cannot verify source is offline: %v", err)
	}
	dir, err := os.MkdirTemp("/tmp", "cove-windows-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live evidence directory: %s", dir)
	disk := filepath.Join(dir, "disk"+filepath.Ext(source))
	t.Cleanup(func() {
		os.Remove(disk)
		os.Remove(filepath.Join(dir, "qemu", "efi_vars.fd"))
		after, err := os.Stat(source)
		if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			t.Errorf("source disk identity/size/mtime changed: %v", err)
		}
	})
	if out, err := exec.Command("cp", "-c", source, disk).CombinedOutput(); err != nil {
		t.Fatalf("APFS disposable clone required (no full-copy fallback): %v: %s", err, out)
	}
	cloned, err := os.Stat(disk)
	if err != nil || os.SameFile(before, cloned) {
		t.Fatalf("clone must be a distinct file: %v", err)
	}
	if err := os.Chmod(disk, 0600); err != nil {
		t.Fatal(err)
	}
	old := windowsNetworkFlags
	t.Cleanup(func() { windowsNetworkFlags = old })
	for _, name := range []string{"COVE_QEMU_AGENT_HOST_PORT", "COVE_QEMU_AGENT_GUEST_PORT", "COVE_QEMU_USER_AGENT_HOST_PORT", "COVE_QEMU_USER_AGENT_GUEST_PORT", "COVE_QEMU_AGENT_FORWARD", "COVE_QEMU_USER_AGENT_FORWARD"} {
		t.Setenv(name, "")
	}
	daemonPort, err := pickFreeLocalTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	userPort, err := pickFreeLocalTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	for userPort == daemonPort {
		userPort, err = pickFreeLocalTCPPort()
		if err != nil {
			t.Fatal(err)
		}
	}
	windowsNetworkFlags = windowsNetworkOverrides{NetworkSet: true, AgentHostPort: fmt.Sprint(daemonPort), UserAgentHostPort: fmt.Sprint(userPort), AgentGuestPort: os.Getenv("COVE_WINDOWS_SMOKE_AGENT_GUEST_PORT"), UserAgentGuestPort: os.Getenv("COVE_WINDOWS_SMOKE_USER_AGENT_GUEST_PORT")}
	timeout := 2 * time.Minute
	if value := os.Getenv("COVE_WINDOWS_SMOKE_TIMEOUT"); value != "" {
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			t.Fatalf("invalid COVE_WINDOWS_SMOKE_TIMEOUT %q", value)
		}
	}
	for cycle := 1; cycle <= 2; cycle++ {
		cfg, err := windowsQEMUConfigFromRun(vmrun.RunConfig{DiskPath: disk, CPUCount: 2, MemoryGB: 4, NetworkMode: "nat", Headless: true}, vmrun.HostConfig{VMDir: dir}, false)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AgentHostPort != daemonPort || cfg.UserAgentHostPort != userPort {
			t.Fatalf("cycle %d did not restore host ports: %d/%d", cycle, cfg.AgentHostPort, cfg.UserAgentHostPort)
		}
		if err := ensureWindowsQEMUEFIVars(cfg.EFIVarsPath, cfg.EFIVarsTemplatePath); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var runErr error
		go func() { runErr = runWindowsQEMUContext(ctx, cfg, false); close(done) }()
		stop := func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("QEMU failed to exit after cancellation")
			}
		}
		t.Cleanup(stop)
		deadline := time.Now().Add(timeout)
		var lastErr error
		ready := false
		for time.Now().Before(deadline) {
			select {
			case <-done:
				t.Fatalf("live QEMU exited before agents became ready: %v", runErr)
			default:
			}
			ready = true
			for _, address := range []string{windowsQEMUAgentEndpoint(cfg), windowsQEMUUserAgentEndpoint(cfg)} {
				client, err := qemuAgentClient(address)
				if err != nil {
					t.Fatal(err)
				}
				probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
				info, err := client.Info(probeCtx)
				probeCancel()
				client.Close()
				if err != nil {
					lastErr = err
					ready = false
					break
				}
				if !strings.Contains(strings.ToLower(info.GetOsVersion()), "windows") {
					t.Fatalf("live endpoint %s did not identify Windows: %s", address, info.GetOsVersion())
				}
			}
			if ready {
				break
			}
			time.Sleep(time.Second)
		}
		if !ready {
			t.Fatalf("live Windows agents unavailable after %s; QEMU evidence in %s; last RPC error: %v", timeout, dir, lastErr)
		}
		client, err := qemuAgentClient(windowsQEMUAgentEndpoint(cfg))
		if err != nil {
			t.Fatal(err)
		}
		commandCtx, commandCancel := context.WithTimeout(context.Background(), 10*time.Second)
		result, err := client.Exec(commandCtx, []string{"cmd.exe", "/d", "/c", "echo cove-windows-live"}, nil, "")
		commandCancel()
		if err != nil || result.GetExitCode() != 0 || !strings.Contains(string(result.GetStdout()), "cove-windows-live") {
			client.Close()
			t.Fatalf("live exec failed: %v (%v)", err, result)
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = client.Shutdown(shutdownCtx, false)
		shutdownCancel()
		client.Close()
		if err != nil {
			t.Fatalf("live guest shutdown: %v", err)
		}
		select {
		case <-done:
			if runErr != nil {
				t.Fatalf("live QEMU shutdown: %v", runErr)
			}
		case <-time.After(time.Minute):
			t.Fatal("live Windows did not shut down within one minute")
		}
		cancel()
		t.Logf("live cycle %d passed: Windows agent info, exec, shutdown; ports %d/%d", cycle, daemonPort, userPort)
		windowsNetworkFlags = windowsNetworkOverrides{}
	}
}
