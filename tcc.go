// tcc.go - macOS TCC (Transparency, Consent, and Control) database management.
//
// The TCC database controls which applications have access to protected
// resources like Full Disk Access, Automation (Apple Events), and
// Accessibility. When SIP is disabled, the TCC database at
// /Library/Application Support/com.apple.TCC/TCC.db can be modified
// directly with sqlite3.
//
// Two approaches are provided:
//
//  1. Runtime grant via guest agent: Connects to the running VM's agent
//     and executes sqlite3 commands to insert TCC entries.
//
//  2. Disk injection: Mounts the VM disk image and writes the TCC.db
//     entries directly (for pre-boot configuration).
//
// Both require SIP to be disabled. With SIP enabled, the TCC database
// is protected by system integrity and modifications will be rejected.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TCC service identifiers.
const (
	tccServiceFDA           = "kTCCServiceSystemPolicyAllFiles"
	tccServiceAppleEvents   = "kTCCServiceAppleEvents"
	tccServiceAccessibility = "kTCCServiceAccessibility"
)

// defaultFDAClients are paths that commonly need FDA for VM automation.
var defaultFDAClients = []string{
	"/usr/sbin/sshd",
	"/usr/sbin/systemsetup",
	"/usr/libexec/sshd-keygen-wrapper",
	"/usr/bin/osascript",
}

// defaultAutomationGrants are Apple Events grants needed for VM automation.
// Each entry is {client, target_bundle_id}.
var defaultAutomationGrants = [][2]string{
	{"/usr/local/bin/vz-agent", "com.apple.Terminal"},
	{"/usr/local/bin/vz-agent", "com.apple.systemevents"},
	{"/usr/bin/osascript", "com.apple.Terminal"},
	{"/usr/bin/osascript", "com.apple.systemevents"},
}

// tccDBPath is the system-wide TCC database path inside the guest.
const tccDBPath = "/Library/Application Support/com.apple.TCC/TCC.db"

// tccInsertSQL generates a SQL INSERT for granting a TCC service to a client path.
func tccInsertSQL(service, clientPath string) string {
	return fmt.Sprintf(`INSERT OR REPLACE INTO access (
    service, client, client_type, auth_value, auth_reason, auth_version,
    csreq, policy_id, indirect_object_identifier_type, indirect_object_identifier,
    indirect_object_code_identity, flags, last_modified, boot_uuid, last_reminded
) VALUES (
    '%s',
    '%s',
    1,
    2,
    4,
    1,
    NULL, NULL, NULL, 'UNUSED', NULL, 0,
    CAST(strftime('%%s','now') AS INTEGER), '', 0
);`, service, clientPath)
}

// tccInsertAutomationSQL generates a SQL INSERT for an Apple Events (Automation) grant.
// This allows clientPath to send Apple Events to the app identified by targetBundleID.
func tccInsertAutomationSQL(clientPath, targetBundleID string) string {
	return fmt.Sprintf(`INSERT OR REPLACE INTO access (
    service, client, client_type, auth_value, auth_reason, auth_version,
    csreq, policy_id, indirect_object_identifier_type, indirect_object_identifier,
    indirect_object_code_identity, flags, last_modified, boot_uuid, last_reminded
) VALUES (
    '%s',
    '%s',
    1,
    2,
    4,
    1,
    NULL, NULL, 0, '%s', NULL, 0,
    CAST(strftime('%%s','now') AS INTEGER), '', 0
);`, tccServiceAppleEvents, clientPath, targetBundleID)
}

// handleTCCCommand dispatches TCC management subcommands.
func handleTCCCommand(args []string) error {
	if len(args) == 0 {
		fmt.Println(tccUsage)
		return nil
	}

	switch args[0] {
	case "grant":
		return handleTCCGrant(args[1:])
	case "status":
		return handleTCCStatus(args[1:])
	case "query":
		return handleTCCQuery(args[1:])
	case "help":
		fmt.Println(tccUsage)
		return nil
	default:
		return fmt.Errorf("unknown tcc command: %s\n\n%s", args[0], tccUsage)
	}
}

// handleTCCGrant grants TCC permissions to specified clients.
func handleTCCGrant(args []string) error {
	fs := flag.NewFlagSet("tcc grant", flag.ExitOnError)
	client := fs.String("client", "", "Client path to grant permissions (e.g., /usr/sbin/sshd)")
	service := fs.String("service", "fda", "TCC service: fda, automation, accessibility")
	target := fs.String("target", "", "Target bundle ID for automation grants (e.g., com.apple.Terminal)")
	all := fs.Bool("all", false, "Grant FDA + Automation + Accessibility to all default clients")
	agentBinPath := fs.String("agent-path", "", "Also grant to the vz-agent binary at this path")
	dryRun := fs.Bool("dry-run", false, "Print SQL without executing")
	injectMode := fs.Bool("inject", false, "Inject into disk image instead of using agent (VM must be stopped)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos tcc grant [options]

Grant TCC permissions (FDA, Automation, Accessibility) inside the VM.
Requires SIP to be disabled first (see: vz-macos sip help).

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Services:
  fda            Full Disk Access (kTCCServiceSystemPolicyAllFiles)
  automation     Apple Events / Automation (kTCCServiceAppleEvents)
  accessibility  Accessibility (kTCCServiceAccessibility)

Examples:
  # Grant all default permissions (FDA + Automation + Accessibility)
  vz-macos tcc grant -all

  # Grant FDA to a specific binary
  vz-macos tcc grant -client /usr/sbin/sshd

  # Grant automation: vz-agent → Terminal.app
  vz-macos tcc grant -service automation -client /usr/local/bin/vz-agent -target com.apple.Terminal

  # Grant accessibility to vz-agent
  vz-macos tcc grant -service accessibility -client /usr/local/bin/vz-agent

  # Preview SQL without executing
  vz-macos tcc grant -all -dry-run

  # Inject into disk image (VM must be stopped)
  vz-macos tcc grant -all -inject
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	var sqlStatements []string
	var grantDescriptions []string

	if *all {
		// FDA grants for default clients.
		for _, c := range defaultFDAClients {
			sqlStatements = append(sqlStatements, tccInsertSQL(tccServiceFDA, c))
			grantDescriptions = append(grantDescriptions, fmt.Sprintf("FDA: %s", c))
		}
		// Automation grants.
		for _, grant := range defaultAutomationGrants {
			sqlStatements = append(sqlStatements, tccInsertAutomationSQL(grant[0], grant[1]))
			grantDescriptions = append(grantDescriptions, fmt.Sprintf("Automation: %s → %s", grant[0], grant[1]))
		}
		// Accessibility for vz-agent.
		sqlStatements = append(sqlStatements, tccInsertSQL(tccServiceAccessibility, "/usr/local/bin/vz-agent"))
		grantDescriptions = append(grantDescriptions, "Accessibility: /usr/local/bin/vz-agent")
	}

	if *agentBinPath != "" {
		sqlStatements = append(sqlStatements, tccInsertSQL(tccServiceFDA, *agentBinPath))
		grantDescriptions = append(grantDescriptions, fmt.Sprintf("FDA: %s", *agentBinPath))
	}

	if *client != "" {
		svc := tccServiceForName(*service)
		if svc == "" {
			return fmt.Errorf("unknown service: %s (use fda, automation, or accessibility)", *service)
		}
		if svc == tccServiceAppleEvents {
			if *target == "" {
				return fmt.Errorf("automation grants require -target (e.g., -target com.apple.Terminal)")
			}
			sqlStatements = append(sqlStatements, tccInsertAutomationSQL(*client, *target))
			grantDescriptions = append(grantDescriptions, fmt.Sprintf("Automation: %s → %s", *client, *target))
		} else {
			sqlStatements = append(sqlStatements, tccInsertSQL(svc, *client))
			grantDescriptions = append(grantDescriptions, fmt.Sprintf("%s: %s", *service, *client))
		}
	}

	if len(sqlStatements) == 0 {
		return fmt.Errorf("specify -all, -client, or -agent-path")
	}

	combinedSQL := strings.Join(sqlStatements, "\n")

	if *dryRun {
		fmt.Println("=== Dry Run: SQL to execute ===")
		fmt.Println(combinedSQL)
		return nil
	}

	if *injectMode {
		return tccGrantViaDiskInject(grantDescriptions, combinedSQL)
	}

	return tccGrantViaAgent(grantDescriptions, combinedSQL)
}

// tccServiceForName maps short names to TCC service identifiers.
func tccServiceForName(name string) string {
	switch strings.ToLower(name) {
	case "fda", "full-disk-access":
		return tccServiceFDA
	case "automation", "apple-events":
		return tccServiceAppleEvents
	case "accessibility":
		return tccServiceAccessibility
	default:
		return ""
	}
}

// tccGrantViaAgent executes TCC grants via the guest agent.
func tccGrantViaAgent(grants []string, sql string) error {
	fmt.Println("=== Granting TCC Permissions via Agent ===")
	fmt.Println()

	sock := GetControlSocketPath()
	timeout := 30 * time.Second

	// Check SIP status first.
	fmt.Println("Checking SIP status...")
	sipResp, err := ctlSendCommand(sock, "agent-exec", map[string]interface{}{
		"args": []string{"csrutil", "status"},
	}, timeout)
	if err != nil {
		return fmt.Errorf("connect to VM: %w\n  Is the VM running with the agent?", err)
	}
	if !sipResp.Success {
		return fmt.Errorf("check SIP status: %s", sipResp.Error)
	}

	sipOutput := parseAgentExecOutput(sipResp.Data)
	if strings.Contains(sipOutput, "enabled") {
		fmt.Println()
		fmt.Println("WARNING: SIP appears to be enabled.")
		fmt.Println("TCC.db modifications require SIP to be disabled.")
		fmt.Println("See: vz-macos sip help")
		fmt.Println()
		fmt.Println("Proceeding anyway (modifications may be rejected)...")
		fmt.Println()
	} else {
		fmt.Println("SIP is disabled.")
	}

	// Execute the SQL via sqlite3.
	fmt.Println("Inserting TCC grants...")
	resp, err := ctlSendCommand(sock, "agent-exec", map[string]interface{}{
		"args": []string{"sqlite3", tccDBPath, sql},
	}, timeout)
	if err != nil {
		return fmt.Errorf("sqlite3 exec: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("sqlite3 exec: %s", resp.Error)
	}

	output := parseAgentExecOutput(resp.Data)
	if output != "" {
		fmt.Printf("sqlite3 output: %s\n", output)
	}

	fmt.Println()
	fmt.Println("=== TCC Grants Applied ===")
	for _, g := range grants {
		fmt.Printf("  %s\n", g)
	}
	fmt.Println()
	fmt.Println("Note: Changes take effect immediately for new process launches.")
	fmt.Println("Running processes may need to be restarted.")

	return nil
}

// agentExecResult holds parsed output from an agent-exec response.
type agentExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// parseAgentExecResult parses the JSON data from an agent-exec response.
func parseAgentExecResult(data string) agentExecResult {
	var r agentExecResult
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return agentExecResult{Stdout: data}
	}
	return r
}

// parseAgentExecOutput extracts stdout+stderr from an agent-exec response.
func parseAgentExecOutput(data string) string {
	r := parseAgentExecResult(data)
	return strings.TrimSpace(r.Stdout + r.Stderr)
}

// tccGrantViaDiskInject mounts the VM disk and injects TCC.db entries directly.
func tccGrantViaDiskInject(grants []string, sql string) error {
	fmt.Println("=== Granting TCC Permissions via Disk Injection ===")
	fmt.Println()

	diskImgPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskImgPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s\nRun 'vz-macos install' first", diskImgPath)
	}

	if err := checkDiskNotMounted(diskImgPath); err != nil {
		return err
	}

	mountPoint, device, _, err := attachAndMountDataVolume(diskImgPath)
	if err != nil {
		return fmt.Errorf("mount data volume: %w", err)
	}
	defer detachDisk(device)

	tccDir := filepath.Join(mountPoint, "Library", "Application Support", "com.apple.TCC")
	tccFile := filepath.Join(tccDir, "TCC.db")

	if _, err := os.Stat(tccFile); os.IsNotExist(err) {
		fmt.Println("TCC.db does not exist yet - creating it.")
		fmt.Println("Note: macOS creates TCC.db on first boot. If injecting before")
		fmt.Println("first boot, the database will be created by the system and our")
		fmt.Println("changes may be overwritten.")
		fmt.Println()

		if err := os.MkdirAll(tccDir, 0755); err != nil {
			return fmt.Errorf("create TCC directory: %w", err)
		}

		initSQL := tccCreateTableSQL + "\n" + sql
		out, err := exec.Command("sqlite3", tccFile, initSQL).CombinedOutput()
		if err != nil {
			return fmt.Errorf("initialize TCC.db: %w: %s", err, out)
		}
	} else {
		out, err := exec.Command("sqlite3", tccFile, sql).CombinedOutput()
		if err != nil {
			return fmt.Errorf("update TCC.db: %w: %s", err, out)
		}
	}

	fmt.Println()
	fmt.Println("=== TCC Grants Injected ===")
	for _, g := range grants {
		fmt.Printf("  %s\n", g)
	}
	fmt.Println()
	fmt.Println("WARNING: If injecting before first boot, macOS may recreate TCC.db")
	fmt.Println("and overwrite these entries. For best results, grant TCC permissions")
	fmt.Println("after the VM has booted at least once and SIP is disabled.")

	return nil
}

// handleTCCQuery queries the TCC database for entries matching a filter.
func handleTCCQuery(args []string) error {
	fs := flag.NewFlagSet("tcc query", flag.ExitOnError)
	client := fs.String("client", "", "Filter by client path (substring match)")
	service := fs.String("service", "", "Filter by service (fda, automation, accessibility)")
	all := fs.Bool("all", false, "Show all entries")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos tcc query [options]

Query the TCC database inside the VM. Requires SIP to be disabled.

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Show all TCC entries
  vz-macos tcc query -all

  # Show entries for vz-agent
  vz-macos tcc query -client vz-agent

  # Show automation (Apple Events) entries
  vz-macos tcc query -service automation
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *client == "" && *service == "" && !*all {
		fs.Usage()
		return fmt.Errorf("specify -all, -client, or -service")
	}

	var where []string
	if *client != "" {
		where = append(where, fmt.Sprintf("client LIKE '%%%s%%'", *client))
	}
	if *service != "" {
		svc := tccServiceForName(*service)
		if svc == "" {
			return fmt.Errorf("unknown service: %s (use fda, automation, or accessibility)", *service)
		}
		where = append(where, fmt.Sprintf("service='%s'", svc))
	}

	query := "SELECT service, client, auth_value, indirect_object_identifier FROM access"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY service, client;"

	sock := GetControlSocketPath()
	timeout := 30 * time.Second

	resp, err := ctlSendCommand(sock, "agent-exec", map[string]interface{}{
		"args": []string{"sqlite3", "-header", "-column", tccDBPath, query},
	}, timeout)
	if err != nil {
		return fmt.Errorf("connect to VM: %w\n  Is the VM running with the agent?", err)
	}
	if !resp.Success {
		return fmt.Errorf("query TCC.db: %s", resp.Error)
	}

	r := parseAgentExecResult(resp.Data)
	if r.ExitCode != 0 {
		errOutput := strings.TrimSpace(r.Stderr)
		if strings.Contains(errOutput, "authorization denied") {
			return fmt.Errorf("TCC.db is protected (SIP is enabled)\n  Disable SIP first: vz-macos sip disable")
		}
		return fmt.Errorf("sqlite3: %s", errOutput)
	}

	output := strings.TrimSpace(r.Stdout)
	if output == "" {
		fmt.Println("No matching TCC entries found.")
		return nil
	}

	fmt.Println(output)
	return nil
}

// handleTCCStatus shows TCC status for known clients via the guest agent.
func handleTCCStatus(_ []string) error {
	fmt.Println("=== TCC Status ===")
	fmt.Println()

	sock := GetControlSocketPath()
	timeout := 30 * time.Second

	// Check SIP status.
	sipResp, err := ctlSendCommand(sock, "agent-exec", map[string]interface{}{
		"args": []string{"csrutil", "status"},
	}, timeout)
	if err != nil {
		return fmt.Errorf("connect to VM: %w\n  Is the VM running with the agent?", err)
	}
	if sipResp.Success {
		fmt.Printf("SIP: %s\n", parseAgentExecOutput(sipResp.Data))
	}
	fmt.Println()

	// Query each service type.
	services := []struct {
		name    string
		service string
	}{
		{"Full Disk Access", tccServiceFDA},
		{"Automation (Apple Events)", tccServiceAppleEvents},
		{"Accessibility", tccServiceAccessibility},
	}

	for _, svc := range services {
		query := fmt.Sprintf("SELECT client, auth_value, indirect_object_identifier FROM access WHERE service='%s';", svc.service)
		tccResp, err := ctlSendCommand(sock, "agent-exec", map[string]interface{}{
			"args": []string{"sqlite3", tccDBPath, query},
		}, timeout)
		if err != nil {
			fmt.Printf("%s: could not query (agent unreachable)\n", svc.name)
			continue
		}
		if !tccResp.Success {
			fmt.Printf("%s: could not query: %s\n", svc.name, tccResp.Error)
			continue
		}

		r := parseAgentExecResult(tccResp.Data)
		if r.ExitCode != 0 {
			if strings.Contains(r.Stderr, "authorization denied") {
				fmt.Printf("%s: protected (SIP enabled)\n", svc.name)
			} else {
				fmt.Printf("%s: error: %s\n", svc.name, strings.TrimSpace(r.Stderr))
			}
			continue
		}

		output := strings.TrimSpace(r.Stdout)
		if output == "" {
			fmt.Printf("%s: no entries\n", svc.name)
			continue
		}

		fmt.Printf("%s:\n", svc.name)
		for _, line := range strings.Split(output, "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "|", 3)
			status := "denied"
			if len(parts) >= 2 && parts[1] == "2" {
				status = "allowed"
			}
			desc := parts[0]
			if len(parts) >= 3 && parts[2] != "" && parts[2] != "UNUSED" {
				desc = fmt.Sprintf("%s → %s", parts[0], parts[2])
			}
			fmt.Printf("  %s: %s\n", desc, status)
		}
		fmt.Println()
	}

	return nil
}

// tccCreateTableSQL creates the TCC access table if it doesn't exist.
const tccCreateTableSQL = `CREATE TABLE IF NOT EXISTS access (
    service TEXT NOT NULL,
    client TEXT NOT NULL,
    client_type INTEGER NOT NULL,
    auth_value INTEGER NOT NULL,
    auth_reason INTEGER NOT NULL,
    auth_version INTEGER NOT NULL,
    csreq BLOB,
    policy_id INTEGER,
    indirect_object_identifier_type INTEGER,
    indirect_object_identifier TEXT DEFAULT 'UNUSED',
    indirect_object_code_identity BLOB,
    flags INTEGER,
    last_modified INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)),
    pid INTEGER,
    pid_version INTEGER,
    boot_uuid TEXT DEFAULT '',
    last_reminded INTEGER DEFAULT 0,
    PRIMARY KEY (service, client, client_type)
);`

const tccUsage = `Usage: vz-macos tcc <command>

Commands:
  grant     Grant TCC permissions inside the VM
  status    Show TCC status for known services and clients
  query     Query the TCC database for specific entries
  help      Show this help

TCC (Transparency, Consent, and Control) manages access to:
  - Full Disk Access (FDA) - file system access for sshd, vz-agent, etc.
  - Automation (Apple Events) - inter-app control (vz-agent → Terminal.app)
  - Accessibility - UI automation and control

IMPORTANT: TCC grants via TCC.db require SIP to be disabled first.
See 'vz-macos sip help' for instructions.

Workflow:
  1. Disable SIP:    vz-macos sip disable
  2. Grant all:      vz-macos tcc grant -all
  3. Verify:         vz-macos tcc status
  4. Query:          vz-macos tcc query -client vz-agent`
