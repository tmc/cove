package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmrun"
	winsetup "github.com/tmc/cove/internal/windows"
)

type windowsBackend string

const (
	windowsBackendVZ   windowsBackend = "vz"
	windowsBackendQEMU windowsBackend = "qemu"
)

func parseWindowsBackend(s string) (windowsBackend, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(windowsBackendVZ), "virtualization":
		return windowsBackendVZ, nil
	case string(windowsBackendQEMU):
		return windowsBackendQEMU, nil
	default:
		return "", fmt.Errorf("invalid -windows-backend %q (must be vz or qemu)", s)
	}
}

type windowsQEMUConfig struct {
	QEMUPath            string
	QEMUImgPath         string
	EFICodePath         string
	EFIVarsTemplatePath string

	VMDir       string
	DiskPath    string
	DiskFormat  string
	DiskSizeGB  uint64
	EFIVarsPath string
	ISOPath     string

	CPUCount            uint
	MemoryGB            uint64
	NetworkMode         string
	Headless            bool
	DisplayDevice       string
	Nodefaults          bool
	SerialOutput        string
	SerialLogPath       string
	MonitorSockPath     string
	AutounattendISOPath string
	VirtioISOPath       string
	AgentExecutablePath string
	AgentHostAddress    string
	AgentHostPort       int
	AgentGuestPort      int
}

func installWindowsQEMUVM() error {
	rc := vmrunRunConfig(vmrun.GuestWindows)
	hc := vmrunHostConfig()
	return installWindowsQEMUVMWithConfig(rc, hc, os.Stderr)
}

func installWindowsQEMUVMWithConfig(rc vmrun.RunConfig, hc vmrun.HostConfig, quotaWarnings io.Writer) error {
	if quotaWarnings == nil {
		quotaWarnings = io.Discard
	}
	fmt.Println("=== Windows VM Installer (QEMU/HVF experimental) ===")

	if err := os.MkdirAll(hc.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	lock, err := AcquireRunLock(hc.VMDir)
	if err != nil {
		return fmt.Errorf("cove install -windows -windows-backend qemu: %w", err)
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			fmt.Fprintf(os.Stderr, "warning: release run.lock: %v\n", releaseErr)
		}
	}()
	noteVMRuntimePhase(hc.VMDir, "starting", "qemu-install-prepare")

	saveHardwareConfig(hc.VMDir)
	persistInstallQuota(quotaWarnings, hc.VMDir)
	if err := applyInstallDiskQuota(quotaWarnings, hc.VMDir); err != nil {
		return err
	}

	windowsISO, err := ensureWindowsISO()
	if err != nil {
		return err
	}
	rc.ResolveISO(windowsISO)
	fmt.Printf("Using Windows ISO: %s\n", windowsISO)

	cfg, err := windowsQEMUConfigFromRun(rc, hc, true)
	if err != nil {
		return err
	}
	if err := ensureWindowsQEMUDisk(cfg); err != nil {
		return err
	}
	if err := ensureWindowsQEMUEFIVars(cfg.EFIVarsPath, cfg.EFIVarsTemplatePath); err != nil {
		return err
	}
	agentPath, err := ensureWindowsQEMUAgentExecutable(hc.VMDir)
	if err != nil {
		return fmt.Errorf("build windows vz-agent: %w", err)
	}
	provConfig, err := windowsQEMUProvisionConfigFromFlags(agentPath, cfg.AgentGuestPort)
	if err != nil {
		return err
	}
	autounattendISO, err := winsetup.CreateAutounattendISO(hc.VMDir, provConfig)
	if err != nil {
		return fmt.Errorf("create windows autounattend ISO: %w", err)
	}
	virtioISO, err := winsetup.EnsureVirtIODriversISO("")
	if err != nil {
		return fmt.Errorf("ensure windows virtio drivers ISO: %w", err)
	}
	cfg.AutounattendISOPath = autounattendISO
	cfg.VirtioISOPath = virtioISO
	cfg.AgentExecutablePath = agentPath

	fmt.Printf("Configuring QEMU: %d CPUs, %d GB RAM\n", cfg.CPUCount, cfg.MemoryGB)
	fmt.Printf("QEMU disk: %s\n", cfg.DiskPath)
	fmt.Printf("QEMU EFI vars: %s\n", cfg.EFIVarsPath)
	fmt.Printf("Windows autounattend ISO: %s\n", cfg.AutounattendISOPath)
	fmt.Printf("Windows VirtIO drivers ISO: %s\n", cfg.VirtioISOPath)
	fmt.Printf("Windows vz-agent: %s\n", cfg.AgentExecutablePath)
	if cfg.AgentHostPort != 0 {
		fmt.Printf("Windows vz-agent host endpoint: %s:%d -> guest :%d\n", cfg.AgentHostAddress, cfg.AgentHostPort, cfg.AgentGuestPort)
	}
	return runWindowsQEMU(cfg, true)
}

func runWindowsQEMUVM() error {
	rc := vmrunRunConfig(vmrun.GuestWindows)
	hc := vmrunHostConfig()
	return runWindowsQEMUVMWithConfig(rc, hc)
}

func runWindowsQEMUVMWithConfig(rc vmrun.RunConfig, hc vmrun.HostConfig) error {
	fmt.Println("=== Windows VM Runner (QEMU/HVF experimental) ===")

	if err := os.MkdirAll(hc.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	saveHardwareConfig(hc.VMDir)

	cfg, err := windowsQEMUConfigFromRun(rc, hc, false)
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.DiskPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("windows QEMU disk image not found: %s\nrun 'cove install -windows -windows-backend qemu -iso <path>' first", cfg.DiskPath)
		}
		return fmt.Errorf("stat Windows QEMU disk image: %w", err)
	}
	if err := ensureWindowsQEMUEFIVars(cfg.EFIVarsPath, cfg.EFIVarsTemplatePath); err != nil {
		return err
	}

	fmt.Printf("Configuring QEMU: %d CPUs, %d GB RAM\n", cfg.CPUCount, cfg.MemoryGB)
	fmt.Printf("QEMU disk: %s\n", cfg.DiskPath)
	fmt.Printf("QEMU EFI vars: %s\n", cfg.EFIVarsPath)
	return runWindowsQEMU(cfg, false)
}

func windowsQEMUConfigFromRun(rc vmrun.RunConfig, hc vmrun.HostConfig, install bool) (windowsQEMUConfig, error) {
	qemuPath, err := findQEMUTool("COVE_QEMU_SYSTEM_AARCH64", "qemu-system-aarch64")
	if err != nil {
		return windowsQEMUConfig{}, err
	}
	qemuImgPath, err := findQEMUTool("COVE_QEMU_IMG", "qemu-img")
	if err != nil {
		return windowsQEMUConfig{}, err
	}
	efiCodePath, err := findQEMUFile("COVE_QEMU_EFI_CODE", []string{
		"edk2-aarch64-code.fd",
		"QEMU_EFI.fd",
	})
	if err != nil {
		return windowsQEMUConfig{}, err
	}
	efiVarsTemplatePath, err := findQEMUFile("COVE_QEMU_EFI_VARS_TEMPLATE", []string{
		"edk2-arm-vars.fd",
		"QEMU_VARS.fd",
	})
	if err != nil {
		return windowsQEMUConfig{}, err
	}

	qemuDir := filepath.Join(hc.VMDir, "qemu")
	diskPath := rc.DiskPath
	if diskPath == "" {
		diskPath = filepath.Join(hc.VMDir, "windows.qcow2")
	}
	if !filepath.IsAbs(diskPath) {
		diskPath = filepath.Join(hc.VMDir, diskPath)
	}

	iso := ""
	if install || rc.ISOPath != "" {
		iso = rc.ISOPath
	}
	serialLogPath := filepath.Join(qemuDir, "serial.log")
	serialOutput := rc.SerialOutput
	if install && !flagWasProvided(flag.CommandLine, "serial") {
		serialOutput = serialLogPath
	}
	cfg := windowsQEMUConfig{
		QEMUPath:            qemuPath,
		QEMUImgPath:         qemuImgPath,
		EFICodePath:         efiCodePath,
		EFIVarsTemplatePath: efiVarsTemplatePath,
		VMDir:               hc.VMDir,
		DiskPath:            diskPath,
		DiskFormat:          windowsQEMUDiskFormat(diskPath),
		DiskSizeGB:          rc.DiskSizeGB,
		EFIVarsPath:         filepath.Join(qemuDir, "efi_vars.fd"),
		ISOPath:             iso,
		CPUCount:            rc.CPUCount,
		MemoryGB:            rc.MemoryGB,
		NetworkMode:         rc.NetworkMode,
		Headless:            rc.Headless,
		DisplayDevice:       windowsQEMUDisplayDeviceFromEnv(),
		Nodefaults:          windowsQEMUNodefaultsFromEnv(),
		SerialOutput:        serialOutput,
		SerialLogPath:       serialLogPath,
		MonitorSockPath:     filepath.Join(qemuDir, "monitor.sock"),
	}
	if agentForward, err := windowsQEMUAgentForwardConfig(rc.NetworkMode); err != nil {
		return windowsQEMUConfig{}, err
	} else {
		cfg.AgentHostAddress = agentForward.hostAddress
		cfg.AgentHostPort = agentForward.hostPort
		cfg.AgentGuestPort = agentForward.guestPort
	}
	return cfg, os.MkdirAll(qemuDir, 0755)
}

func windowsQEMUProvisionConfigFromFlags(agentPath string, agentPort int) (winsetup.ProvisionConfig, error) {
	cfg := winsetup.DefaultProvisionConfig()
	if provisionUser != "" {
		cfg.Username = provisionUser
		if provisionPassword == "" {
			pw, err := readPassword("Windows user password: ")
			if err != nil {
				return cfg, fmt.Errorf("read windows user password: %w", err)
			}
			provisionPassword = string(pw)
			if provisionPassword == "" {
				return cfg, fmt.Errorf("missing -provision-password")
			}
		}
		cfg.Password = provisionPassword
	}
	cfg.LocalAdmin = provisionAdmin
	cfg.AgentExecutable = agentPath
	cfg.AgentTCPPort = agentPort
	return cfg, nil
}

func ensureWindowsQEMUAgentExecutable(vmDir string) (string, error) {
	path := filepath.Join(vmDir, "qemu", "vz-agent-windows-arm64.exe")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create windows agent directory: %w", err)
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", path, "./cmd/vz-agent")
	cmd.Env = append(os.Environ(),
		"GOOS=windows",
		"GOARCH=arm64",
		"CGO_ENABLED=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build ./cmd/vz-agent: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return path, nil
}

func findQEMUTool(envName, tool string) (string, error) {
	if path := strings.TrimSpace(os.Getenv(envName)); path != "" {
		if err := executableFile(path); err != nil {
			return "", fmt.Errorf("%s: %w", envName, err)
		}
		return path, nil
	}
	if path, err := exec.LookPath(tool); err == nil {
		return path, nil
	}
	for _, dir := range []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		filepath.Join(os.Getenv("HOME"), ".local/homebrew/bin"),
	} {
		path := filepath.Join(dir, tool)
		if err := executableFile(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found; install QEMU or set %s", tool, envName)
}

func executableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

func findQEMUFile(envName string, names []string) (string, error) {
	if path := strings.TrimSpace(os.Getenv(envName)); path != "" {
		if err := regularFile(path); err != nil {
			return "", fmt.Errorf("%s: %w", envName, err)
		}
		return path, nil
	}
	for _, dir := range []string{
		"/Applications/UTM.app/Contents/Resources/qemu",
		"/opt/homebrew/share/qemu",
		"/usr/local/share/qemu",
		filepath.Join(os.Getenv("HOME"), ".local/homebrew/share/qemu"),
	} {
		for _, name := range names {
			path := filepath.Join(dir, name)
			if err := regularFile(path); err == nil {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("%s not found; install UTM/QEMU firmware or set %s", strings.Join(names, " or "), envName)
}

func regularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}

func windowsQEMUDiskFormat(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".qcow2") {
		return "qcow2"
	}
	return "raw"
}

func ensureWindowsQEMUDisk(cfg windowsQEMUConfig) error {
	if _, err := os.Stat(cfg.DiskPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat Windows QEMU disk image: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DiskPath), 0755); err != nil {
		return fmt.Errorf("create Windows QEMU disk directory: %w", err)
	}
	if cfg.DiskFormat != "qcow2" {
		fmt.Printf("Creating raw QEMU disk image: %s (%d GB)\n", cfg.DiskPath, cfg.DiskSizeGB)
		return createInstallDiskImage(cfg.DiskPath, cfg.DiskSizeGB)
	}
	fmt.Printf("Creating QEMU disk image: %s (%d GB)\n", cfg.DiskPath, cfg.DiskSizeGB)
	cmd := exec.Command(cfg.QEMUImgPath, "create", "-f", "qcow2", cfg.DiskPath, fmt.Sprintf("%dG", cfg.DiskSizeGB))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create QEMU disk image: %w", err)
	}
	return nil
}

func ensureWindowsQEMUEFIVars(varsPath, templatePath string) error {
	if info, err := os.Stat(varsPath); err == nil && info.Size() > 0 {
		return nil
	} else if err == nil {
		if err := os.Remove(varsPath); err != nil {
			return fmt.Errorf("remove empty QEMU EFI vars: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat QEMU EFI vars: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(varsPath), 0755); err != nil {
		return fmt.Errorf("create QEMU EFI vars directory: %w", err)
	}

	in, err := os.Open(templatePath)
	if err != nil {
		return fmt.Errorf("open QEMU EFI vars template: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(varsPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create QEMU EFI vars: %w", err)
	}
	removeOnError := true
	defer func() {
		out.Close()
		if removeOnError {
			os.Remove(varsPath)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy QEMU EFI vars template: %w", err)
	}
	const varsSize = 64 * 1024 * 1024
	if err := out.Truncate(varsSize); err != nil {
		return fmt.Errorf("pad QEMU EFI vars: %w", err)
	}
	removeOnError = false
	return nil
}

func runWindowsQEMU(cfg windowsQEMUConfig, install bool) error {
	noteVMRuntimePhase(cfg.VMDir, "starting", "qemu-start")
	args, err := windowsQEMUArgs(cfg)
	if err != nil {
		noteVMRuntimePhase(cfg.VMDir, "error", "qemu-args")
		return err
	}
	if err := removeWindowsQEMUMonitorSocket(cfg.MonitorSockPath); err != nil {
		noteVMRuntimePhase(cfg.VMDir, "error", "qemu-monitor-socket")
		return err
	}
	commandPath := filepath.Join(filepath.Dir(cfg.MonitorSockPath), "launch.sh")
	if err := writeWindowsQEMUCommand(commandPath, cfg.QEMUPath, args); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write QEMU launch script: %v\n", err)
	} else {
		fmt.Printf("QEMU launch script: %s\n", commandPath)
	}
	metadataPath := filepath.Join(filepath.Dir(cfg.MonitorSockPath), "metadata.json")
	if err := writeWindowsQEMUMetadata(metadataPath, cfg, args); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write QEMU metadata: %v\n", err)
	}
	readmePath := filepath.Join(filepath.Dir(cfg.MonitorSockPath), "README")
	if err := writeWindowsQEMUReadme(readmePath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write QEMU README: %v\n", err)
	}
	fmt.Printf("Launching QEMU: %s\n", cfg.QEMUPath)
	if cfg.SerialOutput != "" && cfg.SerialOutput != "none" && cfg.SerialOutput != "stdout" {
		fmt.Printf("QEMU serial log: %s\n", cfg.SerialOutput)
	}

	cmd := exec.Command(cfg.QEMUPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		noteVMRuntimePhase(cfg.VMDir, "error", "qemu-start")
		return fmt.Errorf("start QEMU: %w", err)
	}
	processPath := windowsQEMUProcessPath(cfg)
	process := windowsQEMUProcessMetadata{
		State:           "running",
		CovePID:         os.Getpid(),
		QEMUPID:         cmd.Process.Pid,
		StartedAt:       time.Now().UTC(),
		MonitorSockPath: cfg.MonitorSockPath,
	}
	if err := writeWindowsQEMUProcessMetadata(processPath, process); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write QEMU process metadata: %v\n", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := waitWindowsQEMUMonitor(cfg.MonitorSockPath, done, 5*time.Second); err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			<-done
		}
		process.State = "error"
		process.ExitError = err.Error()
		exitedAt := time.Now().UTC()
		process.ExitedAt = &exitedAt
		_ = writeWindowsQEMUProcessMetadata(processPath, process)
		noteVMRuntimePhase(cfg.VMDir, "error", "qemu-monitor")
		return err
	}
	noteVMRuntimePhase(cfg.VMDir, "running", "qemu-monitor-ready")
	fmt.Printf("QEMU monitor: %s\n", cfg.MonitorSockPath)
	if install && windowsQEMUAutoBootKey() {
		go windowsQEMUSendBootKeys(cfg.MonitorSockPath, windowsQEMUBootKeyConfigFromEnv())
	}
	waitErr := <-done
	exitedAt := time.Now().UTC()
	process.ExitedAt = &exitedAt
	if waitErr != nil {
		process.State = "error"
		process.ExitError = waitErr.Error()
		_ = writeWindowsQEMUProcessMetadata(processPath, process)
		noteVMRuntimePhase(cfg.VMDir, "error", "qemu-exited")
		return fmt.Errorf("QEMU exited: %w", waitErr)
	}
	process.State = "stopped"
	_ = writeWindowsQEMUProcessMetadata(processPath, process)
	noteVMRuntimeState(cfg.VMDir, "stopped")
	return nil
}

func removeWindowsQEMUMonitorSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat stale QEMU monitor socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to remove non-socket QEMU monitor path: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale QEMU monitor socket: %w", err)
	}
	return nil
}

func waitWindowsQEMUMonitor(sock string, done <-chan error, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("QEMU exited before monitor was ready: %w", err)
			}
			return fmt.Errorf("QEMU exited before monitor was ready")
		case <-deadline.C:
			return fmt.Errorf("QEMU monitor did not become ready: %s", sock)
		case <-tick.C:
			conn, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

func windowsQEMUAutoBootKey() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_AUTO_BOOT_KEY"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

type windowsQEMUBootKeyConfig struct {
	Delay    time.Duration
	Count    int
	Interval time.Duration
}

func windowsQEMUBootKeyConfigFromEnv() windowsQEMUBootKeyConfig {
	return windowsQEMUBootKeyConfig{
		Delay:    windowsQEMUEnvDuration("COVE_QEMU_BOOT_KEY_DELAY", 6*time.Second),
		Count:    windowsQEMUEnvInt("COVE_QEMU_BOOT_KEY_COUNT", 4),
		Interval: windowsQEMUEnvDuration("COVE_QEMU_BOOT_KEY_INTERVAL", 4*time.Second),
	}
}

func windowsQEMUEnvInt(name string, def int) int {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func windowsQEMUEnvDuration(name string, def time.Duration) time.Duration {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return d
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return def
}

func windowsQEMUDisplayDeviceFromEnv() string {
	s := strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_DISPLAY_DEVICE")))
	if s == "" {
		return "ramfb+virtio-gpu-pci"
	}
	return strings.ReplaceAll(s, ",", "+")
}

func windowsQEMUNodefaultsFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_NODEFAULTS"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func windowsQEMUSendBootKeys(sock string, cfg windowsQEMUBootKeyConfig) {
	time.Sleep(cfg.Delay)
	for i := 0; i < cfg.Count; i++ {
		if err := windowsQEMUSendMonitorCommand(sock, "sendkey spc"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: send qemu boot key: %v\n", err)
		}
		if i+1 < cfg.Count {
			time.Sleep(cfg.Interval)
		}
	}
}

func windowsQEMUSendMonitorCommand(sock, command string) error {
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial qemu monitor: %w", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, command); err != nil {
		return err
	}
	return nil
}

func windowsQEMUArgs(cfg windowsQEMUConfig) ([]string, error) {
	if cfg.ISOPath != "" {
		if _, err := os.Stat(cfg.ISOPath); err != nil {
			return nil, fmt.Errorf("stat Windows ISO: %w", err)
		}
	}
	displayArgs, err := windowsQEMUDisplayDeviceArgs(cfg.DisplayDevice)
	if err != nil {
		return nil, err
	}

	args := []string{
		"-machine", windowsQEMUMachineArg(cfg.MemoryGB),
		"-cpu", "host",
		"-smp", strconv.Itoa(int(cfg.CPUCount)),
		"-m", fmt.Sprintf("%dG", cfg.MemoryGB),
	}
	if cfg.Nodefaults {
		args = append(args, "-nodefaults", "-vga", "none")
	}
	args = append(args,
		"-drive", "if=pflash,format=raw,readonly=on,unit=0,file.locking=off,file="+cfg.EFICodePath,
		"-drive", "if=pflash,format=raw,unit=1,file="+cfg.EFIVarsPath,
	)
	args = append(args, displayArgs...)
	args = append(args,
		"-device", "qemu-xhci,id=xhci",
		"-device", "usb-kbd,bus=xhci.0",
		"-device", "usb-tablet,bus=xhci.0",
		"-object", "rng-random,id=rng0,filename=/dev/urandom",
		"-device", "virtio-rng-pci,rng=rng0",
		"-drive", fmt.Sprintf("if=none,id=hd0,format=%s,file=%s", cfg.DiskFormat, cfg.DiskPath),
		"-device", "nvme,drive=hd0,serial=covewindows001,bootindex=2",
	)
	if cfg.ISOPath != "" {
		args = append(args,
			"-drive", "if=none,id=cd0,format=raw,media=cdrom,readonly=on,file="+cfg.ISOPath,
			"-device", "usb-storage,drive=cd0,bootindex=1",
		)
	}
	if cfg.VirtioISOPath != "" {
		if _, err := os.Stat(cfg.VirtioISOPath); err != nil {
			return nil, fmt.Errorf("stat Windows VirtIO ISO: %w", err)
		}
		args = append(args,
			"-drive", "if=none,id=virtio0,format=raw,media=cdrom,readonly=on,file="+cfg.VirtioISOPath,
			"-device", "usb-storage,drive=virtio0",
		)
	}
	if cfg.AutounattendISOPath != "" {
		if _, err := os.Stat(cfg.AutounattendISOPath); err != nil {
			return nil, fmt.Errorf("stat Windows autounattend ISO: %w", err)
		}
		args = append(args,
			"-drive", "if=none,id=oemdrv0,format=raw,media=cdrom,readonly=on,file="+cfg.AutounattendISOPath,
			"-device", "usb-storage,drive=oemdrv0",
		)
	}
	netArgs, err := windowsQEMUNetworkArgs(cfg)
	if err != nil {
		return nil, err
	}
	args = append(args, netArgs...)
	args = append(args, windowsQEMUSerialArgs(cfg)...)
	args = append(args,
		"-monitor", "unix:"+cfg.MonitorSockPath+",server=on,wait=off",
	)
	if cfg.Headless {
		args = append(args, "-display", "none")
	} else {
		args = append(args, "-display", "cocoa")
	}
	return args, nil
}

func windowsQEMUDisplayDeviceArgs(device string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(device)) {
	case "", "ramfb":
		return []string{"-device", "ramfb"}, nil
	case "virtio-gpu", "virtio-gpu-pci":
		return []string{"-device", "virtio-gpu-pci,xres=1280,yres=800"}, nil
	case "ramfb+virtio-gpu", "ramfb+virtio-gpu-pci", "virtio-gpu+ramfb", "virtio-gpu-pci+ramfb":
		return []string{
			"-device", "ramfb",
			"-device", "virtio-gpu-pci,xres=1280,yres=800",
		}, nil
	case "bochs", "bochs-display":
		return []string{"-device", "bochs-display"}, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("invalid COVE_QEMU_DISPLAY_DEVICE %q (must be ramfb, virtio-gpu-pci, ramfb+virtio-gpu-pci, bochs-display, or none)", device)
	}
}

func windowsQEMUMachineArg(memoryGB uint64) string {
	if memoryGB > 3 {
		return "virt,accel=hvf"
	}
	return "virt,accel=hvf,highmem=off"
}

type windowsQEMUAgentForward struct {
	hostAddress string
	hostPort    int
	guestPort   int
}

func windowsQEMUAgentForwardConfig(networkMode string) (windowsQEMUAgentForward, error) {
	if strings.EqualFold(strings.TrimSpace(networkMode), "none") {
		return windowsQEMUAgentForward{}, nil
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_AGENT_FORWARD"))) {
	case "0", "false", "no", "off":
		return windowsQEMUAgentForward{}, nil
	}
	guestPort := windowsQEMUEnvInt("COVE_QEMU_AGENT_GUEST_PORT", 1024)
	hostPort := windowsQEMUEnvInt("COVE_QEMU_AGENT_HOST_PORT", 0)
	var err error
	if hostPort == 0 {
		hostPort, err = pickFreeLocalTCPPort()
		if err != nil {
			return windowsQEMUAgentForward{}, err
		}
	}
	return windowsQEMUAgentForward{
		hostAddress: "127.0.0.1",
		hostPort:    hostPort,
		guestPort:   guestPort,
	}, nil
}

func pickFreeLocalTCPPort() (int, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate qemu agent host port: %w", err)
	}
	defer lis.Close()
	addr, ok := lis.Addr().(*net.TCPAddr)
	if !ok || addr.Port == 0 {
		return 0, fmt.Errorf("allocate qemu agent host port: unexpected address %s", lis.Addr())
	}
	return addr.Port, nil
}

func windowsQEMUNetworkArgs(cfg windowsQEMUConfig) ([]string, error) {
	switch strings.TrimSpace(strings.ToLower(cfg.NetworkMode)) {
	case "", "nat":
		netdev := "user,id=net0"
		if cfg.AgentHostPort != 0 && cfg.AgentGuestPort != 0 {
			netdev += fmt.Sprintf(",hostfwd=tcp:%s:%d-:%d", cfg.AgentHostAddress, cfg.AgentHostPort, cfg.AgentGuestPort)
		}
		return []string{
			"-netdev", netdev,
			"-device", "virtio-net-pci,netdev=net0",
		}, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("-windows-backend qemu supports -network nat or none, not %q", cfg.NetworkMode)
	}
}

func windowsQEMUSerialArgs(cfg windowsQEMUConfig) []string {
	switch strings.TrimSpace(cfg.SerialOutput) {
	case "", "stdout":
		return []string{"-serial", "stdio"}
	case "none":
		return []string{"-serial", "none"}
	default:
		return []string{"-serial", "file:" + cfg.SerialOutput}
	}
}

type windowsQEMUMetadata struct {
	Backend             string   `json:"backend"`
	QEMUPath            string   `json:"qemuPath"`
	QEMUVersion         string   `json:"qemuVersion,omitempty"`
	EFICodePath         string   `json:"efiCodePath"`
	EFIVarsTemplatePath string   `json:"efiVarsTemplatePath"`
	EFIVarsPath         string   `json:"efiVarsPath"`
	DiskPath            string   `json:"diskPath"`
	DiskFormat          string   `json:"diskFormat"`
	ISOPath             string   `json:"isoPath,omitempty"`
	Display             string   `json:"display"`
	SerialOutput        string   `json:"serialOutput"`
	MonitorSockPath     string   `json:"monitorSockPath"`
	AutounattendISOPath string   `json:"autounattendIsoPath,omitempty"`
	VirtioISOPath       string   `json:"virtioIsoPath,omitempty"`
	AgentExecutablePath string   `json:"agentExecutablePath,omitempty"`
	AgentHostAddress    string   `json:"agentHostAddress,omitempty"`
	AgentHostPort       int      `json:"agentHostPort,omitempty"`
	AgentGuestPort      int      `json:"agentGuestPort,omitempty"`
	Args                []string `json:"args"`
}

type windowsQEMUProcessMetadata struct {
	State           string     `json:"state"`
	CovePID         int        `json:"covePid"`
	QEMUPID         int        `json:"qemuPid"`
	StartedAt       time.Time  `json:"startedAt"`
	ExitedAt        *time.Time `json:"exitedAt,omitempty"`
	ExitError       string     `json:"exitError,omitempty"`
	MonitorSockPath string     `json:"monitorSockPath"`
}

func windowsQEMUProcessPath(cfg windowsQEMUConfig) string {
	return filepath.Join(filepath.Dir(cfg.MonitorSockPath), "process.json")
}

func writeWindowsQEMUProcessMetadata(path string, metadata windowsQEMUProcessMetadata) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func writeWindowsQEMUMetadata(path string, cfg windowsQEMUConfig, args []string) error {
	metadata := windowsQEMUMetadata{
		Backend:             "qemu-hvf",
		QEMUPath:            cfg.QEMUPath,
		QEMUVersion:         windowsQEMUVersion(cfg.QEMUPath),
		EFICodePath:         cfg.EFICodePath,
		EFIVarsTemplatePath: cfg.EFIVarsTemplatePath,
		EFIVarsPath:         cfg.EFIVarsPath,
		DiskPath:            cfg.DiskPath,
		DiskFormat:          cfg.DiskFormat,
		ISOPath:             cfg.ISOPath,
		Display:             cfg.DisplayDevice,
		SerialOutput:        cfg.SerialOutput,
		MonitorSockPath:     cfg.MonitorSockPath,
		AutounattendISOPath: cfg.AutounattendISOPath,
		VirtioISOPath:       cfg.VirtioISOPath,
		AgentExecutablePath: cfg.AgentExecutablePath,
		AgentHostAddress:    cfg.AgentHostAddress,
		AgentHostPort:       cfg.AgentHostPort,
		AgentGuestPort:      cfg.AgentGuestPort,
		Args:                append([]string(nil), args...),
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func windowsQEMUVersion(qemuPath string) string {
	out, err := exec.Command(qemuPath, "--version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

func windowsQEMUDisplay(cfg windowsQEMUConfig) string {
	if cfg.Headless {
		return "none"
	}
	return "cocoa"
}

func writeWindowsQEMUReadme(path string, cfg windowsQEMUConfig) error {
	text := fmt.Sprintf(`cove Windows QEMU/HVF backend artifacts

This is a direct QEMU/HVF backend. UTM is used only as a firmware asset source
when firmware is found under /Applications/UTM.app/Contents/Resources/qemu.

Monitor:
  nc -U %s

Useful monitor commands:
  info status
  screendump /tmp/cove-windows-qemu-screen.ppm
  sendkey spc

Artifacts:
  metadata: %s
  process metadata: %s
  launch script: %s
  display device: %s
  serial: %s
  EFI vars: %s
  disk: %s
  autounattend ISO: %s
  VirtIO drivers ISO: %s
  Windows vz-agent: %s
  Windows vz-agent host endpoint: %s

Display experiment:
  COVE_QEMU_DISPLAY_DEVICE=ramfb+virtio-gpu-pci
  COVE_QEMU_DISPLAY_DEVICE=virtio-gpu-pci
  COVE_QEMU_DISPLAY_DEVICE=ramfb
`, cfg.MonitorSockPath,
		filepath.Join(filepath.Dir(cfg.MonitorSockPath), "metadata.json"),
		windowsQEMUProcessPath(cfg),
		filepath.Join(filepath.Dir(cfg.MonitorSockPath), "launch.sh"),
		cfg.DisplayDevice,
		cfg.SerialOutput,
		cfg.EFIVarsPath,
		cfg.DiskPath,
		cfg.AutounattendISOPath,
		cfg.VirtioISOPath,
		cfg.AgentExecutablePath,
		windowsQEMUAgentEndpoint(cfg),
	)
	return os.WriteFile(path, []byte(text), 0644)
}

func windowsQEMUAgentEndpoint(cfg windowsQEMUConfig) string {
	if cfg.AgentHostAddress == "" || cfg.AgentHostPort == 0 {
		return ""
	}
	return net.JoinHostPort(cfg.AgentHostAddress, strconv.Itoa(cfg.AgentHostPort))
}

func writeWindowsQEMUCommand(path, qemuPath string, args []string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "#!/bin/sh")
	fmt.Fprintln(w, "set -eu")
	fmt.Fprintf(w, "exec %s", shellQuote(qemuPath))
	for _, arg := range args {
		fmt.Fprintf(w, " %s", shellQuote(arg))
	}
	fmt.Fprintln(w)
	if err := w.Flush(); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}
