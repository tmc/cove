package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/rfb"
	"github.com/tmc/cove/internal/vmconfig"
	winsetup "github.com/tmc/cove/internal/windows"
)

var errDoctorQEMUFailed = errors.New("qemu readiness failed")

// qemuDoctorCheck is one QEMU readiness check. Status is "pass", "fail",
// "warn", or "info". An "info" check reports something cove treats as
// optional, so it never degrades the report status.
type qemuDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func verifyWindowsQEMUVM(target vmSelection) error {
	status := readWindowsQEMUCTLStatus(target.Directory)
	fmt.Println("=== Verifying Windows QEMU VM ===")
	fmt.Printf("VM: %s\n", target.Directory)
	fmt.Printf("State: %s\n", status.State)
	fmt.Printf("Backend: %s\n\n", status.Backend)

	allOK := true
	check := func(ok bool, name, message string) {
		state := "OK"
		if !ok {
			state = "FAIL"
			allOK = false
		}
		fmt.Printf("  %s: %s (%s)\n", name, state, message)
	}
	warn := func(name, message string) {
		fmt.Printf("  %s: WARN (%s)\n", name, message)
	}

	check(status.State == "running", "QEMU process", status.State)
	check(windowsQEMUMonitorReachable(status.MonitorSockPath), "QEMU monitor", status.MonitorSockPath)
	if status.VNCURL == "" {
		warn("QEMU console", "VNC is not configured; restart with -vnc :5901 for gui open")
	} else {
		check(true, "QEMU console", status.VNCURL)
		check(windowsQEMURFBReachable(status.VNCEndpoint), "QEMU RFB", status.VNCEndpoint)
	}
	if status.GuestUsername == "" || status.GuestPassword == "" {
		warn("Windows credentials", "guest username/password not recorded in qemu metadata")
	} else {
		check(true, "Windows credentials", status.GuestUsername)
	}
	check(strings.HasPrefix(status.AgentHealth, "connected"), "daemon agent", status.AgentHealth)
	if status.UserAgentEndpoint == "" {
		warn("user agent", "no user-agent endpoint recorded")
	} else {
		check(strings.HasPrefix(status.UserAgentHealth, "connected"), "user agent", status.UserAgentHealth)
	}
	if strings.HasPrefix(status.AgentHealth, "connected") {
		ok, msg := windowsQEMUFirewallProfilesDisabled(status.AgentEndpoint)
		if ok {
			check(true, "Windows firewall", msg)
		} else {
			warn("Windows firewall", msg)
		}
	}
	if !allOK {
		return errDoctorQEMUFailed
	}
	return nil
}

func windowsQEMUFirewallProfilesDisabled(address string) (bool, string) {
	stdout, stderr, exitCode, err := qemuAgentExecStream(
		vzscriptConfig{qemuAgentAddress: address},
		[]string{"powershell.exe", "-NoProfile", "-Command", `Get-NetFirewallProfile | Select-Object Name,Enabled | ConvertTo-Json -Compress`},
		10*time.Second,
		nil,
		nil,
	)
	if err != nil {
		return false, err.Error()
	}
	if exitCode != 0 {
		return false, strings.TrimSpace(stderr)
	}
	text := strings.TrimSpace(stdout)
	if text == "" {
		return false, "firewall profile query returned no output"
	}
	if strings.Contains(text, `"Enabled":true`) || strings.Contains(text, `"Enabled":1`) {
		return false, text
	}
	return true, text
}

type qemuDoctorReport struct {
	OK     bool              `json:"ok"`
	Status string            `json:"status"`
	Checks []qemuDoctorCheck `json:"checks"`
}

func handleDoctorQEMU(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("doctor qemu", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printDoctorQEMUUsage(fs.Output()) }
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove doctor qemu [-json]")
	}
	report := collectQEMUDoctorReport()
	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else if err := writeQEMUDoctorReport(w, report); err != nil {
		return err
	}
	if !report.OK {
		return errDoctorQEMUFailed
	}
	return nil
}

func printDoctorQEMUUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove doctor qemu [-json]

Check whether this Mac has the direct QEMU/HVF Windows backend prerequisites.

Checks:
  host             macOS on Apple Silicon for hvf
  qemu-system      qemu-system-aarch64 executable
  qemu-version     qemu-system-aarch64 is a version cove is tested against
  qemu-img         qemu-img executable
  efi-code         AArch64 EFI pflash code image
  efi-vars         writable pflash vars template
  display          COVE_QEMU_DISPLAY_DEVICE value
  screenshot-backend COVE_QEMU_SCREENSHOT_BACKEND value
  text-backend    COVE_QEMU_TEXT_BACKEND value
  qemu-vdagent     QEMU SPICE vdagent chardev for clipboard transport
  aqua-session     console session for the Cove display window
  virtio-drivers   cached ARM64 VirtIO driver ISO, if present

Flags:
  -json            emit machine-readable JSON`)
}

func collectQEMUDoctorReport() qemuDoctorReport {
	checks := []qemuDoctorCheck{
		qemuDoctorHostCheck(),
		qemuDoctorToolCheck("qemu-system-aarch64", "COVE_QEMU_SYSTEM_AARCH64", "qemu-system-aarch64"),
		qemuDoctorVersionCheck(),
		qemuDoctorToolCheck("qemu-img", "COVE_QEMU_IMG", "qemu-img"),
		qemuDoctorFileCheck("efi-code", "COVE_QEMU_EFI_CODE", []string{
			"edk2-aarch64-code.fd",
			"QEMU_EFI.fd",
		}),
		qemuDoctorFileCheck("efi-vars-template", "COVE_QEMU_EFI_VARS_TEMPLATE", []string{
			"edk2-arm-vars.fd",
			"QEMU_VARS.fd",
		}),
		qemuDoctorDisplayCheck(),
		qemuDoctorBackendEnvCheck("screenshot-backend", "COVE_QEMU_SCREENSHOT_BACKEND", []string{"auto", "rfb", "vnc", "monitor", "screendump"}),
		qemuDoctorBackendEnvCheck("text-backend", "COVE_QEMU_TEXT_BACKEND", []string{"auto", "rfb", "vnc", "monitor", "sendkey"}),
		qemuDoctorVDAgentCheck(),
		qemuDoctorAquaSessionCheck(),
		qemuDoctorVirtIODriversCheck(),
	}

	status := "pass"
	ok := true
	for _, check := range checks {
		switch check.Status {
		case "fail":
			status = "fail"
			ok = false
		case "warn":
			if status == "pass" {
				status = "warn"
			}
		}
	}
	return qemuDoctorReport{OK: ok, Status: status, Checks: checks}
}

func windowsQEMURFBReachable(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := rfb.Dial(ctx, endpoint)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func writeQEMUDoctorReport(w io.Writer, report qemuDoctorReport) error {
	fmt.Fprintf(w, "QEMU Windows readiness: %s\n", report.Status)
	for _, check := range report.Checks {
		fmt.Fprintf(w, "  %s  %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message)
	}
	if !report.OK {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Resolve failed checks before installing Windows with `-windows-backend qemu`.")
	}
	return nil
}

func qemuDoctorHostCheck() qemuDoctorCheck {
	if runtime.GOOS != "darwin" {
		return qemuDoctorCheck{"host", "fail", "QEMU/HVF Windows backend requires macOS"}
	}
	if runtime.GOARCH != "arm64" {
		return qemuDoctorCheck{"host", "fail", "QEMU/HVF Windows backend requires Apple Silicon"}
	}
	return qemuDoctorCheck{"host", "pass", "running on darwin/arm64"}
}

func qemuDoctorToolCheck(name, envName, tool string) qemuDoctorCheck {
	path, err := findQEMUTool(envName, tool)
	if err != nil {
		return qemuDoctorCheck{name, "fail", err.Error() + qemuDoctorInstallHint("qemu")}
	}
	msg := path
	if name == "qemu-system-aarch64" {
		if version := windowsQEMUVersion(path); version != "" {
			msg = fmt.Sprintf("%s (%s)", path, version)
		}
	}
	return qemuDoctorCheck{name, "pass", msg}
}

func qemuDoctorFileCheck(name, envName string, names []string) qemuDoctorCheck {
	path, err := findQEMUFile(envName, names)
	if err != nil {
		return qemuDoctorCheck{name, "fail", err.Error()}
	}
	info, err := os.Stat(path)
	if err != nil {
		return qemuDoctorCheck{name, "fail", fmt.Sprintf("stat %s: %v", path, err)}
	}
	if info.Size() == 0 {
		return qemuDoctorCheck{name, "fail", fmt.Sprintf("%s is empty", path)}
	}
	return qemuDoctorCheck{name, "pass", fmt.Sprintf("%s (%s)", path, bytefmt.Size(info.Size()))}
}

func qemuDoctorDisplayCheck() qemuDoctorCheck {
	device := windowsQEMUDisplayDeviceFromEnv()
	args, err := windowsQEMUDisplayDeviceArgs(device)
	if err != nil {
		return qemuDoctorCheck{"display", "fail", err.Error()}
	}
	if len(args) == 0 {
		return qemuDoctorCheck{"display", "warn", "display disabled with COVE_QEMU_DISPLAY_DEVICE=none"}
	}
	return qemuDoctorCheck{"display", "pass", device}
}

func qemuDoctorBackendEnvCheck(name, envName string, allowed []string) qemuDoctorCheck {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(envName)))
	if value == "" {
		return qemuDoctorCheck{name, "pass", envName + "=auto"}
	}
	for _, ok := range allowed {
		if value == ok {
			return qemuDoctorCheck{name, "pass", envName + "=" + value}
		}
	}
	return qemuDoctorCheck{name, "fail", fmt.Sprintf("invalid %s=%q", envName, os.Getenv(envName))}
}

func qemuDoctorVDAgentCheck() qemuDoctorCheck {
	qemuPath, err := findQEMUTool("COVE_QEMU_SYSTEM_AARCH64", "qemu-system-aarch64")
	if err != nil {
		return qemuDoctorCheck{"qemu-vdagent", "fail", err.Error() + qemuDoctorInstallHint("qemu")}
	}
	if err := windowsQEMUVDAgentSupported(qemuPath); err != nil {
		return qemuDoctorCheck{"qemu-vdagent", "fail", err.Error() + qemuDoctorInstallHint("qemu")}
	}
	return qemuDoctorCheck{"qemu-vdagent", "pass", "qemu-vdagent chardev is available"}
}

func windowsQEMUVDAgentSupported(qemuPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemuPath, "-machine", "none", "-chardev", "help")
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("check qemu-vdagent chardev: timed out")
	}
	if err != nil {
		return fmt.Errorf("check qemu-vdagent chardev: %w", err)
	}
	if !strings.Contains(string(output), "qemu-vdagent") {
		return fmt.Errorf("qemu-vdagent chardev not available; install QEMU with CONFIG_SPICE_PROTOCOL")
	}
	return nil
}

// qemuDoctorVirtIODriversCheck reports whether the ARM64 VirtIO driver ISO is
// cached. The first Windows install downloads it, so its absence is
// informational rather than a defect in the host.
func qemuDoctorVirtIODriversCheck() qemuDoctorCheck {
	cacheDir, err := winsetup.DefaultVirtIODriversCacheDir()
	if err != nil {
		return qemuDoctorCheck{"virtio-drivers", "info", fmt.Sprintf("could not find driver cache directory: %v", err)}
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "virtio-win-*.iso"))
	if err != nil {
		return qemuDoctorCheck{"virtio-drivers", "info", fmt.Sprintf("scan %s: %v", cacheDir, err)}
	}
	for _, path := range matches {
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			return qemuDoctorCheck{"virtio-drivers", "pass", fmt.Sprintf("%s (%s)", path, bytefmt.Size(info.Size()))}
		}
	}
	return qemuDoctorCheck{"virtio-drivers", "info", fmt.Sprintf("not cached under %s; first Windows install downloads ARM64 VirtIO drivers", cacheDir)}
}

// minQEMUVersion is the oldest QEMU cove's Windows backend is tested against.
// Nothing in the backend is known to require it, so an older QEMU is reported
// as untested rather than broken. The phase that adds qcow2 snapshot-save and
// snapshot-load to the QEMU path should raise this floor and grade it as a
// hard failure, since those monitor commands landed in QEMU 6.0.
var minQEMUVersion = qemuVersion{Major: 6}

// qemuVersion is a parsed QEMU major.minor.patch version.
type qemuVersion struct {
	Major int
	Minor int
	Patch int
}

func (v qemuVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// less reports whether v orders before other.
func (v qemuVersion) less(other qemuVersion) bool {
	switch {
	case v.Major != other.Major:
		return v.Major < other.Major
	case v.Minor != other.Minor:
		return v.Minor < other.Minor
	default:
		return v.Patch < other.Patch
	}
}

// parseQEMUVersion extracts the version from the first line of
// "qemu-system-aarch64 --version", such as "QEMU emulator version 9.1.0".
// A pre-release or packaging suffix ("8.2.0-rc1", "6.2.0-dirty") is dropped:
// only the numeric core is compared, so a release candidate counts as its
// release.
func parseQEMUVersion(line string) (qemuVersion, error) {
	fields := strings.Fields(line)
	number := ""
	for i, field := range fields {
		if field == "version" && i+1 < len(fields) {
			number = fields[i+1]
			break
		}
	}
	if number == "" {
		return qemuVersion{}, fmt.Errorf("no version in %q", strings.TrimSpace(line))
	}
	number, _, _ = strings.Cut(number, "-")
	parts := strings.Split(number, ".")
	if len(parts) > 3 {
		return qemuVersion{}, fmt.Errorf("malformed version %q", number)
	}
	var version qemuVersion
	into := []*int{&version.Major, &version.Minor, &version.Patch}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || strings.ContainsAny(part, "+-") {
			return qemuVersion{}, fmt.Errorf("malformed version %q", number)
		}
		*into[i] = n
	}
	return version, nil
}

// qemuDoctorInstallHint returns the remediation suffix for a check that a
// Homebrew formula can satisfy.
func qemuDoctorInstallHint(formula string) string {
	return "; install with: brew install " + formula
}

func qemuDoctorVersionCheck() qemuDoctorCheck {
	path, err := findQEMUTool("COVE_QEMU_SYSTEM_AARCH64", "qemu-system-aarch64")
	if err != nil {
		return qemuDoctorCheck{"qemu-version", "fail", err.Error() + qemuDoctorInstallHint("qemu")}
	}
	return qemuDoctorVersionStatus(windowsQEMUVersion(path))
}

// qemuDoctorVersionStatus grades the first line of "qemu-system-aarch64
// --version" against minQEMUVersion. An older QEMU only warns: no shipped
// part of the backend is known to need 6.0.
func qemuDoctorVersionStatus(line string) qemuDoctorCheck {
	version, err := parseQEMUVersion(line)
	if err != nil {
		return qemuDoctorCheck{"qemu-version", "warn", fmt.Sprintf("could not read the QEMU version (%v); cove is tested against %s or newer%s", err, minQEMUVersion, qemuDoctorInstallHint("qemu"))}
	}
	if version.less(minQEMUVersion) {
		return qemuDoctorCheck{"qemu-version", "warn", fmt.Sprintf("found QEMU %s, older than the %s cove is tested against; the Windows backend may still work%s", version, minQEMUVersion, qemuDoctorInstallHint("qemu"))}
	}
	return qemuDoctorCheck{"qemu-version", "pass", fmt.Sprintf("found QEMU %s, tested against %s or newer", version, minQEMUVersion)}
}

// qemuDoctorSessionName reports this process's launchd session type: "Aqua"
// in the console session, "Background" or "StandardIO" over ssh and from
// launchd daemons.
var qemuDoctorSessionName = func() (string, error) {
	out, err := exec.Command("launchctl", "managername").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func qemuDoctorAquaSessionCheck() qemuDoctorCheck {
	name, err := qemuDoctorSessionName()
	return qemuDoctorAquaSessionStatus(name, err)
}

// qemuDoctorAquaSessionStatus grades a launchd session name. The Cove display
// window is an AppKit window, so it can only bind WindowServer from the
// console session. A headless invocation still runs the VM and can reach it
// over VNC, so a non-Aqua session is reported informationally.
func qemuDoctorAquaSessionStatus(name string, err error) qemuDoctorCheck {
	const remedy = `run "cove gui open" from a terminal inside the logged-in desktop session, or from here run: sudo launchctl asuser "$(stat -f %u /dev/console)" cove qemu-display -vm <name>, or point a VNC client at the VM's -vnc endpoint`
	if err != nil {
		return qemuDoctorCheck{"aqua-session", "info", fmt.Sprintf("could not read the launchd session type (%v); if the display window fails to open, %s", err, remedy)}
	}
	if !strings.EqualFold(name, "Aqua") {
		if name == "" {
			name = "unknown"
		}
		return qemuDoctorCheck{"aqua-session", "info", fmt.Sprintf("launchd session is %s, not Aqua: the Cove display window cannot bind WindowServer here; %s", name, remedy)}
	}
	return qemuDoctorCheck{"aqua-session", "pass", "Aqua session; the Cove display window can bind WindowServer"}
}

// qemuDoctorSelection records why a default doctor run would include the QEMU
// Windows checks. The prerequisites belong to the QEMU backend alone: a host
// running Windows under Virtualization.framework needs none of them, so the
// selection is backend-scoped, not Windows-scoped.
type qemuDoctorSelection struct {
	// Explicit is set when the invocation itself asks for a QEMU Windows VM.
	Explicit bool
	// QEMUWindowsVMs counts the QEMU-backed Windows VMs on this host.
	QEMUWindowsVMs int
}

// include reports whether the QEMU checks belong in this doctor run.
func (s qemuDoctorSelection) include() bool {
	return s.Explicit || s.QEMUWindowsVMs > 0
}

// currentQEMUDoctorSelection describes this host and invocation.
func currentQEMUDoctorSelection() qemuDoctorSelection {
	backend, err := parseWindowsBackend(windowsBackendMode)
	return qemuDoctorSelection{
		Explicit:       windowsMode && err == nil && backend == windowsBackendQEMU,
		QEMUWindowsVMs: countQEMUWindowsVMs(vmconfig.BaseDir()),
	}
}

// countQEMUWindowsVMs counts the VM directories under root laid out for the
// QEMU Windows backend: a qcow2 system disk, or the qemu subdirectory that
// holds the backend's metadata and staged guest tools.
func countQEMUWindowsVMs(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if isQEMUWindowsVMDir(filepath.Join(root, entry.Name())) {
			n++
		}
	}
	return n
}

// isQEMUWindowsVMDir reports whether dir holds a QEMU-backed Windows VM.
//
// TODO: windows_qemu.go is growing a backend-detection helper; fold this into
// it once both have landed.
func isQEMUWindowsVMDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "windows.qcow2")); err == nil {
		return true
	}
	info, err := os.Stat(filepath.Join(dir, "qemu"))
	return err == nil && info.IsDir()
}

// hostDoctorQEMUChecks returns the QEMU Windows readiness checks in the host
// doctor's shape so a plain `cove doctor host` reports them alongside the
// rest. The QEMU host check is dropped: the host report already covers macOS
// on Apple Silicon.
func hostDoctorQEMUChecks() []hostDoctorCheck {
	report := collectQEMUDoctorReport()
	checks := make([]hostDoctorCheck, 0, len(report.Checks))
	for _, check := range report.Checks {
		if check.Name == "host" {
			continue
		}
		checks = append(checks, hostDoctorCheck{Name: "qemu/" + check.Name, Status: check.Status, Message: check.Message})
	}
	return checks
}
