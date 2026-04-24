//go:build integration && darwin && arm64

package main

import (
	"context"
	"flag"
	"fmt"
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
	flagIntegrationProvision     = flag.Bool("integration.provision", envBoolDefault("VZ_TEST_PROVISION", true), "provision missing integration VMs automatically")
	flagIntegrationReprovision   = flag.Bool("integration.reprovision", envBool("VZ_TEST_REPROVISION"), "delete and rebuild integration VMs before running")
	flagIntegrationUser          = flag.String("integration.user", envOrString("VZ_TEST_USER", "vztest"), "username for auto-provisioned macOS integration VMs")
	flagIntegrationPassword      = flag.String("integration.password", envOrString("VZ_TEST_PASSWORD", "vztest123"), "password for auto-provisioned macOS integration VMs")
	flagIntegrationIPSW          = flag.String("integration.ipsw", strings.TrimSpace(os.Getenv("VZ_TEST_IPSW")), "path to IPSW for auto-provisioned macOS integration VMs")
	flagIntegrationLinuxUser     = flag.String("integration.linux-user", envOrString("VZ_TEST_LINUX_USER", "ubuntu"), "username for auto-provisioned Linux integration VMs")
	flagIntegrationLinuxPassword = flag.String("integration.linux-password", envOrString("VZ_TEST_LINUX_PASSWORD", "ubuntu"), "password for auto-provisioned Linux integration VMs")
	flagIntegrationLinuxCmdline  = flag.String("integration.linux-cmdline", envOrString("VZ_TEST_LINUX_CMDLINE", "boot=casper autoinstall ds=nocloud console=hvc0 console=tty0"), "kernel command line for auto-provisioned Linux integration VMs")
	integrationProvisionedVMs    sync.Map
)

func init() {
	_ = os.Setenv("VZ_TEST_INTEGRATION_BUILD", "1")
}

func envBoolDefault(name string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func ensureIntegrationBaseVM(tb testing.TB, name string, linux bool) bool {
	tb.Helper()

	dir := resolvePath(vmconfig.Path(name))
	fresh := false
	key := fmt.Sprintf("%t:%s", linux, name)
	_, alreadyProvisionedThisRun := integrationProvisionedVMs.Load(key)
	if !linux {
		if strings.TrimSpace(*flagIntegrationSIPUser) == "" {
			*flagIntegrationSIPUser = strings.TrimSpace(*flagIntegrationUser)
		}
		if strings.TrimSpace(*flagIntegrationSIPPassword) == "" {
			*flagIntegrationSIPPassword = strings.TrimSpace(*flagIntegrationPassword)
		}
	}
	if *flagIntegrationReprovision && !alreadyProvisionedThisRun {
		stopIntegrationVMIfRunning(tb, dir)
		if err := os.RemoveAll(dir); err != nil {
			tb.Fatalf("remove integration vm %q: %v", name, err)
		}
	}

	if !integrationBaseReady(dir, linux) {
		if !*flagIntegrationProvision {
			tb.Skipf("integration VM %q not provisioned at %s", name, dir)
		}
		tb.Logf("provisioning %s integration VM %q", map[bool]string{true: "Linux", false: "macOS"}[linux], name)
		stopIntegrationVMIfRunning(tb, dir)
		if err := os.RemoveAll(dir); err != nil {
			tb.Fatalf("reset integration vm dir %q: %v", dir, err)
		}
		if linux {
			provisionLinuxIntegrationVM(tb, name)
		} else {
			provisionMacOSIntegrationVM(tb, name)
		}
		fresh = true
	}

	if _, err := EnsureControlTokenForVM(dir); err != nil {
		tb.Fatalf("ensure control token for %q: %v", name, err)
	}
	integrationProvisionedVMs.Store(key, struct{}{})
	return fresh
}

func integrationBaseReady(dir string, linux bool) bool {
	if !vmconfig.Validate(dir) {
		return false
	}
	if linux {
		if _, err := os.Stat(linuxInstalledMarkerPath(dir)); err != nil {
			return false
		}
		if !hasInstalledLinuxBootArtifacts(dir) {
			return false
		}
		return true
	}
	_, err := os.Stat(filepath.Join(dir, ".inject-succeeded"))
	return err == nil
}

func stopIntegrationVMIfRunning(tb testing.TB, dir string) {
	tb.Helper()

	tokenPath := GetControlTokenPathForVM(dir)
	token, err := LoadControlTokenFromPath(tokenPath)
	if err != nil {
		return
	}
	sock := GetControlSocketPathForVM(dir)
	if !controlSocketReady(sock, token) {
		return
	}

	req := &controlpb.ControlRequest{Type: "stop", AuthToken: token}
	if _, err := ctlSendRequest(sock, req, 30*time.Second, req.Type); err != nil {
		tb.Logf("stop running integration vm %s: %v", dir, err)
		return
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if !controlSocketReady(sock, token) {
			return
		}
		time.Sleep(2 * time.Second)
	}
	tb.Fatalf("integration vm %s did not stop", dir)
}

func provisionLinuxIntegrationVM(tb testing.TB, name string) {
	tb.Helper()

	bin := buildIntegrationBinary(tb)
	args := []string{
		"-vm", name,
		"-linux",
		"-headless",
		"-verbose",
		"-cmdline", strings.TrimSpace(*flagIntegrationLinuxCmdline),
		"-provision-user", strings.TrimSpace(*flagIntegrationLinuxUser),
		"-provision-password", strings.TrimSpace(*flagIntegrationLinuxPassword),
		"install",
	}
	runProvisioningCommand(tb, 90*time.Minute, bin, args...)
}

func provisionMacOSIntegrationVM(tb testing.TB, name string) {
	tb.Helper()

	bin := buildIntegrationBinary(tb)
	installArgs := []string{"-vm", name, "-verbose"}
	if integrationHeadlessMode(false) {
		installArgs = append(installArgs, "-headless")
	} else {
		installArgs = append(installArgs, "-gui")
	}
	ipsw := strings.TrimSpace(*flagIntegrationIPSW)
	if ipsw == "" {
		cached := filepath.Join(vmconfig.CacheDir(), "RestoreImage.ipsw")
		if _, err := os.Stat(cached); err == nil {
			ipsw = cached
		}
	}
	if ipsw != "" {
		installArgs = append(installArgs, "-ipsw", ipsw)
	}
	installArgs = append(installArgs, "install")
	runProvisioningCommand(tb, 2*time.Hour, bin, installArgs...)

	provisionArgs := []string{
		"-vm", name,
		"-verbose",
		"provision",
		"-user", strings.TrimSpace(*flagIntegrationUser),
		"-password", strings.TrimSpace(*flagIntegrationPassword),
		"-skip-setup-assistant",
	}
	runProvisioningCommand(tb, 30*time.Minute, bin, provisionArgs...)
}

func runProvisioningCommand(tb testing.TB, timeout time.Duration, bin string, args ...string) {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	logFile, err := os.CreateTemp("", "cove-provision-*.log")
	if err != nil {
		tb.Fatalf("create provisioning log: %v", err)
	}
	logPath := logFile.Name()
	defer logFile.Close()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		tb.Fatalf("start %s %v: %v", bin, args, err)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			tb.Fatalf("%s %v: timed out after %s\nlog: %s", bin, args, timeout, logPath)
		}
		tb.Fatalf("%s %v: %v\nlog: %s\n%s", bin, args, err, logPath, tailFile(logPath, 200))
	}
	tb.Logf("completed %s %v (log: %s)", filepath.Base(bin), args, logPath)
}

func tailFile(path string, maxLines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("read log %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
