package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/guibench"
	"github.com/tmc/cove/internal/vmconfig"
)

// runBenchGUIImageCheck verifies a candidate base image carries exactly the TCC
// grants its corpus needs, before the image is saved (design 047 §5, §8, slice
// 3). The grants — Full Disk Access for Tier-B getters and Apple Events +
// Accessibility for Tier-C getters — are baked into the image, never done per
// run, so a fresh fork must already hold them or every Tier-B/C verifier reads
// denied/stale state and the benchmark reads as "agents fail at macOS" when the
// truth is "the image was under-granted".
//
// The operator boots one fork of the candidate image, leaves it at the desktop
// long enough for the user agent to come up, then runs this against that VM:
//
//	cove bench gui image-check -vm <running-fork> [-corpus <dir>] [-tcc-path <p>]
//
// With -corpus, the required grant level is derived from the corpus's getters
// ([guibench.MaxTier]); without it, all three grants are required (the safe
// default for a general-purpose base image). The command exits nonzero if any
// required grant is missing, so it can gate an image-save script.
func runBenchGUIImageCheck(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui image-check", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	vm := fs.String("vm", "", "name of a running fork of the candidate image to probe")
	corpus := fs.String("corpus", "", "task corpus whose getters set the required grant level (optional)")
	tccPath := fs.String("tcc-path", "", "guest path to probe for Full Disk Access (optional)")
	verbose := fs.Bool("verbose", false, "print probe diagnostics")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui image-check: unexpected arguments: %v", fs.Args())
	}
	if *vm == "" {
		return fmt.Errorf("bench gui image-check: -vm is required (boot a fork of the candidate image first)")
	}

	need, err := requiredTierForImageCheck(*corpus)
	if err != nil {
		return err
	}

	sock := GetControlSocketPathForVM(filepath.Join(vmconfig.BaseDir(), *vm))
	fmt.Fprintf(env.Stdout, "image-check %s: required grant level tier %s (%s)\n", *vm, need, need.Grant())

	missing := checkImageGrants(env.Stdout, sock, need, *tccPath, *verbose)
	if len(missing) > 0 {
		return fmt.Errorf("bench gui image-check: candidate image is missing %d required grant(s): %s — do NOT save this image",
			len(missing), strings.Join(missing, ", "))
	}
	fmt.Fprintln(env.Stdout, "image-check: all required grants present; image is safe to save")
	return nil
}

// requiredTierForImageCheck derives the grant level an image must carry. With a
// corpus, it is the corpus's MaxTier; without one, it defaults to TierC so a
// general-purpose base image is verified for every grant.
func requiredTierForImageCheck(corpus string) (guibench.Tier, error) {
	if corpus == "" {
		return guibench.TierC, nil
	}
	tasks, err := guibench.Load(corpus)
	if err != nil {
		return "", fmt.Errorf("bench gui image-check: %w", err)
	}
	return guibench.MaxTier(tasks), nil
}

// checkImageGrants runs the grant probes the required tier implies and returns
// the names of any missing grants. Tier B requires Full Disk Access; Tier C
// additionally requires Apple Events and Accessibility (design 047 §5). It
// prints a per-grant line to w so the operator sees what failed.
func checkImageGrants(w io.Writer, sock string, need guibench.Tier, tccPath string, verbose bool) []string {
	var missing []string
	if need >= guibench.TierB {
		if verifyTCCFDAProbe(sock, tccPath, verbose) {
			fmt.Fprintln(w, "  + Full Disk Access: granted")
		} else {
			missing = append(missing, "Full Disk Access")
		}
	}
	if need >= guibench.TierC {
		if probeStrictTCC(w, sock, "Apple Events", tccAppleEventsProbeScript()) {
			fmt.Fprintln(w, "  + Apple Events: granted")
		} else {
			missing = append(missing, "Apple Events")
		}
		if probeStrictTCC(w, sock, "Accessibility", tccAccessibilityProbeScript()) {
			fmt.Fprintln(w, "  + Accessibility: granted")
		} else {
			missing = append(missing, "Accessibility")
		}
	}
	return missing
}

// probeStrictTCC runs a TCC probe script that prints "granted" on success and
// reports a hard miss otherwise (unlike the doctor command's soft Apple Events
// check, which never fails). It returns true only when the grant is present.
func probeStrictTCC(w io.Writer, sock, label, script string) bool {
	result, err := agentUserExec(sock, []string{"/bin/sh", "-c", script}, 10*time.Second)
	if err != nil {
		fmt.Fprintf(w, "  ! %s: probe error (%v)\n", label, err)
		return false
	}
	if result.ExitCode == 0 && strings.TrimSpace(result.Stdout) == "granted" {
		return true
	}
	fmt.Fprintf(w, "  - %s: not granted in candidate image\n", label)
	return false
}

// tccAccessibilityProbeScript reports "granted" iff the vz-agent client holds an
// Accessibility (kTCCServiceAccessibility) grant in the system TCC database. It
// mirrors [tccAppleEventsProbeScript] but for the independent Accessibility TCC
// service (design 047 §5: AE and Accessibility are distinct from FDA).
func tccAccessibilityProbeScript() string {
	query := `SELECT CASE WHEN EXISTS (
  SELECT 1 FROM access
  WHERE service='kTCCServiceAccessibility'
    AND auth_value=2
    AND (
      client='/usr/local/bin/vz-agent'
      OR client LIKE '%com.tmc.cove.vz-agent%'
    )
) THEN 'granted' ELSE 'missing' END FROM access LIMIT 1;`
	return fmt.Sprintf(`db="/Library/Application Support/com.apple.TCC/TCC.db"
[ -r "$db" ] || { echo missing; exit 1; }
out=$(sqlite3 "$db" %s 2>/dev/null || true)
if [ "$out" = granted ]; then
	echo granted
	exit 0
fi
echo missing
exit 1
`, shellEscape(query))
}
