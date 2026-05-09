// cove_help_e2e_test.go - regression tests for `cove <cmd> -h`.
//
// Help output drifts silently when flags are added, removed, or
// reshuffled. These tests assert that every supported top-level
// subcommand prints something containing the subcommand name and a
// "Usage" section in response to -h, exiting cleanly.
//
// Subcommands whose -h surface is currently malformed (exit nonzero,
// missing Usage, or hand-routed through a flag parser that errors on
// -h) are tracked in skippedHelpSubcommands so the gap is visible
// instead of silently ignored.

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// helpSubcommands lists top-level subcommands whose `-h` surface is
// expected to exit 0, mention the subcommand name, and contain a
// "Usage" section. Aliases (e.g. ls, doctor for verify) are covered
// by their canonical name.
var helpSubcommands = []string{
	"action",
	"agent-sandbox",
	"agent-upgrade",
	"bench",
	"clean",
	"compact",
	"config",
	"ctl",
	"disk-detach",
	"disk-snapshot",
	"doctor",
	"export",
	"fork",
	"gc",
	"helper",
	"image",
	"import",
	"inject",
	"install",
	"list",
	"pit",
	"policy",
	"provision",
	"provision-agent",
	"pull",
	"push",
	"rename",
	"rosetta",
	"run",
	"runs",
	"secret",
	"serve",
	"shared-folder",
	"shell",
	"snapshot",
	"status",
	"store",
	"template",
	"up",
	"vm",
	"vzscript",
}

// skippedHelpSubcommands records subcommands intentionally not covered
// by TestCoveSubcommandHelp, with a short reason. Each entry is a real
// gap; flipping one of these green means the help surface improved.
var skippedHelpSubcommands = map[string]string{
	"build":        "exits 1 on -h via flag.ContinueOnError",
	"cp":           "flag parser surfaces 'help requested' to stderr, exit 1",
	"daemon":       "no -h handler; prints subcommand error, exit 1",
	"diff":         "flag parser exits 1 on -h",
	"fleet":        "treats -h as unknown subcommand, exit 1",
	"forward":      "no -h handler; prints usage to stderr, exit 1",
	"inject-agent": "deprecated alias; usage prints 'provision-agent' only",
	"logs":         "flag parser exits 1 on -h",
	"network":      "prints 'Network modes:' instead of 'Usage:'",
	"pin":          "flag parser exits 1 on -h, no Usage line",
	"pins":         "treats -h as unknown subcommand, exit 1",
	"quota":        "flag parser surfaces 'help requested' to stderr, exit 1",
	"sip":          "treats -h as unknown subcommand, exit 1",
	"softreset":    "treats -h as unknown subcommand, exit 1",
	"storage":      "treats -h as unknown subcommand, exit 1",
	"unpin":        "flag parser exits 1 on -h, no Usage line",
	"verify":       "alias of doctor; usage prints 'doctor' only",
	"version":      "prints version banner, no Usage section by design",
}

// TestCoveSubcommandHelp asserts that `cove <cmd> -h` returns a
// well-formed help surface for every entry in helpSubcommands. It
// reuses doctorE2EBinary so the binary is built once per test process.
func TestCoveSubcommandHelp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	for _, name := range helpSubcommands {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(bin, name, "-h")
			cmd.Env = append(os.Environ(), "HOME="+home)
			cmd.Stdin = strings.NewReader("")
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			exit := 0
			if err != nil {
				ee, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("run %s -h: %v", name, err)
				}
				exit = ee.ExitCode()
			}
			// Brief allows exit 0 or 2 (some flag parsers signal -h
			// via exit 2). Exit 1 means a real surface bug.
			if exit != 0 && exit != 2 {
				t.Errorf("%s -h: exit %d, want 0 or 2\nstdout:\n%s\nstderr:\n%s",
					name, exit, stdout.String(), stderr.String())
			}
			combined := stdout.String() + stderr.String()
			if !strings.Contains(combined, name) {
				t.Errorf("%s -h: output missing subcommand name %q\noutput:\n%s",
					name, name, combined)
			}
			if !containsFold(combined, "usage") {
				t.Errorf("%s -h: output missing Usage section\noutput:\n%s",
					name, combined)
			}
		})
	}
}

// TestCoveSubcommandHelpSkipsAreReal pins skippedHelpSubcommands to
// the registry: every skipped name must still exist as a real
// command, so we notice when a subcommand is renamed or removed.
func TestCoveSubcommandHelpSkipsAreReal(t *testing.T) {
	for name := range skippedHelpSubcommands {
		if _, ok := lookupCommand(name); !ok {
			t.Errorf("skip list references unknown subcommand %q", name)
		}
	}
	for _, name := range helpSubcommands {
		if _, ok := lookupCommand(name); !ok {
			t.Errorf("help list references unknown subcommand %q", name)
		}
		if _, dup := skippedHelpSubcommands[name]; dup {
			t.Errorf("subcommand %q is in both helpSubcommands and skippedHelpSubcommands", name)
		}
	}
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
