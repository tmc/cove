package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type windowsQEMUCTLStatus struct {
	Backend         string     `json:"backend"`
	State           string     `json:"state"`
	VMDir           string     `json:"vmDir"`
	MonitorSockPath string     `json:"monitorSockPath"`
	AgentEndpoint   string     `json:"agentEndpoint,omitempty"`
	CovePID         int        `json:"covePid,omitempty"`
	QEMUPID         int        `json:"qemuPid,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	ExitedAt        *time.Time `json:"exitedAt,omitempty"`
	ExitError       string     `json:"exitError,omitempty"`
}

func ctlMaybeHandleWindowsQEMU(vmDir, cmdType string, args []string, timeout, wait time.Duration, raw bool) (bool, error) {
	if !windowsQEMUCTLVM(vmDir) {
		return false, nil
	}
	switch cmdType {
	case "status":
		return true, ctlWindowsQEMUStatus(vmDir, raw)
	case "stop":
		return true, ctlWindowsQEMUStop(vmDir, timeout)
	case "request-stop":
		return true, ctlWindowsQEMURequestStop(vmDir)
	case "agent-ping":
		return true, ctlWindowsQEMUAgentPing(vmDir, wait, timeout, raw)
	case "agent-exec", "agent-exec-stream":
		return true, ctlWindowsQEMUAgentExec(vmDir, args, wait, timeout, raw)
	case "agent-shutdown":
		force := len(args) > 0 && args[0] == "force"
		return true, ctlWindowsQEMUAgentShutdown(vmDir, force, timeout)
	default:
		return true, fmt.Errorf("ctl %s is not supported for qemu windows VMs; supported commands: status, stop, request-stop, agent-ping, exec, agent-exec, agent-exec-stream, agent-shutdown", cmdType)
	}
}

func windowsQEMUCTLVM(vmDir string) bool {
	if strings.TrimSpace(vmDir) == "" {
		return false
	}
	if vmconfig.DetectOSType(vmDir) != "Windows" {
		return false
	}
	if _, err := os.Stat(filepath.Join(vmDir, "windows.qcow2")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(vmDir, "qemu", "metadata.json")); err == nil {
		return true
	}
	return false
}

func ctlWindowsQEMUStatus(vmDir string, raw bool) error {
	status := readWindowsQEMUCTLStatus(vmDir)
	if raw {
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal qemu status: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("state:   %s\n", status.State)
	fmt.Printf("backend: %s\n", status.Backend)
	fmt.Printf("vmDir:   %s\n", status.VMDir)
	fmt.Printf("monitor: %s\n", status.MonitorSockPath)
	if status.AgentEndpoint != "" {
		fmt.Printf("agent:   %s\n", status.AgentEndpoint)
	}
	if status.CovePID != 0 {
		fmt.Printf("covePid: %d\n", status.CovePID)
	}
	if status.QEMUPID != 0 {
		fmt.Printf("qemuPid: %d\n", status.QEMUPID)
	}
	if status.ExitError != "" {
		fmt.Printf("error:   %s\n", status.ExitError)
	}
	return nil
}

func readWindowsQEMUCTLStatus(vmDir string) windowsQEMUCTLStatus {
	status := windowsQEMUCTLStatus{
		Backend:         "qemu-hvf",
		State:           detectVMState(vmDir),
		VMDir:           vmDir,
		MonitorSockPath: qemuMonitorPathForVMDir(vmDir),
		AgentEndpoint:   qemuAgentAddressForVMDir(vmDir),
	}
	process := readWindowsQEMUProcessForCTL(vmDir)
	if process.State != "" {
		status.State = strings.TrimSpace(process.State)
	}
	if status.State == "" {
		status.State = "stopped"
	}
	status.CovePID = process.CovePID
	status.QEMUPID = process.QEMUPID
	if !process.StartedAt.IsZero() {
		startedAt := process.StartedAt
		status.StartedAt = &startedAt
	}
	status.ExitedAt = process.ExitedAt
	status.ExitError = process.ExitError
	return status
}

func readWindowsQEMUProcessForCTL(vmDir string) windowsQEMUProcessMetadata {
	data, err := os.ReadFile(filepath.Join(vmDir, "qemu", "process.json"))
	if err != nil {
		return windowsQEMUProcessMetadata{}
	}
	var process windowsQEMUProcessMetadata
	if err := json.Unmarshal(data, &process); err != nil {
		return windowsQEMUProcessMetadata{}
	}
	return process
}

func ctlWindowsQEMUStop(vmDir string, timeout time.Duration) error {
	monitor := qemuMonitorPathForVMDir(vmDir)
	if err := qemuMonitorCommand(monitor, "quit"); err != nil {
		return fmt.Errorf("qemu monitor unavailable; vm is not running or has exited: %w", err)
	}
	if err := waitWindowsQEMUCTLStopped(vmDir, timeout); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
}

func ctlWindowsQEMURequestStop(vmDir string) error {
	if err := qemuMonitorCommand(qemuMonitorPathForVMDir(vmDir), "system_powerdown"); err != nil {
		return fmt.Errorf("qemu monitor unavailable; vm is not running or has exited: %w", err)
	}
	fmt.Println("stop requested (ACPI power button sent)")
	return nil
}

func ctlWindowsQEMUAgentPing(vmDir string, wait, timeout time.Duration, raw bool) error {
	version, err := waitWindowsQEMUAgentPing(qemuAgentAddressForVMDir(vmDir), wait, timeout)
	if err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{Success: true, Data: "agent version: " + version}
	return ctlPrintResponse(resp, "agent-ping", raw, "")
}

func ctlWindowsQEMUAgentExec(vmDir string, args []string, wait, timeout time.Duration, raw bool) error {
	if len(args) == 0 {
		return fmt.Errorf("exec requires at least one argument")
	}
	address := qemuAgentAddressForVMDir(vmDir)
	if _, err := waitWindowsQEMUAgentPing(address, wait, timeout); err != nil {
		return err
	}
	stdout, stderr, exitCode, err := qemuAgentExecStream(vzscriptConfig{qemuAgentAddress: address}, args, timeout, nil, nil)
	if err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{
		Success: exitCode == 0,
		Result: &controlpb.ControlResponse_AgentExecResult{
			AgentExecResult: &controlpb.AgentExecResponse{
				ExitCode: exitCode,
				Stdout:   stdout,
				Stderr:   stderr,
			},
		},
	}
	if raw {
		return ctlPrintResponse(resp, "agent-exec", true, "")
	}
	if stdout != "" {
		fmt.Print(stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	if exitCode != 0 {
		return fmt.Errorf("command exited with code %d", exitCode)
	}
	return nil
}

func ctlWindowsQEMUAgentShutdown(vmDir string, force bool, timeout time.Duration) error {
	args := []string{"shutdown.exe", "/s", "/t", "0"}
	if force {
		args = append(args, "/f")
	}
	if err := ctlWindowsQEMUAgentExec(vmDir, args, 0, timeout, false); err != nil {
		return err
	}
	if err := waitWindowsQEMUCTLStopped(vmDir, timeout); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
}

func waitWindowsQEMUAgentPing(address string, wait, timeout time.Duration) (string, error) {
	if strings.TrimSpace(address) == "" {
		return "", fmt.Errorf("qemu windows agent endpoint is unavailable")
	}
	if wait <= 0 {
		return qemuAgentPing(address, timeout)
	}
	deadline := time.Now().Add(wait)
	attempt := 0
	for {
		attempt++
		version, err := qemuAgentPing(address, timeout)
		if err == nil {
			return version, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("qemu windows agent not ready after %s: %w", wait, err)
		}
		if attempt == 1 {
			fmt.Fprintf(os.Stderr, "Connecting to QEMU Windows agent (waiting up to %s)...\n", wait)
		}
		time.Sleep(2 * time.Second)
	}
}

func waitWindowsQEMUCTLStopped(vmDir string, timeout time.Duration) error {
	process := readWindowsQEMUProcessForCTL(vmDir)
	if process.QEMUPID == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if !processLive(process.QEMUPID) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for qemu pid %d to exit", process.QEMUPID)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
