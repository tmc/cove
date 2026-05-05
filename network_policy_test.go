package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteNetworkPolicyAudit(t *testing.T) {
	policy, err := ParseNetworkPolicy("packages")
	if err != nil {
		t.Fatalf("ParseNetworkPolicy: %v", err)
	}
	dir := t.TempDir()
	if err := WriteNetworkPolicyAudit(dir, policy); err != nil {
		t.Fatalf("WriteNetworkPolicyAudit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "network.log"))
	if err != nil {
		t.Fatalf("ReadFile(network.log): %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"# policy=packages mode=nat",
		"# allow_domains=deb.debian.org,security.debian.org",
		"# enforcement=not-hooked",
		"dest=* decision=policy-loaded policy=packages",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("network.log missing %q:\n%s", want, out)
		}
	}
}

func TestWriteNetworkPolicyAuditSkipsOpen(t *testing.T) {
	policy, err := ParseNetworkPolicy("open")
	if err != nil {
		t.Fatalf("ParseNetworkPolicy: %v", err)
	}
	dir := t.TempDir()
	if err := WriteNetworkPolicyAudit(dir, policy); err != nil {
		t.Fatalf("WriteNetworkPolicyAudit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "network.log")); !os.IsNotExist(err) {
		t.Fatalf("network.log stat error = %v, want not exist", err)
	}
}

func TestRunVMWithConfigWritesNetworkPolicyAudit(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	prevNetwork := networkMode
	runsDirHook = func() string { return runsRoot }
	networkMode = "lan"
	t.Cleanup(func() {
		runsDirHook = prevRuns
		networkMode = prevNetwork
	})
	stubAcquireRunLockHook(t)
	prevMac := runMacOSVMHook
	runMacOSVMHook = func() error { return nil }
	t.Cleanup(func() { runMacOSVMHook = prevMac })

	cfg := RunConfig{VM: vmSelection{Name: "plain-vm", Directory: t.TempDir()}}
	if err := runVMWithConfig(cfg); err != nil {
		t.Fatalf("runVMWithConfig: %v", err)
	}
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("run dirs = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(runsRoot, entries[0].Name(), "network.log"))
	if err != nil {
		t.Fatalf("ReadFile(network.log): %v", err)
	}
	if !strings.Contains(string(data), "# policy=lan mode=nat") {
		t.Fatalf("network.log = %s", data)
	}
}

func TestPrintNetworkAudit(t *testing.T) {
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	dir := filepath.Join(runsRoot, "abcd1234")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "network.log"), []byte("audit\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var out bytes.Buffer
	if err := PrintNetworkAudit(&out, "abcd1234"); err != nil {
		t.Fatalf("PrintNetworkAudit: %v", err)
	}
	if out.String() != "audit\n" {
		t.Fatalf("output = %q", out.String())
	}
	if err := PrintNetworkAudit(&out, "../abcd1234"); err == nil {
		t.Fatal("PrintNetworkAudit accepted path traversal")
	}
}
