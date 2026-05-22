package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorQEMUJSON(t *testing.T) {
	dir := t.TempDir()
	qemu := writeDoctorQEMUTool(t, dir, "qemu-system-aarch64", "QEMU emulator version test")
	qemuImg := writeDoctorQEMUTool(t, dir, "qemu-img", "qemu-img version test")
	code := writeDoctorQEMUFile(t, dir, "code.fd")
	vars := writeDoctorQEMUFile(t, dir, "vars.fd")
	t.Setenv("COVE_QEMU_SYSTEM_AARCH64", qemu)
	t.Setenv("COVE_QEMU_IMG", qemuImg)
	t.Setenv("COVE_QEMU_EFI_CODE", code)
	t.Setenv("COVE_QEMU_EFI_VARS_TEMPLATE", vars)
	t.Setenv("COVE_QEMU_DISPLAY_DEVICE", "ramfb+virtio-gpu-pci")
	t.Setenv("HOME", dir)

	var buf bytes.Buffer
	err := handleDoctorQEMU([]string{"-json"}, &buf)
	if err != nil && !errors.Is(err, errDoctorQEMUFailed) {
		t.Fatalf("handleDoctorQEMU: %v", err)
	}
	var got qemuDoctorReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("doctor qemu json: %v\n%s", err, buf.String())
	}
	if len(got.Checks) == 0 {
		t.Fatalf("no checks in report: %s", buf.String())
	}
	for _, want := range []string{"qemu-system-aarch64", "qemu-img", "efi-code", "efi-vars-template", "display", "screenshot-backend", "text-backend", "qemu-vdagent"} {
		if !qemuDoctorHasCheck(got, want) {
			t.Fatalf("doctor qemu JSON missing check %q: %#v", want, got.Checks)
		}
	}
}

func TestDoctorQEMUMissingToolFails(t *testing.T) {
	dir := t.TempDir()
	qemuImg := writeDoctorQEMUTool(t, dir, "qemu-img", "qemu-img version test")
	code := writeDoctorQEMUFile(t, dir, "code.fd")
	vars := writeDoctorQEMUFile(t, dir, "vars.fd")
	t.Setenv("COVE_QEMU_SYSTEM_AARCH64", filepath.Join(dir, "missing-qemu"))
	t.Setenv("COVE_QEMU_IMG", qemuImg)
	t.Setenv("COVE_QEMU_EFI_CODE", code)
	t.Setenv("COVE_QEMU_EFI_VARS_TEMPLATE", vars)
	t.Setenv("HOME", dir)

	var buf bytes.Buffer
	err := handleDoctorQEMU(nil, &buf)
	if !errors.Is(err, errDoctorQEMUFailed) {
		t.Fatalf("handleDoctorQEMU err = %v, want %v", err, errDoctorQEMUFailed)
	}
	if !strings.Contains(buf.String(), "FAIL  qemu-system-aarch64") {
		t.Fatalf("doctor qemu output missing failed qemu-system check:\n%s", buf.String())
	}
}

func TestDoctorQEMUInvalidDisplayFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COVE_QEMU_SYSTEM_AARCH64", writeDoctorQEMUTool(t, dir, "qemu-system-aarch64", "QEMU emulator version test"))
	t.Setenv("COVE_QEMU_IMG", writeDoctorQEMUTool(t, dir, "qemu-img", "qemu-img version test"))
	t.Setenv("COVE_QEMU_EFI_CODE", writeDoctorQEMUFile(t, dir, "code.fd"))
	t.Setenv("COVE_QEMU_EFI_VARS_TEMPLATE", writeDoctorQEMUFile(t, dir, "vars.fd"))
	t.Setenv("COVE_QEMU_DISPLAY_DEVICE", "bad-display")
	t.Setenv("HOME", dir)

	var buf bytes.Buffer
	err := handleDoctorQEMU(nil, &buf)
	if !errors.Is(err, errDoctorQEMUFailed) {
		t.Fatalf("handleDoctorQEMU err = %v, want %v", err, errDoctorQEMUFailed)
	}
	if !strings.Contains(buf.String(), "FAIL  display") {
		t.Fatalf("doctor qemu output missing failed display check:\n%s", buf.String())
	}
}

func TestDoctorQEMUInvalidBackendEnvFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COVE_QEMU_SYSTEM_AARCH64", writeDoctorQEMUTool(t, dir, "qemu-system-aarch64", "QEMU emulator version test"))
	t.Setenv("COVE_QEMU_IMG", writeDoctorQEMUTool(t, dir, "qemu-img", "qemu-img version test"))
	t.Setenv("COVE_QEMU_EFI_CODE", writeDoctorQEMUFile(t, dir, "code.fd"))
	t.Setenv("COVE_QEMU_EFI_VARS_TEMPLATE", writeDoctorQEMUFile(t, dir, "vars.fd"))
	t.Setenv("COVE_QEMU_TEXT_BACKEND", "bad")
	t.Setenv("HOME", dir)

	var buf bytes.Buffer
	err := handleDoctorQEMU(nil, &buf)
	if !errors.Is(err, errDoctorQEMUFailed) {
		t.Fatalf("handleDoctorQEMU err = %v, want %v", err, errDoctorQEMUFailed)
	}
	if !strings.Contains(buf.String(), "FAIL  text-backend") {
		t.Fatalf("doctor qemu output missing failed text backend check:\n%s", buf.String())
	}
}

func qemuDoctorHasCheck(report qemuDoctorReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name {
			return true
		}
	}
	return false
}

func writeDoctorQEMUTool(t *testing.T, dir, name, output string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	text := "#!/bin/sh\n" +
		"if [ \"$1 $2 $3\" = \"-machine none -chardev\" ] && [ \"$4\" = \"help\" ]; then\n" +
		"  printf '%s\\n' 'Available chardev backend types:' '  qemu-vdagent'\n" +
		"  exit 0\n" +
		"fi\n" +
		"printf '%s\\n' " + doctorQEMUShellQuote(output) + "\n"
	if err := os.WriteFile(path, []byte(text), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeDoctorQEMUFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(name), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func doctorQEMUShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
