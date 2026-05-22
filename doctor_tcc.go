package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

const tccProbeSeconds = 15

func verifyTCCFDAProbe(sock, explicitPath string, verbose bool) bool {
	fmt.Println("Full Disk Access probe:")

	path := strings.TrimSpace(explicitPath)
	if path == "" {
		var ok bool
		path, ok = discoverTCCProbePath(sock, verbose)
		if !ok {
			return false
		}
	}
	if path == "" {
		fmt.Println("  - skipped: no non-system /Volumes mount found")
		fmt.Println("    pass --tcc-path <guest-path> to probe a specific protected path")
		return true
	}

	result, err := agentUserExec(sock, []string{"/bin/sh", "-c", tccProbeScript(), "cove-tcc-probe", path}, 30*time.Second)
	if err != nil {
		fmt.Printf("  ! %s: error (%v)\n", path, err)
		printFDAHint()
		return false
	}
	if result.ExitCode == 0 {
		fmt.Printf("  + %s: readable via user agent\n", path)
		return true
	}

	stderr := strings.TrimSpace(result.Stderr)
	if stderr == "" {
		stderr = fmt.Sprintf("exit %d", result.ExitCode)
	}
	fmt.Printf("  ! %s: not readable via user agent (%s)\n", path, stderr)
	if isENOENTStderr(stderr) {
		fmt.Printf("    path not found in guest: %s\n", path)
		fmt.Println("    pass --tcc-path <existing-guest-path> to probe a real path")
		return false
	}
	printFDAHint()
	return false
}

func verifyTCCAppleEventsProbe(sock string) bool {
	fmt.Println("Apple Events probe:")
	result, err := agentUserExec(sock, []string{"/bin/sh", "-c", tccAppleEventsProbeScript()}, 10*time.Second)
	if err != nil {
		fmt.Printf("  ! error checking Terminal automation grant: %v\n", err)
		printAppleEventsHint()
		return false
	}
	if result.ExitCode == 0 && strings.TrimSpace(result.Stdout) == "granted" {
		fmt.Println("  + Terminal automation already granted for vz-agent")
		return true
	}
	fmt.Println("  - Terminal automation is not pre-granted")
	fmt.Println("    cove streams vzscript terminal output to the host by default")
	printAppleEventsHint()
	return true
}

func isENOENTStderr(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no such file or directory")
}

func discoverTCCProbePath(sock string, verbose bool) (string, bool) {
	result, err := agentUserExec(sock, []string{"/bin/sh", "-c", tccVolumeDiscoveryScript()}, 10*time.Second)
	if err != nil {
		fmt.Printf("  ! user agent unavailable: %v\n", err)
		fmt.Println("    log into the guest GUI, wait for vz-agent-user, then rerun cove doctor")
		return "", false
	}
	if result.ExitCode != 0 {
		if verbose {
			fmt.Printf("  ! volume discovery failed: %s\n", strings.TrimSpace(result.Stderr))
		}
		return "", false
	}
	return firstOutputLine(result.Stdout), true
}

func agentUserExec(sock string, args []string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	req := &controlpb.ControlRequest{
		Type: "agent-user-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: args},
		},
	}
	resp, err := ctlSendRequest(sock, req, timeout, "agent-user-exec")
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, errors.New(resp.Error)
	}
	result := resp.GetAgentExecResult()
	if result == nil {
		return nil, fmt.Errorf("missing agent exec result")
	}
	return result, nil
}

func tccVolumeDiscoveryScript() string {
	return `for p in /Volumes/*; do
	[ -d "$p" ] || continue
	name=${p##*/}
	case "$name" in
		"Macintosh HD"|"Macintosh HD - Data"|"Preboot"|"Recovery"|"VM"|"VZRECOVERY") continue ;;
	esac
	printf '%s\n' "$p"
	exit 0
done`
}

func tccProbeScript() string {
	return fmt.Sprintf(`path=$1
out="${TMPDIR:-/tmp}/cove-tcc-probe.$$.out"
err="${TMPDIR:-/tmp}/cove-tcc-probe.$$.err"
timed="${TMPDIR:-/tmp}/cove-tcc-probe.$$.timeout"
/bin/ls -1 "$path" >"$out" 2>"$err" &
pid=$!
( sleep %d; if kill -0 "$pid" 2>/dev/null; then : >"$timed"; kill "$pid" 2>/dev/null; fi ) &
watcher=$!
wait "$pid"
rc=$?
kill "$watcher" 2>/dev/null
wait "$watcher" 2>/dev/null
if [ -f "$timed" ]; then
	cat "$err" >&2 2>/dev/null
	rm -f "$out" "$err" "$timed"
	echo "timed out waiting for Full Disk Access approval" >&2
	exit 124
fi
cat "$out" 2>/dev/null
cat "$err" >&2 2>/dev/null
rm -f "$out" "$err" "$timed"
exit "$rc"`, tccProbeSeconds)
}

func tccAppleEventsProbeScript() string {
	query := `SELECT CASE WHEN EXISTS (
  SELECT 1 FROM access
  WHERE service='kTCCServiceAppleEvents'
    AND auth_value=2
    AND (
      client='/usr/local/bin/vz-agent'
      OR client LIKE '%com.github.tmc.vz-macos.vz-agent%'
    )
    AND (
      indirect_object_identifier='com.apple.Terminal'
      OR indirect_object_identifier LIKE '%Terminal%'
      OR indirect_object_identifier IS NULL
    )
) THEN 'granted' ELSE 'missing' END FROM access LIMIT 1;`
	return fmt.Sprintf(`dbs="/Library/Application Support/com.apple.TCC/TCC.db"
console=$(stat -f %%Su /dev/console 2>/dev/null || true)
if [ -n "$console" ] && [ "$console" != root ]; then
	home=$(dscl . -read /Users/"$console" NFSHomeDirectory 2>/dev/null | awk '{print $2}')
	if [ -n "$home" ]; then
		dbs="$dbs:$home/Library/Application Support/com.apple.TCC/TCC.db"
	fi
fi
IFS=:
for db in $dbs; do
	[ -r "$db" ] || continue
	out=$(sqlite3 "$db" %s 2>/dev/null || true)
	if [ "$out" = granted ]; then
		echo granted
		exit 0
	fi
done
echo missing
exit 1
`, shellEscape(query))
}

func firstOutputLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func printFDAHint() {
	fmt.Println("    grant Full Disk Access inside the guest:")
	fmt.Println("      System Settings -> Privacy & Security -> Full Disk Access")
	fmt.Println("      add /usr/local/bin/vz-agent, approve the prompt, then rerun cove doctor")
}

func printAppleEventsHint() {
	fmt.Println("    explicit guest Terminal mode may need Apple Events approval:")
	fmt.Println("      System Settings -> Privacy & Security -> Automation")
	fmt.Println("      allow vz-agent to control Terminal, or keep using host-streamed output")
	fmt.Println("      reset stale denials with: tccutil reset AppleEvents")
}
