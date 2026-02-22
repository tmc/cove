// fda.go - Full Disk Access (FDA) management via TCC.db injection.
//
// macOS Transparency, Consent, and Control (TCC) database controls which
// applications have access to protected resources like Full Disk Access.
// When SIP is disabled, the TCC database at
// /Library/Application Support/com.apple.TCC/TCC.db can be modified
// directly with sqlite3.
//
// Two approaches are provided:
//
//  1. Runtime grant via guest agent: Connects to the running VM's agent
//     and executes sqlite3 commands to insert FDA entries.
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

// tccServiceFDA is the TCC service identifier for Full Disk Access.
const tccServiceFDA = "kTCCServiceSystemPolicyAllFiles"

// defaultFDAClients are paths that commonly need FDA for VM automation.
var defaultFDAClients = []string{
	"/usr/sbin/sshd",
	"/usr/sbin/systemsetup",
	"/usr/libexec/sshd-keygen-wrapper",
	"/usr/bin/osascript",
}

// tccDBPath is the system-wide TCC database path inside the guest.
const tccDBPath = "/Library/Application Support/com.apple.TCC/TCC.db"

// tccInsertSQL generates a SQL INSERT statement for granting FDA to a client path.
func tccInsertSQL(clientPath string) string {
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
);`, tccServiceFDA, clientPath)
}

// handleFDACommand dispatches FDA management subcommands.
func handleFDACommand(args []string) error {
	if len(args) == 0 {
		fmt.Println(fdaUsage)
		return nil
	}

	switch args[0] {
	case "grant":
		return handleFDAGrant(args[1:])
	case "status":
		return handleFDAStatus(args[1:])
	case "help":
		fmt.Println(fdaUsage)
		return nil
	default:
		return fmt.Errorf("unknown fda command: %s\n\n%s", args[0], fdaUsage)
	}
}

// handleFDAGrant grants Full Disk Access to specified clients.
func handleFDAGrant(args []string) error {
	fs := flag.NewFlagSet("fda grant", flag.ExitOnError)
	client := fs.String("client", "", "Client path to grant FDA (e.g., /usr/sbin/sshd)")
	all := fs.Bool("all", false, "Grant FDA to all default clients (sshd, systemsetup, etc.)")
	agentBinPath := fs.String("agent-path", "", "Also grant FDA to the vz-agent binary at this path")
	dryRun := fs.Bool("dry-run", false, "Print SQL without executing")
	injectMode := fs.Bool("inject", false, "Inject into disk image instead of using agent (VM must be stopped)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos fda grant [options]

Grant Full Disk Access (FDA) to applications inside the VM.
Requires SIP to be disabled first (see: vz-macos sip help).

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Grant FDA to all default clients via agent (VM must be running)
  vz-macos fda grant -all

  # Grant FDA to a specific binary
  vz-macos fda grant -client /usr/sbin/sshd

  # Grant FDA to agent binary
  vz-macos fda grant -agent-path /usr/local/bin/vz-agent

  # Preview SQL without executing
  vz-macos fda grant -all -dry-run

  # Inject into disk image (VM must be stopped)
  vz-macos fda grant -all -inject
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	var clients []string
	if *all {
		clients = append(clients, defaultFDAClients...)
	}
	if *client != "" {
		clients = append(clients, *client)
	}
	if *agentBinPath != "" {
		clients = append(clients, *agentBinPath)
	}

	if len(clients) == 0 {
		return fmt.Errorf("specify -all, -client, or -agent-path")
	}

	var sqlStatements []string
	for _, c := range clients {
		sqlStatements = append(sqlStatements, tccInsertSQL(c))
	}
	combinedSQL := strings.Join(sqlStatements, "\n")

	if *dryRun {
		fmt.Println("=== Dry Run: SQL to execute ===")
		fmt.Println(combinedSQL)
		return nil
	}

	if *injectMode {
		return grantFDAViaDiskInject(clients, combinedSQL)
	}

	return grantFDAViaAgent(clients, combinedSQL)
}

// grantFDAViaAgent executes the TCC.db grant via the guest agent.
func grantFDAViaAgent(clients []string, sql string) error {
	fmt.Println("=== Granting Full Disk Access via Agent ===")
	fmt.Printf("Clients: %s\n", strings.Join(clients, ", "))
	fmt.Println()

	sock := GetControlSocketPath()
	timeout := 30 * time.Second

	// Check SIP status first
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

	// Execute the SQL via sqlite3
	fmt.Println("Inserting FDA grants into TCC.db...")
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
	fmt.Println("=== FDA Grants Applied ===")
	for _, c := range clients {
		fmt.Printf("  Granted: %s\n", c)
	}
	fmt.Println()
	fmt.Println("Note: Changes take effect immediately for new process launches.")
	fmt.Println("Running processes may need to be restarted.")

	return nil
}

// parseAgentExecOutput extracts stdout+stderr from an agent-exec response.
func parseAgentExecOutput(data string) string {
	var result struct {
		ExitCode int    `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return data
	}
	return strings.TrimSpace(result.Stdout + result.Stderr)
}

// grantFDAViaDiskInject mounts the VM disk and injects TCC.db entries directly.
func grantFDAViaDiskInject(clients []string, sql string) error {
	fmt.Println("=== Granting FDA via Disk Injection ===")
	fmt.Printf("Clients: %s\n", strings.Join(clients, ", "))
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

	// TCC.db on the Data volume
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
	fmt.Println("=== FDA Grants Injected ===")
	for _, c := range clients {
		fmt.Printf("  Granted: %s\n", c)
	}
	fmt.Println()
	fmt.Println("WARNING: If injecting before first boot, macOS may recreate TCC.db")
	fmt.Println("and overwrite these entries. For best results, grant FDA after the")
	fmt.Println("VM has booted at least once and SIP is disabled.")

	return nil
}

// handleFDAStatus checks FDA status for known clients via the guest agent.
func handleFDAStatus(_ []string) error {
	fmt.Println("=== Full Disk Access Status ===")
	fmt.Println()

	sock := GetControlSocketPath()
	timeout := 30 * time.Second

	// Check SIP status
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

	// Query TCC.db for FDA entries
	query := fmt.Sprintf("SELECT client, auth_value FROM access WHERE service='%s';", tccServiceFDA)
	tccResp, err := ctlSendCommand(sock, "agent-exec", map[string]interface{}{
		"args": []string{"sqlite3", tccDBPath, query},
	}, timeout)
	if err != nil {
		fmt.Println("Could not query TCC.db (SIP may be enabled or database may not exist)")
		return nil
	}

	if !tccResp.Success {
		fmt.Printf("Could not query TCC.db: %s\n", tccResp.Error)
		return nil
	}

	output := parseAgentExecOutput(tccResp.Data)
	if output == "" {
		fmt.Println("No FDA entries found in TCC.db")
		return nil
	}

	fmt.Println("FDA entries in TCC.db:")
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) == 2 {
			status := "denied"
			if parts[1] == "2" {
				status = "allowed"
			}
			fmt.Printf("  %s: %s\n", parts[0], status)
		} else {
			fmt.Printf("  %s\n", line)
		}
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

const fdaUsage = `Usage: vz-macos fda <command>

Commands:
  grant     Grant Full Disk Access to applications inside the VM
  status    Check FDA status for known clients
  help      Show this help

Full Disk Access (FDA) allows applications to access protected files
and directories. This is required for:
  - sshd (remote login on macOS 14+)
  - systemsetup (enable/disable remote login)
  - vz-agent (file read/write operations)

IMPORTANT: FDA grants via TCC.db require SIP to be disabled first.
See 'vz-macos sip help' for instructions.

Workflow:
  1. Disable SIP:    vz-macos sip disable
  2. Grant FDA:      vz-macos fda grant -all
  3. Verify:         vz-macos fda status`
