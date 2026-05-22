package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/tmc/cove/internal/bytefmt"
)

type hostDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type hostDoctorReport struct {
	OK     bool              `json:"ok"`
	Status string            `json:"status"`
	Checks []hostDoctorCheck `json:"checks"`
}

var hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func handleDoctorHost(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("doctor host", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printDoctorHostUsage(fs.Output()) }
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove doctor host [-json]")
	}
	report := collectHostDoctorReport()
	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return writeHostDoctorReport(w, report)
}

func printDoctorHostUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove doctor host [-json]

Check whether this Mac is ready to create and run cove VMs.

Checks:
  platform       Apple Silicon and macOS version
  codesign       cove binary signing and virtualization entitlement
  disk           free space under cove state root
  state          writable cove state directory
  network        active non-loopback network interface
  helper         privileged helper install/socket state
  xcode          Xcode Command Line Tools availability

Flags:
  -json          emit machine-readable JSON`)
}

func collectHostDoctorReport() hostDoctorReport {
	var checks []hostDoctorCheck
	checks = append(checks, hostDoctorPlatformCheck())
	checks = append(checks, hostDoctorMacOSCheck())
	checks = append(checks, hostDoctorCodesignCheck())
	checks = append(checks, hostDoctorDiskCheck())
	checks = append(checks, hostDoctorStateWritableCheck())
	checks = append(checks, hostDoctorNetworkCheck())
	checks = append(checks, hostDoctorHelperCheck())
	checks = append(checks, hostDoctorXcodeCheck())

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
	return hostDoctorReport{OK: ok, Status: status, Checks: checks}
}

func writeHostDoctorReport(w io.Writer, report hostDoctorReport) error {
	fmt.Fprintf(w, "Host readiness: %s\n", report.Status)
	for _, check := range report.Checks {
		fmt.Fprintf(w, "  %s  %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message)
	}
	if !report.OK {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Resolve failed checks before creating a VM. Re-run `cove doctor host` afterward.")
	}
	return nil
}

func hostDoctorPlatformCheck() hostDoctorCheck {
	if runtime.GOOS != "darwin" {
		return hostDoctorCheck{"apple-silicon", "fail", "cove requires macOS on Apple Silicon"}
	}
	if runtime.GOARCH != "arm64" {
		return hostDoctorCheck{"apple-silicon", "fail", "cove requires an Apple Silicon Mac"}
	}
	return hostDoctorCheck{"apple-silicon", "pass", "running on darwin/arm64"}
}

func hostDoctorMacOSCheck() hostDoctorCheck {
	out, err := hostDoctorRunCommand("sw_vers", "-productVersion")
	if err != nil {
		return hostDoctorCheck{"macos-version", "warn", "could not read macOS version: " + strings.TrimSpace(string(out))}
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return hostDoctorCheck{"macos-version", "warn", "macOS version was empty"}
	}
	return hostDoctorCheck{"macos-version", "pass", version}
}

func hostDoctorCodesignCheck() hostDoctorCheck {
	exe, err := os.Executable()
	if err != nil {
		return hostDoctorCheck{"virtualization-entitlement", "warn", "could not locate cove binary"}
	}
	out, err := hostDoctorRunCommand("codesign", "-d", "--entitlements", ":-", exe)
	text := string(out)
	if err != nil {
		return hostDoctorCheck{"virtualization-entitlement", "fail", "codesign inspection failed: " + strings.TrimSpace(text)}
	}
	if !strings.Contains(text, "com.apple.security.virtualization") {
		return hostDoctorCheck{"virtualization-entitlement", "fail", "missing com.apple.security.virtualization entitlement"}
	}
	return hostDoctorCheck{"virtualization-entitlement", "pass", "cove binary has virtualization entitlement"}
}

func hostDoctorDiskCheck() hostDoctorCheck {
	root := filepath.Clean(coveRoot())
	if err := os.MkdirAll(root, 0o755); err != nil {
		return hostDoctorCheck{"disk-capacity", "fail", fmt.Sprintf("%s is not writable: %v", root, err)}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		return hostDoctorCheck{"disk-capacity", "fail", fmt.Sprintf("stat %s: %v", root, err)}
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	msg := fmt.Sprintf("%s free under %s", bytefmt.Size(free), root)
	if free < 32*1024*1024*1024 {
		return hostDoctorCheck{"disk-capacity", "warn", msg + " (64 GB recommended for a first macOS VM)"}
	}
	return hostDoctorCheck{"disk-capacity", "pass", msg}
}

func hostDoctorStateWritableCheck() hostDoctorCheck {
	root := filepath.Clean(coveRoot())
	if err := os.MkdirAll(root, 0o755); err != nil {
		return hostDoctorCheck{"state-writable", "fail", fmt.Sprintf("create %s: %v", root, err)}
	}
	f, err := os.CreateTemp(root, ".cove-doctor-*")
	if err != nil {
		return hostDoctorCheck{"state-writable", "fail", fmt.Sprintf("%s is not writable: %v", root, err)}
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return hostDoctorCheck{"state-writable", "fail", fmt.Sprintf("close %s: %v", name, err)}
	}
	if err := os.Remove(name); err != nil {
		return hostDoctorCheck{"state-writable", "warn", fmt.Sprintf("remove probe file %s: %v", name, err)}
	}
	return hostDoctorCheck{"state-writable", "pass", root + " writable"}
}

func hostDoctorNetworkCheck() hostDoctorCheck {
	ifaces, err := net.Interfaces()
	if err != nil {
		return hostDoctorCheck{"network", "warn", "could not list network interfaces: " + err.Error()}
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		return hostDoctorCheck{"network", "pass", "active interface " + iface.Name}
	}
	return hostDoctorCheck{"network", "warn", "no active non-loopback interface found"}
}

func hostDoctorHelperCheck() hostDoctorCheck {
	plist := fileExists(helperPlistPath)
	bin := fileExists(helperBinaryPath)
	socket := fileExists(helperSocketPath)
	switch {
	case plist && bin && socket:
		return hostDoctorCheck{"helper", "pass", "privileged helper installed and socket present"}
	case plist && bin:
		return hostDoctorCheck{"helper", "warn", "privileged helper installed but socket is not present; it may be stopped"}
	case plist || bin || socket:
		return hostDoctorCheck{"helper", "warn", "partial privileged helper install; run cove helper status"}
	default:
		return hostDoctorCheck{"helper", "pass", "privileged helper not installed; cove will use one-shot authorization prompts when needed"}
	}
}

func hostDoctorXcodeCheck() hostDoctorCheck {
	out, err := hostDoctorRunCommand("xcode-select", "-p")
	if err != nil {
		return hostDoctorCheck{"xcode-cli", "warn", "Xcode Command Line Tools not found; run xcode-select --install if cove prompts for toolchain support"}
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return hostDoctorCheck{"xcode-cli", "warn", "xcode-select returned an empty path"}
	}
	return hostDoctorCheck{"xcode-cli", "pass", path}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
