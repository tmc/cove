// tcc_preauth.go - Host TCC Apple Events pre-flight subcommand.
//
// `cove doctor tcc-preauth` triggers the macOS TCC consent dialogs
// for the Apple Events services cove uses on the host, so the
// dialogs appear at a predictable moment instead of in the middle
// of a long `cove up` first-boot. The decisions are recorded in
// ~/.vz/runtime/tcc.json so subsequent invocations skip already
// pre-flighted services.
//
// Each service is described by a tccPreAuthService: an id used as the
// state-file key, a one-line description, and a dummy AppleScript
// snippet whose only job is to provoke the kTCCServiceAppleEvents
// dialog for the named target application.

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type tccPreAuthService struct {
	ID     string
	Desc   string
	Target string
	Script string
}

// hostTCCServices is the set of Apple Events targets the cove host
// binary actually drives via osascript. Update this list when adding
// a new osascript call site.
var hostTCCServices = []tccPreAuthService{
	{
		ID:     "system_events",
		Desc:   "System Events  - password and confirmation dialogs",
		Target: "System Events",
		// A no-op tell that does nothing observable to the user but
		// triggers the AE consent prompt.
		Script: `tell application "System Events" to get name`,
	},
	{
		ID:     "utm",
		Desc:   "UTM            - discover existing UTM VMs",
		Target: "UTM",
		Script: `tell application "System Events" to (name of processes) contains "UTM"`,
	},
}

// runPreAuth is the entry point for `cove doctor tcc-preauth`.
func runPreAuth(args []string) error {
	fs := flag.NewFlagSet("doctor tcc-preauth", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "Re-run preflight even for services already recorded as granted")
	reset := fs.Bool("reset", false, "Delete the saved preauth state and exit")
	yes := fs.Bool("y", false, "Run all preflight checks without prompting")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cove doctor tcc-preauth [options]

Pre-flight the macOS Apple Events consent dialogs that the cove host
binary triggers via osascript. Each service shows a one-time TCC
dialog the first time it is invoked. Pre-flighting them now lets you
respond at a predictable time instead of mid-workflow.

State is recorded in ~/.vz/runtime/tcc.json. Run with --reset to
forget all decisions and start over. Reset with the system tool too:
  tccutil reset AppleEvents com.github.tmc.cove

Options:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	statePath, err := DefaultTCCStatePath()
	if err != nil {
		return err
	}

	if *reset {
		if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove tcc state: %w", err)
		}
		fmt.Println("tcc preauth state cleared")
		return nil
	}

	state, err := LoadTCCState(statePath)
	if err != nil {
		return err
	}

	return preAuthRun(state, statePath, hostTCCServices, *force, *yes, os.Stdin, os.Stdout, runOSAScript)
}

// preAuthRun is the testable core. The osascript runner is injected
// so unit tests can simulate granted/denied results without firing
// real TCC dialogs.
func preAuthRun(state *TCCState, statePath string, services []tccPreAuthService, force, yes bool, in io.Reader, out io.Writer, run osaScriptRunner) error {
	fmt.Fprintln(out, "=== cove host TCC pre-auth ===")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "cove uses macOS Apple Events for the following operations on the host:")
	fmt.Fprintln(out)
	pending := make([]tccPreAuthService, 0, len(services))
	for i, svc := range services {
		entry, ok := state.HostEntry(svc.ID)
		marker := "."
		status := "not preflighted"
		if ok {
			status = string(entry.Result)
			switch entry.Result {
			case TCCResultGranted:
				marker = "+"
			case TCCResultDenied:
				marker = "!"
			case TCCResultSkipped:
				marker = "-"
			}
		}
		fmt.Fprintf(out, "  %s %d. %s   [%s]\n", marker, i+1, svc.Desc, status)
		if force || !ok || entry.Result == TCCResultUnknown {
			pending = append(pending, svc)
		}
	}
	fmt.Fprintln(out)

	if len(pending) == 0 {
		fmt.Fprintln(out, "All services already preflighted. Use --force to re-run, --reset to clear.")
		return nil
	}

	if !yes {
		fmt.Fprintf(out, "Pre-flight %d service(s) now? [y/N] ", len(pending))
		reader := bufio.NewReader(in)
		line, _ := reader.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans != "y" && ans != "yes" {
			fmt.Fprintln(out, "skipped")
			return nil
		}
	}

	for _, svc := range pending {
		fmt.Fprintf(out, "  -> %s ... ", svc.Target)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		result := run(ctx, svc.Script)
		cancel()
		state.SetHostEntry(svc.ID, result, time.Now())
		fmt.Fprintln(out, result)
	}

	if err := SaveTCCState(statePath, state); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nstate saved to %s\n", statePath)
	return nil
}

// osaScriptRunner runs an AppleScript snippet and returns the
// observed TCC result. Real implementations exec osascript; tests
// substitute fakes.
type osaScriptRunner func(ctx context.Context, script string) TCCResult

// runOSAScript is the production runner. It interprets osascript exit
// codes and stderr to classify the outcome:
//   - exit 0:                                       granted
//   - stderr mentions "not authorized" / "1743":    denied
//   - any other failure:                            unknown (treat as
//     not-yet-decided so a re-run will retry)
func runOSAScript(ctx context.Context, script string) TCCResult {
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return TCCResultGranted
	}
	low := strings.ToLower(string(out))
	if strings.Contains(low, "not authorized") || strings.Contains(low, "-1743") || strings.Contains(low, "1743") {
		return TCCResultDenied
	}
	return TCCResultUnknown
}
