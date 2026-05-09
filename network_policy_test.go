package main

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNetworkPolicyNamed(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantName    string
		wantMode    string
		wantAudit   bool
		wantEnforce bool
		wantDomains bool
		wantCIDRs   bool
	}{
		{"empty defaults to open", "", "open", "nat", false, false, false, false},
		{"offline", "offline", "offline", "none", true, true, false, false},
		{"packages", "packages", "packages", "nat", true, false, true, false},
		{"host-services", "host-services", "host-services", "nat", true, false, true, true},
		{"lan", "lan", "lan", "nat", true, false, false, true},
		{"open named", "open", "open", "nat", false, false, false, false},
		{"case insensitive", "OFFLINE", "offline", "none", true, true, false, false},
		{"trims whitespace", "  packages  ", "packages", "nat", true, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseNetworkPolicy(tt.in)
			if err != nil {
				t.Fatalf("ParseNetworkPolicy(%q): %v", tt.in, err)
			}
			if p.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", p.Name, tt.wantName)
			}
			if string(p.Mode) != tt.wantMode {
				t.Errorf("Mode = %q, want %q", p.Mode, tt.wantMode)
			}
			if p.Audit != tt.wantAudit {
				t.Errorf("Audit = %v, want %v", p.Audit, tt.wantAudit)
			}
			if p.Enforced != tt.wantEnforce {
				t.Errorf("Enforced = %v, want %v", p.Enforced, tt.wantEnforce)
			}
			if (len(p.Domains) > 0) != tt.wantDomains {
				t.Errorf("Domains populated = %v, want %v", len(p.Domains) > 0, tt.wantDomains)
			}
			if (len(p.CIDRs) > 0) != tt.wantCIDRs {
				t.Errorf("CIDRs populated = %v, want %v", len(p.CIDRs) > 0, tt.wantCIDRs)
			}
		})
	}
}

func TestParseNetworkPolicyInvalid(t *testing.T) {
	if _, err := ParseNetworkPolicy("bogus-policy-name"); err == nil {
		t.Fatal("ParseNetworkPolicy(bogus): want error, got nil")
	}
	if err := validateNetworkMode("not-a-mode"); err == nil {
		t.Fatal("validateNetworkMode(not-a-mode): want error, got nil")
	}
}

func TestNetworkPolicyAllows(t *testing.T) {
	pkg, err := ParseNetworkPolicy("packages")
	if err != nil {
		t.Fatalf("ParseNetworkPolicy: %v", err)
	}
	if !pkg.AllowsDomain("pypi.org") {
		t.Error("packages should allow pypi.org")
	}
	if !pkg.AllowsDomain("files.pypi.org") {
		t.Error("packages should allow subdomain files.pypi.org")
	}
	if pkg.AllowsDomain("evil.example.com") {
		t.Error("packages should not allow evil.example.com")
	}
	if pkg.AllowsIP(netip.MustParseAddr("10.0.0.1")) {
		t.Error("packages should not allow RFC1918")
	}

	lan, _ := ParseNetworkPolicy("lan")
	if !lan.AllowsIP(netip.MustParseAddr("192.168.1.1")) {
		t.Error("lan should allow 192.168.1.1")
	}
	if lan.AllowsIP(netip.MustParseAddr("8.8.8.8")) {
		t.Error("lan should not allow 8.8.8.8")
	}

	open, _ := ParseNetworkPolicy("open")
	if !open.AllowsDomain("anything.example") || !open.AllowsIP(netip.MustParseAddr("8.8.8.8")) {
		t.Error("open should allow everything")
	}

	off, _ := ParseNetworkPolicy("offline")
	if off.AllowsDomain("pypi.org") || off.AllowsIP(netip.MustParseAddr("10.0.0.1")) {
		t.Error("offline should allow nothing")
	}
}

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
