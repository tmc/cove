package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	networkx "github.com/tmc/apple/x/vzkit/network"
	"github.com/tmc/cove/internal/runs"
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
	if spec, ok := strings.CutPrefix(in, "egress:"); ok {
		return parseEgressNetworkPolicy(spec)
	}
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

func parseEgressNetworkPolicy(spec string) (NetworkPolicy, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return NetworkPolicy{}, fmt.Errorf("%w: egress policy requires at least one domain, IP, or CIDR", ErrInvalidNetworkSpec)
	}
	var domains []string
	var cidrs []netip.Prefix
	for _, part := range strings.Split(spec, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			return NetworkPolicy{}, fmt.Errorf("%w: egress policy contains an empty allowlist entry", ErrInvalidNetworkSpec)
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			cidrs = append(cidrs, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(item); err == nil {
			cidrs = append(cidrs, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		domain, err := normalizeNetworkPolicyDomain(item)
		if err != nil {
			return NetworkPolicy{}, err
		}
		domains = append(domains, domain)
	}
	return NetworkPolicy{
		Name:       "egress",
		Mode:       NetworkModeNAT,
		Domains:    dedupeStrings(domains),
		CIDRs:      dedupePrefixes(cidrs),
		Audit:      true,
		Limit:      "Virtualization.framework NAT does not expose per-connection host-side allow/deny hooks; this run uses NAT and records the custom egress allowlist.",
		namedInput: true,
	}, nil
}

func normalizeNetworkPolicyDomain(s string) (string, error) {
	domain := strings.TrimSuffix(strings.TrimSpace(s), ".")
	if domain == "" {
		return "", fmt.Errorf("%w: empty egress domain", ErrInvalidNetworkSpec)
	}
	if strings.Contains(domain, "://") || strings.ContainsAny(domain, "/:*[]") {
		return "", fmt.Errorf("%w: invalid egress domain %q", ErrInvalidNetworkSpec, s)
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("%w: invalid egress domain %q", ErrInvalidNetworkSpec, s)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%w: invalid egress domain %q", ErrInvalidNetworkSpec, s)
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return "", fmt.Errorf("%w: invalid egress domain %q", ErrInvalidNetworkSpec, s)
		}
	}
	return domain, nil
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
	path := filepath.Join(dir, "network.log")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write network audit %s: %w", path, err)
	}
	return nil
}

type networkAuditDir interface {
	Dir() string
}

type networkAuditArgs struct {
	RunPrefix string
	Raw       bool
	JSON      bool
}

type networkAuditReport struct {
	RunID        string                `json:"run_id"`
	Dir          string                `json:"dir"`
	LogPath      string                `json:"log_path,omitempty"`
	VMName       string                `json:"vm_name,omitempty"`
	ImageRef     string                `json:"image_ref,omitempty"`
	Status       string                `json:"status,omitempty"`
	ExitCode     *int                  `json:"exit_code,omitempty"`
	Policy       string                `json:"policy,omitempty"`
	Mode         string                `json:"mode,omitempty"`
	StartedAt    string                `json:"started_at,omitempty"`
	Enforcement  string                `json:"enforcement,omitempty"`
	Limitation   string                `json:"limitation,omitempty"`
	AllowDomains []string              `json:"allow_domains,omitempty"`
	AllowCIDRs   []string              `json:"allow_cidrs,omitempty"`
	Decisions    map[string]int        `json:"decisions,omitempty"`
	Events       []networkAuditLogLine `json:"events,omitempty"`
	HasLog       bool                  `json:"has_log"`
}

type networkAuditLogLine struct {
	Timestamp string            `json:"timestamp,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Raw       string            `json:"raw"`
}

func parseNetworkAuditArgs(args []string) (networkAuditArgs, error) {
	fs := flag.NewFlagSet("network audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var out networkAuditArgs
	fs.BoolVar(&out.Raw, "raw", false, "print raw network.log")
	fs.BoolVar(&out.JSON, "json", false, "emit machine-readable JSON")
	if err := fs.Parse(moveKnownFlagsFirst(args, map[string]bool{"raw": false, "json": false})); err != nil {
		return networkAuditArgs{}, err
	}
	if out.Raw && out.JSON {
		return networkAuditArgs{}, fmt.Errorf("network audit: choose only one of --raw or --json")
	}
	if fs.NArg() != 1 {
		return networkAuditArgs{}, fmt.Errorf("usage: cove network audit <run-id-prefix> [--raw|--json]")
	}
	out.RunPrefix = strings.TrimSpace(fs.Arg(0))
	if out.RunPrefix == "" || filepath.Base(out.RunPrefix) != out.RunPrefix {
		return networkAuditArgs{}, fmt.Errorf("network audit: invalid run id prefix %q", out.RunPrefix)
	}
	return out, nil
}

func printNetworkAuditUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove network audit <run-id-prefix> [--raw|--json]

Summarize the network policy audit for one run bundle. By default, cove prints
run status, policy, enforcement, allowlists, limitations, and decision counts.
Use --raw to print network.log unchanged or --json for machine-readable output.`)
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

func RunNetworkAudit(w io.Writer, args []string) error {
	opts, err := parseNetworkAuditArgs(args)
	if err != nil {
		return err
	}
	if opts.Raw {
		return PrintNetworkAudit(w, opts.RunPrefix)
	}
	report, err := LoadNetworkAuditReport(runsDirHook(), opts.RunPrefix)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return RenderNetworkAuditReport(w, report)
}

func PrintNetworkAudit(w io.Writer, runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" || filepath.Base(runID) != runID {
		return fmt.Errorf("network audit: invalid run id %q", runID)
	}
	dir, err := runs.ResolveDir(runsDirHook(), runID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "network.log")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("network audit: read %s: %w", path, err)
	}
	_, err = w.Write(data)
	return err
}

func LoadNetworkAuditReport(root, prefix string) (networkAuditReport, error) {
	show, err := runs.LoadShow(root, prefix)
	if err != nil {
		return networkAuditReport{}, err
	}
	report := networkAuditReport{
		RunID:    show.RunID,
		Dir:      show.Dir,
		VMName:   networkAuditVMName(show),
		ImageRef: networkAuditImageRef(show),
		Status:   show.Result.Status,
		HasLog:   false,
	}
	if show.Result.HasExitCode {
		exit := show.Result.ExitCode
		report.ExitCode = &exit
	}
	logPath := filepath.Join(show.Dir, "network.log")
	report.LogPath = logPath
	data, err := os.ReadFile(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return report, nil
		}
		return networkAuditReport{}, fmt.Errorf("network audit: read %s: %w", logPath, err)
	}
	report.HasLog = true
	if err := parseNetworkAuditLog(data, &report); err != nil {
		return networkAuditReport{}, err
	}
	return report, nil
}

func RenderNetworkAuditReport(w io.Writer, report networkAuditReport) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "run:\t%s\n", report.RunID)
	fmt.Fprintf(tw, "directory:\t%s\n", report.Dir)
	if report.VMName != "" {
		fmt.Fprintf(tw, "vm:\t%s\n", report.VMName)
	}
	if report.ImageRef != "" {
		fmt.Fprintf(tw, "image:\t%s\n", report.ImageRef)
	}
	if report.Status != "" {
		if report.ExitCode != nil {
			fmt.Fprintf(tw, "status:\t%s exit_code=%d\n", report.Status, *report.ExitCode)
		} else {
			fmt.Fprintf(tw, "status:\t%s\n", report.Status)
		}
	}
	if !report.HasLog {
		fmt.Fprintf(tw, "network audit:\tno network.log found\n")
		fmt.Fprintf(tw, "note:\topen policy and legacy runs do not write network audit logs\n")
		return tw.Flush()
	}
	fmt.Fprintf(tw, "network audit:\t%s\n", report.LogPath)
	if report.Policy != "" || report.Mode != "" {
		fmt.Fprintf(tw, "policy:\t%s mode=%s\n", emptyDash(report.Policy), emptyDash(report.Mode))
	}
	if report.Enforcement != "" {
		fmt.Fprintf(tw, "enforcement:\t%s\n", report.Enforcement)
	}
	if report.StartedAt != "" {
		fmt.Fprintf(tw, "started at:\t%s\n", report.StartedAt)
	}
	if len(report.AllowDomains) > 0 {
		fmt.Fprintf(tw, "allow domains:\t%s\n", strings.Join(report.AllowDomains, ", "))
	}
	if len(report.AllowCIDRs) > 0 {
		fmt.Fprintf(tw, "allow cidrs:\t%s\n", strings.Join(report.AllowCIDRs, ", "))
	}
	if report.Limitation != "" {
		fmt.Fprintf(tw, "limitation:\t%s\n", report.Limitation)
	}
	if len(report.Decisions) > 0 {
		keys := make([]string, 0, len(report.Decisions))
		for key := range report.Decisions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var parts []string
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", key, report.Decisions[key]))
		}
		fmt.Fprintf(tw, "decisions:\t%s\n", strings.Join(parts, ", "))
	}
	return tw.Flush()
}

func parseNetworkAuditLog(data []byte, report *networkAuditReport) error {
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			parseNetworkAuditHeader(strings.TrimSpace(strings.TrimPrefix(line, "#")), report)
			continue
		}
		event := parseNetworkAuditEvent(line)
		report.Events = append(report.Events, event)
		if decision := event.Fields["decision"]; decision != "" {
			if report.Decisions == nil {
				report.Decisions = map[string]int{}
			}
			report.Decisions[decision]++
		}
	}
	return nil
}

func parseNetworkAuditHeader(line string, report *networkAuditReport) {
	if line == "cove network audit" {
		return
	}
	for _, field := range strings.Fields(line) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "policy":
			report.Policy = value
		case "mode":
			report.Mode = value
		case "started_at":
			report.StartedAt = value
		case "allow_domains":
			report.AllowDomains = splitCommaList(value)
		case "allow_cidrs":
			report.AllowCIDRs = splitCommaList(value)
		case "enforcement":
			report.Enforcement = value
		}
	}
	if strings.HasPrefix(line, "limitation=") {
		report.Limitation = strings.TrimPrefix(line, "limitation=")
	}
}

func parseNetworkAuditEvent(line string) networkAuditLogLine {
	event := networkAuditLogLine{Raw: line, Fields: map[string]string{}}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return event
	}
	if !strings.Contains(fields[0], "=") {
		event.Timestamp = fields[0]
		fields = fields[1:]
	}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		event.Fields[key] = value
	}
	return event
}

func splitCommaList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func dedupePrefixes(in []netip.Prefix) []netip.Prefix {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		key := p.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

func networkAuditVMName(show runs.Show) string {
	for _, event := range show.Events {
		if event.VMName != "" {
			return event.VMName
		}
	}
	return ""
}

func networkAuditImageRef(show runs.Show) string {
	for _, event := range show.Events {
		if event.ImageRef != "" {
			return event.ImageRef
		}
	}
	return ""
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
