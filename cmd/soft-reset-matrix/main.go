// soft-reset-matrix renders the soft-reset isolation test matrix.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type concern struct {
	ID       string
	Name     string
	Question string
	Probe    string
}

type finding struct {
	Status string
	Note   string
}

var profiles = map[string][]concern{
	"soft-reset": {
		{
			ID:       "tcc",
			Name:     "TCC residue",
			Question: "Does a deleted user's TCC grant become visible to a newly-created eval user?",
			Probe:    "User A grants a protected permission, User A is deleted, User B is created, and the same access is attempted without a new prompt.",
		},
		{
			ID:       "keychain",
			Name:     "System Keychain residue",
			Question: "Does a trust or credential change made by one eval user survive into the next eval user?",
			Probe:    "User A installs a test root certificate or credential, User A is deleted, User B checks whether the trust item remains effective.",
		},
		{
			ID:       "appleid",
			Name:     "Apple Account throttling",
			Question: "Where is the practical Apple Account lifecycle limit on one VM identity?",
			Probe:    "Run bounded Apple Account login cycles on a dedicated VM and record the first throttle, lockout, or policy prompt.",
		},
		{
			ID:       "globalprefs",
			Name:     "GlobalPreferences leakage",
			Question: "Do system-wide preferences changed by one eval user alter the next eval user's environment?",
			Probe:    "User A changes a measurable global preference, User A is deleted, User B reads the preference before making any change.",
		},
		{
			ID:       "securetoken",
			Name:     "FileVault SecureToken cycle",
			Question: "How many user delete/create cycles can receive a usable SecureToken on macOS 15+?",
			Probe:    "Create and delete users in a bounded loop on a FileVault-enabled disposable VM, recording the first sysadminctl SecureToken failure.",
		},
		{
			ID:       "daemon",
			Name:     "Orphaned LaunchDaemon residue",
			Question: "Can one eval user leave privileged system state behind for the next eval user?",
			Probe:    "User A installs a test LaunchDaemon, User A is deleted, User B checks whether the daemon or plist remains.",
		},
	},
	"boundary": {
		{
			ID:       "tcc",
			Name:     "TCC isolation",
			Question: "Does protected guest access remain scoped to guest identity instead of host TCC state?",
			Probe:    "Attempt protected operations from the guest and confirm the host neither grants nor inherits permission outside explicit cove control paths.",
		},
		{
			ID:       "network",
			Name:     "Network isolation",
			Question: "Can cove state what host network surfaces a guest can reach by default?",
			Probe:    "Record default NAT behavior, host-reachable services, and any cove-managed network controls needed for eval isolation.",
		},
		{
			ID:       "disk",
			Name:     "Disk-write boundary",
			Question: "Can guest writes escape the VM bundle without an explicit shared-folder or host-copy operation?",
			Probe:    "Write inside the guest, inspect the VM bundle, and verify no host filesystem path outside configured VM storage changed.",
		},
		{
			ID:       "kext",
			Name:     "Kernel-extension visibility",
			Question: "Can guest workloads load or observe host kernel extensions beyond Virtualization.framework boundaries?",
			Probe:    "Compare guest-visible extension and driver state with host state and record any exposed host-only surface.",
		},
		{
			ID:       "machineid",
			Name:     "Machine-identity collision",
			Question: "Do forks expose duplicate identifiers that break per-eval isolation or external account policy?",
			Probe:    "Record hardware UUID, serial-like identifiers, hostnames, and account-policy signals across parent and forked guests.",
		},
		{
			ID:       "time",
			Name:     "Time-source determinism",
			Question: "Can cove bound or explain guest time drift, clock jumps, and host time coupling for reproducible evals?",
			Probe:    "Measure wall-clock, monotonic clock, suspend/resume drift, and forked-guest clock behavior against host time.",
		},
	},
}

func main() {
	var out string
	var vm string
	var host string
	var profile string
	var resultFlags arrayFlags
	flag.StringVar(&out, "out", "", "optional markdown output path")
	flag.StringVar(&vm, "vm", "", "VM name used for the run")
	flag.StringVar(&host, "host", "", "host label")
	flag.StringVar(&profile, "profile", "soft-reset", "matrix profile: soft-reset or boundary")
	flag.Var(&resultFlags, "result", "result in concern=status:note form; repeatable")
	flag.Parse()

	concerns, err := concernsForProfile(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soft-reset-matrix: %v\n", err)
		os.Exit(1)
	}
	findings, err := parseFindings(concerns, resultFlags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soft-reset-matrix: %v\n", err)
		os.Exit(1)
	}
	doc := renderMatrix(profile, concerns, vm, host, findings)
	if out != "" {
		if err := os.WriteFile(out, []byte(doc), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "soft-reset-matrix: write %s: %v\n", out, err)
			os.Exit(1)
		}
	}
	if _, err := io.WriteString(os.Stdout, doc); err != nil {
		fmt.Fprintf(os.Stderr, "soft-reset-matrix: %v\n", err)
		os.Exit(1)
	}
}

type arrayFlags []string

func (a *arrayFlags) String() string {
	return strings.Join(*a, ",")
}

func (a *arrayFlags) Set(value string) error {
	*a = append(*a, value)
	return nil
}

func concernsForProfile(profile string) ([]concern, error) {
	concerns := profiles[profile]
	if concerns == nil {
		return nil, fmt.Errorf("unknown profile %q", profile)
	}
	return concerns, nil
}

func parseFindings(concerns []concern, values []string) (map[string]finding, error) {
	valid := make(map[string]bool)
	for _, c := range concerns {
		valid[c.ID] = true
	}
	out := make(map[string]finding)
	for _, value := range values {
		id, rest, ok := strings.Cut(value, "=")
		if !ok {
			return nil, fmt.Errorf("parse result %q: missing =", value)
		}
		id = strings.TrimSpace(id)
		if !valid[id] {
			return nil, fmt.Errorf("parse result %q: unknown concern %q", value, id)
		}
		status, note, _ := strings.Cut(rest, ":")
		status = strings.TrimSpace(strings.ToLower(status))
		if status != "pass" && status != "fail" && status != "limit" && status != "pending" {
			return nil, fmt.Errorf("parse result %q: status must be pass, fail, limit, or pending", value)
		}
		out[id] = finding{Status: status, Note: strings.TrimSpace(note)}
	}
	return out, nil
}

func renderMatrix(profile string, concerns []concern, vm, host string, findings map[string]finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", profileTitle(profile))
	fmt.Fprintf(&b, "- Date: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Profile: `%s`\n", profile)
	if host != "" {
		fmt.Fprintf(&b, "- Host: %s\n", host)
	}
	if vm != "" {
		fmt.Fprintf(&b, "- VM: `%s`\n", vm)
	}
	fmt.Fprintf(&b, "\n| Concern | Status | Question | Probe | Note |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|\n")
	for _, c := range concerns {
		f := findings[c.ID]
		status := f.Status
		if status == "" {
			status = "pending"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			c.Name,
			status,
			escape(c.Question),
			escape(c.Probe),
			escape(f.Note),
		)
	}
	fmt.Fprintf(&b, "\n## Summary\n\n")
	counts := countStatuses(concerns, findings)
	fmt.Fprintf(&b, "- Pass: %d\n", counts["pass"])
	fmt.Fprintf(&b, "- Fail: %d\n", counts["fail"])
	fmt.Fprintf(&b, "- Limit: %d\n", counts["limit"])
	fmt.Fprintf(&b, "- Pending: %d\n", counts["pending"])
	if profile == "soft-reset" {
		fmt.Fprintf(&b, "\nIf three or more concerns are `fail` or hard `limit`, revise the eval-runner soft-reset positioning before publishing throughput claims.\n")
	} else {
		fmt.Fprintf(&b, "\nIf any concern is `fail` or hard `limit`, document the mitigation before relying on cove host-boundary claims.\n")
	}
	return b.String()
}

func profileTitle(profile string) string {
	if profile == "boundary" {
		return "cove boundary isolation matrix"
	}
	return "cove soft-reset isolation matrix"
}

func countStatuses(concerns []concern, findings map[string]finding) map[string]int {
	out := map[string]int{"pass": 0, "fail": 0, "limit": 0, "pending": 0}
	for _, c := range concerns {
		status := findings[c.ID].Status
		if status == "" {
			status = "pending"
		}
		out[status]++
	}
	return out
}

func escape(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func concernIDs(concerns []concern) []string {
	ids := make([]string, 0, len(concerns))
	for _, c := range concerns {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}
