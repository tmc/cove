package main

import (
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	networkx "github.com/tmc/apple/x/vzkit/network"
)

type NetworkPolicy struct {
	Name       string
	Mode       networkx.Mode
	Domains    []string
	CIDRs      []netip.Prefix
	Audit      bool
	Enforced   bool
	Limit      string
	namedInput bool
}

var packagePolicyDomains = []string{
	"deb.debian.org",
	"security.debian.org",
	"archive.ubuntu.com",
	"security.ubuntu.com",
	"ports.ubuntu.com",
	"pypi.org",
	"files.pythonhosted.org",
	"registry.npmjs.org",
	"ghcr.io",
	"registry-1.docker.io",
	"auth.docker.io",
	"production.cloudflare.docker.com",
	"registry.fedoraproject.org",
	"mirrors.fedoraproject.org",
}

var rfc1918PolicyCIDRs = mustParsePrefixes([]string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
})

func ParseNetworkPolicy(s string) (NetworkPolicy, error) {
	in := strings.ToLower(strings.TrimSpace(s))
	switch in {
	case "":
		return openNetworkPolicy(false), nil
	case "offline":
		return NetworkPolicy{
			Name:       "offline",
			Mode:       NetworkModeNone,
			Audit:      true,
			Enforced:   true,
			namedInput: true,
		}, nil
	case "packages":
		return NetworkPolicy{
			Name:       "packages",
			Mode:       NetworkModeNAT,
			Domains:    append([]string(nil), packagePolicyDomains...),
			Audit:      true,
			Limit:      "Virtualization.framework NAT does not expose per-connection host-side allow/deny hooks; this run uses NAT and records the intended allowlist.",
			namedInput: true,
		}, nil
	case "host-services":
		return NetworkPolicy{
			Name:       in,
			Mode:       NetworkModeNAT,
			Domains:    append([]string(nil), packagePolicyDomains...),
			CIDRs:      append([]netip.Prefix(nil), rfc1918PolicyCIDRs...),
			Audit:      true,
			Limit:      "Virtualization.framework NAT does not expose per-connection host-side allow/deny hooks; this run uses NAT and records the intended package and RFC1918 policy.",
			namedInput: true,
		}, nil
	case "lan":
		return NetworkPolicy{
			Name:       in,
			Mode:       NetworkModeNAT,
			CIDRs:      append([]netip.Prefix(nil), rfc1918PolicyCIDRs...),
			Audit:      true,
			Limit:      "Virtualization.framework NAT does not expose per-connection host-side allow/deny hooks; this run uses NAT and records the intended RFC1918 policy.",
			namedInput: true,
		}, nil
	case "open":
		p := openNetworkPolicy(true)
		return p, nil
	default:
		cfg, err := ParseNetworkMode(s)
		if err != nil {
			return NetworkPolicy{}, err
		}
		return NetworkPolicy{Name: string(cfg.Mode), Mode: cfg.Mode}, nil
	}
}

func openNetworkPolicy(named bool) NetworkPolicy {
	return NetworkPolicy{
		Name:       "open",
		Mode:       NetworkModeNAT,
		namedInput: named,
	}
}

func (p NetworkPolicy) NetworkConfig() networkx.Config {
	return networkx.Config{Mode: p.Mode}
}

func (p NetworkPolicy) IsNamed() bool {
	return p.namedInput
}

func (p NetworkPolicy) ShouldAudit() bool {
	return p.Audit && p.Name != "" && p.Name != "open"
}

func (p NetworkPolicy) AllowsDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return false
	}
	if p.Name == "open" {
		return true
	}
	for _, allowed := range p.Domains {
		if domain == allowed || strings.HasSuffix(domain, "."+allowed) {
			return true
		}
	}
	return false
}

func (p NetworkPolicy) AllowsIP(addr netip.Addr) bool {
	if p.Name == "open" {
		return true
	}
	for _, cidr := range p.CIDRs {
		if cidr.Contains(addr) {
			return true
		}
	}
	return false
}

func WriteNetworkPolicyAudit(dir string, policy NetworkPolicy) error {
	if !policy.ShouldAudit() {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create network audit dir: %w", err)
	}
	var b strings.Builder
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	fmt.Fprintf(&b, "# cove network audit\n")
	fmt.Fprintf(&b, "# policy=%s mode=%s started_at=%s\n", policy.Name, policy.Mode, ts)
	if len(policy.Domains) > 0 {
		fmt.Fprintf(&b, "# allow_domains=%s\n", strings.Join(policy.Domains, ","))
	}
	if len(policy.CIDRs) > 0 {
		parts := make([]string, 0, len(policy.CIDRs))
		for _, cidr := range policy.CIDRs {
			parts = append(parts, cidr.String())
		}
		fmt.Fprintf(&b, "# allow_cidrs=%s\n", strings.Join(parts, ","))
	}
	switch {
	case policy.Enforced:
		fmt.Fprintf(&b, "# enforcement=virtual-network-disabled\n")
	case policy.Limit != "":
		fmt.Fprintf(&b, "# enforcement=not-hooked\n")
		fmt.Fprintf(&b, "# limitation=%s\n", policy.Limit)
	default:
		fmt.Fprintf(&b, "# enforcement=not-hooked\n")
	}
	fmt.Fprintf(&b, "%s dest=* decision=policy-loaded policy=%s\n", ts, policy.Name)
	return os.WriteFile(filepath.Join(dir, "network.log"), []byte(b.String()), 0644)
}

type networkAuditDir interface {
	Dir() string
}

func writeActiveNetworkPolicyAudit(run networkAuditDir) {
	if run == nil {
		return
	}
	policy, err := ParseNetworkPolicy(networkMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: network policy audit: %v\n", err)
		return
	}
	if !policy.ShouldAudit() {
		return
	}
	if err := WriteNetworkPolicyAudit(run.Dir(), policy); err != nil {
		fmt.Fprintf(os.Stderr, "warning: network policy audit: %v\n", err)
	}
}

func PrintNetworkAudit(w io.Writer, runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" || filepath.Base(runID) != runID {
		return fmt.Errorf("network audit: invalid run id %q", runID)
	}
	path := filepath.Join(runsDirHook(), runID, "network.log")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("network audit: read %s: %w", path, err)
	}
	_, err = w.Write(data)
	return err
}

func mustParsePrefixes(in []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			panic(err)
		}
		out = append(out, p)
	}
	return out
}
