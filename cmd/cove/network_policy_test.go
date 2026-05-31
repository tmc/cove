package main

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/metrics"
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
	for _, spec := range []string{
		"egress:",
		"egress:api.openai.com,,ghcr.io",
		"egress:https://api.openai.com",
		"egress:api.openai.com:443",
		"egress:-bad.example",
	} {
		if _, err := ParseNetworkPolicy(spec); err == nil {
			t.Fatalf("ParseNetworkPolicy(%q): want error, got nil", spec)
		}
	}
	if err := validateNetworkMode("not-a-mode"); err == nil {
		t.Fatal("validateNetworkMode(not-a-mode): want error, got nil")
	}
	if _, err := ParseNetworkMode("egress:https://api.openai.com"); err == nil || !strings.Contains(err.Error(), "invalid egress domain") {
		t.Fatalf("ParseNetworkMode invalid egress = %v, want invalid egress domain", err)
	}
}

func TestParseNetworkPolicyEgressAllowlist(t *testing.T) {
	policy, err := ParseNetworkPolicy("egress:API.OpenAI.com,ghcr.io,10.0.0.0/8,192.168.1.10")
	if err != nil {
		t.Fatalf("ParseNetworkPolicy egress: %v", err)
	}
	if policy.Name != "egress" || policy.Mode != NetworkModeNAT || !policy.Audit || policy.Enforced {
		t.Fatalf("policy = %+v", policy)
	}
	if got := strings.Join(policy.Domains, ","); got != "api.openai.com,ghcr.io" {
		t.Fatalf("domains = %q", got)
	}
	if got := prefixStrings(policy.CIDRs); strings.Join(got, ",") != "10.0.0.0/8,192.168.1.10/32" {
		t.Fatalf("cidrs = %v", got)
	}
	if !policy.AllowsDomain("files.api.openai.com") {
		t.Fatal("egress policy should allow subdomains")
	}
	if !policy.AllowsIP(netip.MustParseAddr("10.1.2.3")) {
		t.Fatal("egress policy should allow CIDR member")
	}
	if !policy.AllowsIP(netip.MustParseAddr("192.168.1.10")) {
		t.Fatal("egress policy should allow single IP")
	}
	if policy.AllowsDomain("example.com") || policy.AllowsIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("egress policy allowed an unlisted destination")
	}
}

func TestParseNetworkModeAcceptsEgressPolicy(t *testing.T) {
	cfg, err := ParseNetworkMode("egress:api.openai.com,10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseNetworkMode egress: %v", err)
	}
	if cfg.Mode != NetworkModeNAT {
		t.Fatalf("mode = %q, want nat", cfg.Mode)
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
	hooks, _ := stubAcquireRunLockHook(t)
	hooks.RunMacOSVM = runHook(func() error { return nil })

	cfg := RunConfig{VM: vmSelection{Name: "plain-vm", Directory: t.TempDir()}, Hooks: hooks}
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

func TestWriteNetworkPolicyAuditEgress(t *testing.T) {
	policy, err := ParseNetworkPolicy("egress:api.openai.com,10.0.0.0/8")
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
		"# policy=egress mode=nat",
		"# allow_domains=api.openai.com",
		"# allow_cidrs=10.0.0.0/8",
		"# enforcement=not-hooked",
		"dest=* decision=policy-loaded policy=egress",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("network.log missing %q:\n%s", want, out)
		}
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

func TestRunNetworkAuditSummaryJSONAndRaw(t *testing.T) {
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	dir := filepath.Join(runsRoot, "20260531-alpha")
	writeNetworkAuditMetrics(t, dir, "job-vm", "macos:latest", "ok", 0)
	policy, err := ParseNetworkPolicy("packages")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteNetworkPolicyAudit(dir, policy); err != nil {
		t.Fatal(err)
	}

	var human bytes.Buffer
	if err := RunNetworkAudit(&human, []string{"20260531"}); err != nil {
		t.Fatalf("RunNetworkAudit summary: %v", err)
	}
	for _, want := range []string{
		"run:",
		"20260531-alpha",
		"vm:",
		"job-vm",
		"policy:",
		"packages mode=nat",
		"enforcement:",
		"not-hooked",
		"decisions:",
		"policy-loaded=1",
	} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, human.String())
		}
	}

	var raw bytes.Buffer
	if err := RunNetworkAudit(&raw, []string{"20260531", "--raw"}); err != nil {
		t.Fatalf("RunNetworkAudit raw: %v", err)
	}
	if !strings.Contains(raw.String(), "# cove network audit") {
		t.Fatalf("raw audit = %q", raw.String())
	}

	var js bytes.Buffer
	if err := RunNetworkAudit(&js, []string{"20260531", "--json"}); err != nil {
		t.Fatalf("RunNetworkAudit json: %v", err)
	}
	var report networkAuditReport
	if err := json.Unmarshal(js.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, js.String())
	}
	if report.RunID != "20260531-alpha" || report.Policy != "packages" || report.Mode != "nat" || report.Decisions["policy-loaded"] != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunNetworkAuditNoLog(t *testing.T) {
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	dir := filepath.Join(runsRoot, "open-run")
	writeNetworkAuditMetrics(t, dir, "open-vm", "", "ok", 0)
	var out bytes.Buffer
	if err := RunNetworkAudit(&out, []string{"open"}); err != nil {
		t.Fatalf("RunNetworkAudit: %v", err)
	}
	if !strings.Contains(out.String(), "no network.log found") {
		t.Fatalf("summary = %s", out.String())
	}
}

func TestRunNetworkAuditFlagConflict(t *testing.T) {
	err := RunNetworkAudit(&bytes.Buffer{}, []string{"run", "--raw", "--json"})
	if err == nil || !strings.Contains(err.Error(), "choose only one") {
		t.Fatalf("err = %v, want flag conflict", err)
	}
}

func TestWriteNetworkPolicyAuditWrapsWriteError(t *testing.T) {
	// Plant a directory at network.log so WriteFile fails; exercises the
	// wrap path independently of the MkdirAll branch.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "network.log"), 0755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	policy := NetworkPolicy{Name: "package", Audit: true}
	err := WriteNetworkPolicyAudit(dir, policy)
	if err == nil {
		t.Fatalf("WriteNetworkPolicyAudit: expected error")
	}
	if !strings.Contains(err.Error(), "write network audit") {
		t.Fatalf("error %q missing wrap prefix", err)
	}
	if !strings.Contains(err.Error(), "network.log") {
		t.Fatalf("error %q missing path", err)
	}
}

func writeNetworkAuditMetrics(t *testing.T, dir, vm, image, status string, exitCode int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	events := []metrics.Event{
		{
			Timestamp: "2026-05-31T12:00:00Z",
			EventType: "vm_start",
			VMName:    vm,
			ImageRef:  image,
			Status:    "ok",
		},
		{
			Timestamp:  "2026-05-31T12:00:05Z",
			EventType:  "run_complete",
			VMName:     vm,
			ImageRef:   image,
			Status:     status,
			DurationMS: 5000,
			Extra: map[string]any{
				"exit_code": exitCode,
			},
		},
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, event := range events {
		if err := enc.Encode(event); err != nil {
			t.Fatalf("encode metric: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), b.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile metrics: %v", err)
	}
}

func prefixStrings(in []netip.Prefix) []string {
	out := make([]string, 0, len(in))
	for _, prefix := range in {
		out = append(out, prefix.String())
	}
	return out
}
