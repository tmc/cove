package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tmc/cove/internal/bytefmt"
	winsetup "github.com/tmc/cove/internal/windows"
)

var errDoctorQEMUFailed = errors.New("qemu readiness failed")

type qemuDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
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
  qemu-img         qemu-img executable
  efi-code         AArch64 EFI pflash code image
  efi-vars         writable pflash vars template
  display          COVE_QEMU_DISPLAY_DEVICE value
  virtio-drivers   cached ARM64 VirtIO driver ISO, if present

Flags:
  -json            emit machine-readable JSON`)
}

func collectQEMUDoctorReport() qemuDoctorReport {
	checks := []qemuDoctorCheck{
		qemuDoctorHostCheck(),
		qemuDoctorToolCheck("qemu-system-aarch64", "COVE_QEMU_SYSTEM_AARCH64", "qemu-system-aarch64"),
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
		return qemuDoctorCheck{name, "fail", err.Error()}
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

func qemuDoctorVirtIODriversCheck() qemuDoctorCheck {
	cacheDir, err := winsetup.DefaultVirtIODriversCacheDir()
	if err != nil {
		return qemuDoctorCheck{"virtio-drivers", "warn", fmt.Sprintf("could not find driver cache directory: %v", err)}
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "virtio-win-*.iso"))
	if err != nil {
		return qemuDoctorCheck{"virtio-drivers", "fail", fmt.Sprintf("scan %s: %v", cacheDir, err)}
	}
	for _, path := range matches {
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			return qemuDoctorCheck{"virtio-drivers", "pass", fmt.Sprintf("%s (%s)", path, bytefmt.Size(info.Size()))}
		}
	}
	return qemuDoctorCheck{"virtio-drivers", "warn", fmt.Sprintf("not cached under %s; first Windows install downloads ARM64 VirtIO drivers", cacheDir)}
}
