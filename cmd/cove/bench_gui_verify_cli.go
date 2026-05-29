package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/tmc/cove/internal/guibench"
)

// egressPolicyFor builds the cove network policy that enforces a task's egress
// lockdown during scoring (design 047 §8). A deny-all lockdown maps to the
// "offline" policy (the only policy that disables the guest's virtual network
// outright); an allowlisted task maps to a NAT policy carrying exactly the
// task's allowed domains. The returned NetworkPolicy satisfies
// guibench.EgressPolicy, so guibench.EgressLockdown.CheckPolicy can confirm the
// runner asked for — and got — the right policy before the agent runs.
//
// Gold references never appear here: this carries only the host names a task is
// permitted to reach, never a gold value, which stays host-side in the verifier.
func egressPolicyFor(lock guibench.EgressLockdown) NetworkPolicy {
	if lock.DenyAll() {
		// ParseNetworkPolicy("offline") cannot fail; build it directly so the
		// wiring has no error path the caller must thread.
		p, _ := ParseNetworkPolicy("offline")
		return p
	}
	return NetworkPolicy{
		Name:     "gui-task-allow",
		Mode:     NetworkModeNAT,
		Domains:  append([]string(nil), lock.Allow...),
		Audit:    true,
		Limit:    "Virtualization.framework NAT does not expose per-connection host-side allow/deny hooks; this run uses NAT and records the intended per-task egress allowlist.",
		Enforced: false,
	}
}

// runBenchGUIManifest builds and prints a versioned corpus manifest: the corpus
// version, the verifier version, the cove commit, and the public/held-out task
// partition (design 047 §6, §9 slice 6).
func runBenchGUIManifest(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui manifest", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	corpus := fs.String("corpus", "", "task corpus directory to manifest")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui manifest: unexpected arguments: %v", fs.Args())
	}
	if *corpus == "" {
		return fmt.Errorf("bench gui manifest: -corpus is required")
	}
	tasks, err := guibench.Load(*corpus)
	if err != nil {
		return fmt.Errorf("bench gui manifest: %w", err)
	}
	manifest := guibench.BuildManifest(tasks, coveBuildCommit())
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("bench gui manifest: %w", err)
	}
	return manifest.Encode(env.Stdout)
}

// runBenchGUIVerifyBundle validates a submitted result bundle and stamps its
// tier verified or unverified (design 047 §11). A result is verified only when a
// maintainer (-maintainer) executed the run and the bundle's corpus and verifier
// versions match the manifest the corpus pins; a self-reported number is stamped
// unverified. The stamp is written back into the bundle.
func runBenchGUIVerifyBundle(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui verify-bundle", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	corpus := fs.String("corpus", "", "task corpus the result was scored against (pins the manifest)")
	maintainer := fs.Bool("maintainer", false, "this was a maintainer-executed run (required for a verified stamp)")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("bench gui verify-bundle: want exactly one bundle directory")
	}
	if *corpus == "" {
		return fmt.Errorf("bench gui verify-bundle: -corpus is required")
	}
	dir := fs.Arg(0)
	tasks, err := guibench.Load(*corpus)
	if err != nil {
		return fmt.Errorf("bench gui verify-bundle: %w", err)
	}
	manifest := guibench.BuildManifest(tasks, coveBuildCommit())
	now := time.Now().UTC().Format(time.RFC3339)
	stamped, err := guibench.VerifyBundle(dir, manifest, *maintainer, now)
	if err != nil {
		return fmt.Errorf("bench gui verify-bundle: %w", err)
	}
	fmt.Fprintf(env.Stdout, "bundle %s: tier=%s provider=%s model=%s overall=%.3f\n",
		dir, stamped.Tier, stamped.Provider, stamped.Model, stamped.Overall())
	if stamped.Tier != guibench.TierVerified {
		fmt.Fprintln(env.Stdout, "note: unverified — a verified stamp requires a maintainer-executed run with matching corpus+verifier versions")
	}
	return nil
}

// coveBuildCommit returns the cove build commit for recording in a manifest.
func coveBuildCommit() string {
	return resolvedVersion().Commit
}
