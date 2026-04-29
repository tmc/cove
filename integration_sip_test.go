//go:build integration && darwin && arm64

package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func testSIP(t *testing.T, vm *testVM) {
	requireGUI(t)

	status := strings.ToLower(agentExec(t, vm, "/usr/bin/csrutil", "status"))
	switch {
	case strings.Contains(status, "disabled"):
		t.Skip("SIP already disabled")
	case !strings.Contains(status, "enabled"):
		t.Fatalf("unexpected csrutil status output: %q", status)
	}

	bin := buildIntegrationBinary(t)
	recoveryDisk, bootCommands := prepareSIPDisableArtifacts(t, vm)

	ctlDo(t, vm, &controlpb.ControlRequest{Type: "stop"})
	waitSocketClosed(t, vm.sock, 2*time.Minute)

	runIntegrationCommand(t, 20*time.Minute, bin,
		"-vm", vm.name,
		"-recovery",
		"-no-resume",
		"-gui",
		"-unattended",
		"-usb", recoveryDisk,
		"-boot-commands", bootCommands,
		"run",
	)

	startTestVM(t, vm)
	waitVMReady(t, vm, 5*time.Minute)

	status = strings.ToLower(agentExec(t, vm, "/usr/bin/csrutil", "status"))
	if !strings.Contains(status, "disabled") {
		t.Fatalf("csrutil status after recovery flow: got %q, want output containing %q", status, "disabled")
	}
}

func prepareSIPDisableArtifacts(t *testing.T, vm *testVM) (recoveryDisk, bootCommands string) {
	t.Helper()

	withVMGlobals(t, vm, func() {
		var err error
		recoveryDisk, err = EnsureRecoveryDisk(vm.dir)
		if err != nil {
			t.Fatalf("ensure recovery disk: %v", err)
		}
		script, err := generateSIPVZScript(
			"disable",
			*flagIntegrationSIPUser,
			*flagIntegrationSIPPassword,
		)
		if err != nil {
			t.Fatalf("generate SIP vzscript: %v", err)
		}
		bootCommands, err = writeVZScriptForSIP(vm.dir, "disable", script)
		if err != nil {
			t.Fatalf("write SIP vzscript: %v", err)
		}
	})
	return recoveryDisk, bootCommands
}

func runIntegrationCommand(t *testing.T, timeout time.Duration, bin string, args ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("%s %v: timed out after %s\n%s", bin, args, timeout, out)
	}
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", bin, args, err, out)
	}
}
