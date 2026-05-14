// ctl_ready.go - "ctl ready" subcommand: pluggable readiness probes.
//
// agent-ping answers whether the guest agent process is alive. Orchestrators
// often need to know whether the *environment* is ready: Xcode CLI tools, Go,
// Homebrew, etc. The ready command runs a set of named checks via the agent
// and reports per-check pass/fail.
//
// Checks are implemented host-side: each check is one agent-exec RPC against
// a small command (typically "which <tool>" or a tool-specific "version"
// invocation). The set of built-in checks is defined in readyChecks below;
// any name not in the map is treated as a generic "which <name>" probe so
// callers can pass arbitrary tool names without code changes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// readyExitOK, readyExitFailed, and readyExitUnreachable are the documented
// exit codes for "ctl ready". They mirror common health-check conventions.
const (
	readyExitOK          = 0
	readyExitFailed      = 1
	readyExitUnreachable = 2
)

// readyCheck describes a single readiness probe.
//
// Args is the command run inside the guest via agent-exec. A check passes
// when the command exits 0. Detail is taken from stdout (preferred) or
// stderr so the caller sees something useful (e.g. "go version go1.22.0").
type readyCheck struct {
	Name string
	Args []string
}

// readyChecks is the registry of built-in checks. Names are stable; new
// checks may be added freely. Custom names not in this map fall back to
// "which <name>" via genericReadyCheck.
var readyChecks = map[string]readyCheck{
	"agent-ping": {Name: "agent-ping", Args: []string{"true"}},
	"can-exec":   {Name: "can-exec", Args: []string{"echo", "hi"}},
	"xcode-cli":  {Name: "xcode-cli", Args: []string{"xcode-select", "-p"}},
	"go":         {Name: "go", Args: []string{"go", "version"}},
	"homebrew":   {Name: "homebrew", Args: []string{"which", "brew"}},
	"node":       {Name: "node", Args: []string{"which", "node"}},
	"docker":     {Name: "docker", Args: []string{"which", "docker"}},
}

// genericReadyCheck returns a "which <name>" probe for an unknown check name.
func genericReadyCheck(name string) readyCheck {
	return readyCheck{Name: name, Args: []string{"which", name}}
}

// readyResult is the outcome of a single check. Detail is the first non-empty
// of stdout, stderr, or an exit-status string, trimmed.
type readyResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// readyReport is the machine-readable output of "ctl ready".
type readyReport struct {
	Agent  string        `json:"agent"`
	Mode   string        `json:"mode"`
	Checks []readyResult `json:"checks"`
}

// parseReadyArgs parses "ctl ready" flags. Recognized flags:
//
//	--require name1,name2  comma-separated list of checks; default agent-ping,can-exec
//	--json                 emit machine-readable JSON instead of text
//	--timeout duration     per-check timeout (default 10s)
//	--daemon               run checks via the root daemon agent (default: user session)
func parseReadyArgs(args []string) (names []string, asJSON bool, timeout time.Duration, useDaemon bool, err error) {
	fs := flag.NewFlagSet("ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	require := fs.String("require", "", "comma-separated list of checks to run")
	jsonOut := fs.Bool("json", false, "emit JSON output")
	to := fs.Duration("timeout", 10*time.Second, "per-check timeout")
	daemon := fs.Bool("daemon", false, "run checks via the root daemon agent")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			printReadyUsage(os.Stdout)
		}
		return nil, false, 0, false, err
	}
	if fs.NArg() != 0 {
		return nil, false, 0, false, fmt.Errorf("ready: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	names = parseRequireList(*require)
	if len(names) == 0 {
		names = []string{"agent-ping", "can-exec"}
	}
	return names, *jsonOut, *to, *daemon, nil
}

func printReadyUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove ctl ready [--require name1,name2] [--json] [--timeout D] [--daemon]

Run readiness checks through the guest agent. Exit codes are 0 when all checks
pass, 1 when at least one reachable-agent check fails, and 2 when the agent is
unreachable.`)
}

// parseRequireList splits "a,b,,c" into ["a","b","c"], trimming whitespace
// and dropping empty entries. Order is preserved; duplicates are kept so
// callers see what they asked for.
func parseRequireList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// readyExitCode maps an agent-reachable flag and per-check results to a process
// exit code: 0 all pass, 1 some failed, 2 agent unreachable.
func readyExitCode(agentOK bool, results []readyResult) int {
	if !agentOK {
		return readyExitUnreachable
	}
	for _, r := range results {
		if !r.OK {
			return readyExitFailed
		}
	}
	return readyExitOK
}

// resolveReadyCheck returns the check definition for a name, falling back to
// a generic "which <name>" probe if the name is not registered.
func resolveReadyCheck(name string) readyCheck {
	if c, ok := readyChecks[name]; ok {
		return c
	}
	return genericReadyCheck(name)
}

// ctlReady runs the named readiness checks and reports the result. It always
// calls os.Exit with one of readyExitOK, readyExitFailed, readyExitUnreachable.
func ctlReady(sock string, args []string) error {
	names, asJSON, timeout, useDaemon, err := parseReadyArgs(args)
	if err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Probe agent-ping once up front so we can distinguish "agent unreachable"
	// (exit 2) from "checks failed" (exit 1). agent-ping is cheap and idempotent.
	agentOK, agentDetail := probeAgentPing(sock, timeout)

	results := make([]readyResult, 0, len(names))
	if !agentOK {
		// Mark every requested check as unreachable so the caller sees what
		// would have run; do not actually dispatch RPCs.
		for _, n := range names {
			results = append(results, readyResult{Name: n, OK: false, Detail: agentDetail})
		}
	} else {
		for _, n := range names {
			if n == "agent-ping" {
				// Short-circuit the redundant exec; we already pinged.
				results = append(results, readyResult{Name: n, OK: true})
				continue
			}
			results = append(results, runReadyCheck(sock, resolveReadyCheck(n), timeout, useDaemon))
		}
	}

	report := readyReport{Agent: agentStatus(agentOK), Mode: readyMode(useDaemon), Checks: results}
	if asJSON {
		printReadyJSON(report)
	} else {
		printReadyText(report)
	}

	os.Exit(readyExitCode(agentOK, results))
	return nil // unreachable
}

// agentStatus stringifies the agent-reachable flag for the report.
func agentStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "unreachable"
}

func readyMode(useDaemon bool) string {
	if useDaemon {
		return "daemon-agent"
	}
	return "user-agent"
}

// probeAgentPing sends a single agent-ping. It returns (ok, detail). detail is
// only meaningful when ok is false.
func probeAgentPing(sock string, timeout time.Duration) (bool, string) {
	req := &controlpb.ControlRequest{
		Type:      "agent-ping",
		AuthToken: resolveControlTokenForSocket(sock),
	}
	resp, err := ctlSendRequest(sock, req, timeout, "agent-ping")
	if err != nil {
		return false, fmt.Sprintf("agent-ping: %v", err)
	}
	if !resp.Success {
		return false, fmt.Sprintf("agent-ping: %s", strings.TrimSpace(resp.Error))
	}
	return true, ""
}

// runReadyCheck executes a single check via agent-exec and returns the result.
// useDaemon selects the root daemon agent ("agent-exec") versus the user
// session agent ("agent-user-exec", default).
func runReadyCheck(sock string, c readyCheck, timeout time.Duration, useDaemon bool) readyResult {
	cmdType := "agent-user-exec"
	if useDaemon {
		cmdType = "agent-exec"
	}
	req := &controlpb.ControlRequest{
		Type:      cmdType,
		AuthToken: resolveControlTokenForSocket(sock),
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: readyExecArgs(c.Args, useDaemon)},
		},
	}
	resp, err := ctlSendRequest(sock, req, timeout, cmdType)
	if err != nil {
		return readyResult{Name: c.Name, OK: false, Detail: err.Error()}
	}
	if !resp.Success {
		return readyResult{Name: c.Name, OK: false, Detail: strings.TrimSpace(resp.Error)}
	}
	return readyResultFromResp(c.Name, resp)
}

// readyResultFromResp extracts the per-check verdict from an agent-exec response.
// Pass means exit code 0. Detail is stdout if non-empty, else stderr, else an
// "exit status N" string.
func readyResultFromResp(name string, resp *controlpb.ControlResponse) readyResult {
	exec := resp.GetAgentExecResult()
	if exec == nil {
		// Older agents may only set resp.Data; try to parse it.
		return readyResultFromData(name, resp.Data)
	}
	ok := exec.ExitCode == 0
	detail := pickReadyDetail(exec.Stdout, exec.Stderr, int(exec.ExitCode))
	return readyResult{Name: name, OK: ok, Detail: detail}
}

// readyResultFromData parses the legacy JSON Data field used by older agents.
func readyResultFromData(name, data string) readyResult {
	if data == "" {
		return readyResult{Name: name, OK: true}
	}
	var parsed struct {
		ExitCode int    `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return readyResult{Name: name, OK: false, Detail: fmt.Sprintf("parse response: %v", err)}
	}
	ok := parsed.ExitCode == 0
	return readyResult{Name: name, OK: ok, Detail: pickReadyDetail(parsed.Stdout, parsed.Stderr, parsed.ExitCode)}
}

// readyExecArgs prepares a readiness probe command for execution in the guest.
// User-session checks run through a login shell so PATH additions from
// ~/.zprofile are visible. Daemon checks keep direct exec semantics.
func readyExecArgs(args []string, useDaemon bool) []string {
	if useDaemon || len(args) == 0 {
		return append([]string(nil), args...)
	}
	return []string{"/bin/zsh", "-lc", shellQuoteArgs(args)}
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// pickReadyDetail returns the most useful single-line detail string for a check.
// First non-empty trimmed line of stdout, then stderr, then "exit status N"
// when the check failed.
func pickReadyDetail(stdout, stderr string, exitCode int) string {
	if line := firstLine(stdout); line != "" {
		return line
	}
	if line := firstLine(stderr); line != "" {
		return line
	}
	if exitCode != 0 {
		return fmt.Sprintf("exit status %d", exitCode)
	}
	return ""
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func printReadyText(r readyReport) {
	fmt.Printf("daemon agent: %s\n", r.Agent)
	if r.Mode == "daemon-agent" {
		fmt.Println("checks: daemon agent")
	} else {
		fmt.Println("checks: user agent (requires a logged-in guest session)")
	}
	if len(r.Checks) == 0 {
		return
	}
	// Stable column width based on longest name.
	w := 0
	for _, c := range r.Checks {
		if n := len(c.Name); n > w {
			w = n
		}
	}
	for _, c := range r.Checks {
		mark := "FAIL"
		if c.OK {
			mark = "OK  "
		}
		if c.Detail != "" {
			fmt.Printf("  %s  %-*s  %s\n", mark, w, c.Name, c.Detail)
		} else {
			fmt.Printf("  %s  %-*s\n", mark, w, c.Name)
		}
	}
}

func printReadyJSON(r readyReport) {
	// Preserve the caller's --require order so consumers can correlate the
	// input list with the output list. Do not sort.
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		fmt.Fprintf(os.Stderr, "ready: encode json: %v\n", err)
	}
}
