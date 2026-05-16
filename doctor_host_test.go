package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDoctorHostJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldRun := hostDoctorRunCommand
	t.Cleanup(func() { hostDoctorRunCommand = oldRun })
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		switch name {
		case "sw_vers":
			return []byte("15.5\n"), nil
		case "codesign":
			return []byte("<key>com.apple.security.virtualization</key><true/>"), nil
		case "xcode-select":
			return []byte("/Library/Developer/CommandLineTools\n"), nil
		default:
			return nil, nil
		}
	}

	var buf bytes.Buffer
	if err := handleDoctorHost([]string{"-json"}, &buf); err != nil {
		t.Fatalf("handleDoctorHost: %v", err)
	}
	var got hostDoctorReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("doctor host json: %v\n%s", err, buf.String())
	}
	if len(got.Checks) == 0 {
		t.Fatalf("no checks in report: %s", buf.String())
	}
	if strings.Contains(buf.String(), "cove-version-floor") {
		t.Fatalf("doctor host included action-doctor version floor check:\n%s", buf.String())
	}
}

func TestDoctorHostHumanOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldRun := hostDoctorRunCommand
	t.Cleanup(func() { hostDoctorRunCommand = oldRun })
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		switch name {
		case "sw_vers":
			return []byte("15.5\n"), nil
		case "codesign":
			return []byte("<key>com.apple.security.virtualization</key><true/>"), nil
		case "xcode-select":
			return []byte("/Library/Developer/CommandLineTools\n"), nil
		default:
			return nil, nil
		}
	}

	var buf bytes.Buffer
	if err := handleDoctorHost(nil, &buf); err != nil {
		t.Fatalf("handleDoctorHost: %v", err)
	}
	for _, want := range []string{"Host readiness:", "apple-silicon", "virtualization-entitlement"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("doctor host output missing %q:\n%s", want, buf.String())
		}
	}
}
