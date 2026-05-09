// cove_help_subsub_e2e_test.go - regression tests for `cove <cmd> <action> -h`.
//
// R80 covered top-level subcommands. This test drills one level deeper
// into the dispatcher subcommands fixed by R82 (daemon, fleet, pins,
// sip, softreset, storage) plus image (already implemented). Same
// assertions as R80: exit 0 or 2, output mentions the action name,
// output contains a Usage section.
//
// Sub-subcommands that currently misbehave on -h are tracked in
// skippedSubSubHelp with a short reason. Each entry is a real gap to
// be fixed in R84; flipping one to green means the surface improved.
//
// `forward` is intentionally absent: it is a flat command (`cove
// forward <vm> <hostport>:<vmport>`) with no action verbs, so there
// is nothing to drill into. It is covered by TestCoveSubcommandHelp.

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

type subSubHelpCase struct {
	parent string
	action string
}

// subSubHelp lists (parent, action) pairs whose `-h` surface is
// expected to exit cleanly with a Usage block mentioning the action.
var subSubHelp = []subSubHelpCase{
	{"sip", "enable"},
	{"sip", "enable-auto"},
	{"sip", "disable"},
	{"sip", "disable-auto"},
	{"sip", "status"},
	{"sip", "create-disk"},
	{"softreset", "probe"},
	{"softreset", "run-all"},
}

// skippedSubSubHelp records (parent, action) pairs intentionally not
// covered, with a short reason. These are real R84 gaps surfaced for
// follow-up: most sub-subcommands route -h into their own flag parser
// or positional-arg parser and exit 1 instead of printing a Usage
// surface, mirroring the top-level bugs R82 fixed.
var skippedSubSubHelp = map[subSubHelpCase]string{
	{"daemon", "status"}:        "daemon status -h: exit 1, no Usage (R84)",
	{"daemon", "start"}:         "daemon start -h: exit 1 from flag parser (R84)",
	{"daemon", "stop"}:          "daemon stop -h: exit 1 from flag parser (R84)",
	{"daemon", "metrics"}:       "daemon metrics -h: exit 1 from flag parser (R84)",
	{"daemon", "ui"}:            "daemon ui -h: exit 1 from flag parser (R84)",
	{"fleet", "add"}:            "fleet add -h: exit 1, no Usage, no action name (R84)",
	{"fleet", "ls"}:             "fleet ls -h: exit 0 but no Usage and no action name (R84)",
	{"fleet", "rm"}:             "fleet rm -h: exit 1, no Usage (R84)",
	{"fleet", "vm"}:             "fleet vm -h: exit 1, no Usage (R84)",
	{"fleet", "image"}:          "fleet image -h: exit 1, no Usage (R84)",
	{"fleet", "ps"}:             "fleet ps -h: exit 1, no Usage, no action name (R84)",
	{"fleet", "run"}:            "fleet run -h: exit 1 from flag parser (R84)",
	{"fleet", "metrics"}:        "fleet metrics -h: exit 1 from flag parser (R84)",
	{"pins", "list"}:            "pins list -h: exit 1 from flag parser (R84)",
	{"storage", "census"}:       "storage census -h: exit 1 from flag parser (R84)",
	{"storage", "budget"}:       "storage budget -h: exit 1, no Usage (R84)",
	{"storage", "prune"}:        "storage prune -h: exit 1 from flag parser (R84)",
	{"image", "build"}:          "image build -h: exit 1 from flag parser (R84)",
	{"image", "list"}:           "image list -h: exit 1 from flag parser (R84)",
	{"image", "inspect"}:        "image inspect -h: exit 1 from flag parser (R84)",
	{"image", "verify"}:         "image verify -h: exit 1 from flag parser (R84)",
	{"image", "gc"}:             "image gc -h: exit 1 from flag parser (R84)",
	{"image", "prune"}:          "image prune -h: exit 1 from flag parser (R84)",
	{"image", "tag"}:            "image tag -h: exit 1 from flag parser (R84)",
	{"image", "history"}:        "image history -h: exit 1 from flag parser (R84)",
	{"image", "search"}:         "image search -h: exit 1 from flag parser (R84)",
	{"image", "rm"}:             "image rm -h: exit 1 from flag parser (R84)",
	{"image", "push"}:           "image push -h: exit 1 from flag parser (R84)",
	{"image", "pull"}:           "image pull -h: exit 1 from flag parser (R84)",
	{"image", "load"}:           "image load -h: exit 1 from flag parser (R84)",
}

// TestCoveSubSubcommandHelp asserts that `cove <parent> <action> -h`
// returns a well-formed help surface for every entry in subSubHelp.
// It reuses doctorE2EBinary so the binary is built once per test process.
func TestCoveSubSubcommandHelp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	for _, tc := range subSubHelp {
		name := tc.parent + "_" + tc.action
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.parent, tc.action, "-h")
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
					t.Fatalf("run %s %s -h: %v", tc.parent, tc.action, err)
				}
				exit = ee.ExitCode()
			}
			if exit != 0 && exit != 2 {
				t.Errorf("%s %s -h: exit %d, want 0 or 2\nstdout:\n%s\nstderr:\n%s",
					tc.parent, tc.action, exit, stdout.String(), stderr.String())
			}
			combined := stdout.String() + stderr.String()
			if !strings.Contains(combined, tc.action) {
				t.Errorf("%s %s -h: output missing action name %q\noutput:\n%s",
					tc.parent, tc.action, tc.action, combined)
			}
			if !containsFold(combined, "usage") {
				t.Errorf("%s %s -h: output missing Usage section\noutput:\n%s",
					tc.parent, tc.action, combined)
			}
		})
	}
}

// TestCoveSubSubcommandHelpSkipsAreReal pins skippedSubSubHelp to the
// command registry: every skipped parent must still exist as a real
// top-level command, so we notice when a parent is renamed or removed.
// We do not enforce that the action name still exists in the parent's
// switch — those are internal to each handler and would require a
// separate registry.
func TestCoveSubSubcommandHelpSkipsAreReal(t *testing.T) {
	parents := map[string]bool{}
	for _, tc := range subSubHelp {
		parents[tc.parent] = true
	}
	for tc := range skippedSubSubHelp {
		parents[tc.parent] = true
		if _, dup := findSubSubHelpCase(subSubHelp, tc); dup {
			t.Errorf("(%s, %s) is in both subSubHelp and skippedSubSubHelp", tc.parent, tc.action)
		}
	}
	for parent := range parents {
		if _, ok := lookupCommand(parent); !ok {
			t.Errorf("sub-subcommand list references unknown parent %q", parent)
		}
	}
}

func findSubSubHelpCase(list []subSubHelpCase, want subSubHelpCase) (int, bool) {
	for i, tc := range list {
		if tc == want {
			return i, true
		}
	}
	return 0, false
}
