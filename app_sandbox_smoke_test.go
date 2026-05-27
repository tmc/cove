//go:build darwin

package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppSandboxEntitlementFixture(t *testing.T) {
	path := filepath.Join("internal", "autosign", "app_sandbox.entitlements")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := xml.Unmarshal(data, new(any)); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, key := range []string{
		"com.apple.security.app-sandbox",
		"com.apple.security.files.user-selected.read-write",
		"com.apple.security.network.client",
		"com.apple.security.network.server",
		"com.apple.security.virtualization",
	} {
		if !bytes.Contains(data, []byte("<key>"+key+"</key>")) {
			t.Fatalf("%s missing entitlement %s", path, key)
		}
	}
}

func TestAppSandboxSmoke(t *testing.T) {
	if os.Getenv("COVE_APP_SANDBOX_SMOKE") != "1" {
		t.Skip("set COVE_APP_SANDBOX_SMOKE=1 to build and run a sandbox-signed cove binary")
	}
	if _, err := exec.LookPath("codesign"); err != nil {
		t.Skipf("codesign unavailable: %v", err)
	}

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "cove-sandboxed")
	entitlements, err := filepath.Abs(filepath.Join("internal", "autosign", "app_sandbox.entitlements"))
	if err != nil {
		t.Fatalf("resolve app sandbox entitlements: %v", err)
	}

	out, err := runSandboxSmokeCommand(t, 3*time.Minute, "go", "build", "-o", bin, ".")
	if err != nil {
		t.Fatalf("build sandbox smoke binary: %v\n%s", err, out)
	}
	out, err = runSandboxSmokeCommand(t, time.Minute, "codesign", "-s", "-", "-f", "--entitlements", entitlements, bin)
	if err != nil {
		t.Fatalf("sign sandbox smoke binary: %v\n%s", err, out)
	}
	out, err = runSandboxSmokeCommand(t, time.Minute, "codesign", "-d", "--entitlements", ":-", bin)
	if err != nil {
		t.Fatalf("inspect entitlements: %v\n%s", err, out)
	}
	t.Logf("entitlements:\n%s", out)
	out, err = runSandboxSmokeCommand(t, time.Minute, "spctl", "--assess", "--type", "execute", "-vv", bin)
	t.Logf("spctl err=%v output:\n%s", err, out)

	cases := []struct {
		name string
		args []string
	}{
		{name: "version", args: []string{"--version"}},
		{name: "help", args: []string{"help"}},
		{name: "list", args: []string{"list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runSandboxedCoveSmokeCommand(t, bin, tc.args...)
			t.Logf("%s %s err=%v output:\n%s", filepath.Base(bin), strings.Join(tc.args, " "), err, out)
			if os.Getenv("COVE_APP_SANDBOX_SMOKE_EXPECT_START") == "1" && err != nil {
				t.Fatalf("%s %v: %v\n%s", bin, tc.args, err, out)
			}
		})
	}
}

func runSandboxSmokeCommand(t *testing.T, timeout time.Duration, name string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%s %s: timeout after %s", name, strings.Join(args, " "), timeout)
	}
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func runSandboxedCoveSmokeCommand(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%s %s: timeout after 15s", bin, strings.Join(args, " "))
	}
	return string(out), err
}
