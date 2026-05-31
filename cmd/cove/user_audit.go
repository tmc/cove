package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type userAuditOptions struct {
	VM   string
	User string
	JSON bool
}

type userAuditReport struct {
	VM                string             `json:"vm"`
	User              string             `json:"user"`
	GuestOS           string             `json:"guest_os"`
	Exists            bool               `json:"exists"`
	UID               string             `json:"uid,omitempty"`
	GID               string             `json:"gid,omitempty"`
	Home              string             `json:"home,omitempty"`
	Shell             string             `json:"shell,omitempty"`
	Groups            []string           `json:"groups,omitempty"`
	Admin             bool               `json:"admin"`
	Sudo              bool               `json:"sudo"`
	HomeExists        bool               `json:"home_exists"`
	SSHAuthorizedKeys bool               `json:"ssh_authorized_keys"`
	LaunchAgents      []string           `json:"launch_agents,omitempty"`
	Keychains         []string           `json:"keychains,omitempty"`
	CoveFiles         []string           `json:"cove_files,omitempty"`
	Residue           []userAuditResidue `json:"residue,omitempty"`
}

type userAuditResidue struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type userAuditAgent interface {
	AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error)
}

func handleUserCommand(env commandEnv, args []string) error {
	env = env.WithDefaultIO()
	if len(args) == 0 {
		printUserUsage(env.Stderr)
		return fmt.Errorf("usage: cove user <command>")
	}
	if isHelpArg(args[0]) {
		printUserUsage(env.Stdout)
		return nil
	}
	switch args[0] {
	case "audit":
		return runUserAuditCommand(env, args[1:], newControlUserAuditAgent)
	case "create":
		return runUserCreateCommand(env, args[1:], newControlUserLifecycleAgent)
	case "delete":
		return runUserDeleteCommand(env, args[1:], newControlUserLifecycleAgent)
	default:
		printUserUsage(env.Stderr)
		return fmt.Errorf("user: unknown command %q", args[0])
	}
}

func runUserAuditCommand(env commandEnv, args []string, newAgent func(string) userAuditAgent) error {
	opts, err := parseUserAuditArgs(env, args)
	if errors.Is(err, errFlagHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	dir, label, err := resolveUserAuditVM(env, opts.VM)
	if err != nil {
		return err
	}
	if !isVMRunningAt(dir) {
		state := detectVMState(dir)
		return fmt.Errorf("vm %q is %s; user audit requires a running VM\n  start it with: cove -vm %s run", label, state, label)
	}
	report, err := collectUserAudit(newAgent(dir), opts.User, label)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeUserAuditJSON(env.Stdout, report)
	}
	return writeUserAuditText(env.Stdout, report)
}

func parseUserAuditArgs(env commandEnv, args []string) (userAuditOptions, error) {
	env = env.WithDefaultIO()
	fs := flag.NewFlagSet("user audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts userAuditOptions
	fs.StringVar(&opts.VM, "vm", "", "VM name")
	fs.StringVar(&opts.User, "user", "", "guest username")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON")
	fs.Usage = func() { printUserAuditUsage(env.Stdout) }
	if err := parseFlagsOrHelp(fs, moveKnownFlagsFirst(args, map[string]bool{"vm": true, "user": true, "json": false})); err != nil {
		return userAuditOptions{}, err
	}
	if fs.NArg() > 1 {
		return userAuditOptions{}, fmt.Errorf("usage: cove user audit <vm> --user <name> [-json]")
	}
	if fs.NArg() == 1 {
		positional := strings.TrimSpace(fs.Arg(0))
		if opts.VM != "" && opts.VM != positional {
			return userAuditOptions{}, fmt.Errorf("user audit: -vm %q does not match positional VM %q", opts.VM, positional)
		}
		opts.VM = positional
	}
	opts.User = strings.TrimSpace(opts.User)
	if err := validateUserAuditName(opts.User); err != nil {
		return userAuditOptions{}, err
	}
	return opts, nil
}

func validateUserAuditName(name string) error {
	if name == "" {
		return fmt.Errorf("user audit: -user is required")
	}
	if strings.ContainsAny(name, "\x00/\n\r\t") {
		return fmt.Errorf("user audit: invalid username %q", name)
	}
	return nil
}

func resolveUserAuditVM(env commandEnv, name string) (dir, label string, err error) {
	return resolveUserVM(env, name, "user audit")
}

func resolveUserVM(env commandEnv, name, action string) (dir, label string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(env.VM.Name)
	}
	if name == "" {
		name = strings.TrimSpace(vmName)
	}
	if name == "" && strings.TrimSpace(env.VM.Dir) != "" {
		dir = env.VM.Dir
		if !vmconfig.Validate(dir) {
			return "", "", fmt.Errorf("%s: VM directory is invalid: %s", action, dir)
		}
		return dir, filepath.Base(dir), nil
	}
	if name == "" {
		name = vmconfig.ActiveName()
	}
	dir, err = requireExistingVMForControl(name)
	if err != nil {
		return "", "", err
	}
	return dir, name, nil
}

func newControlUserAuditAgent(vmDir string) userAuditAgent {
	return NewControlClient(GetControlSocketPathForVM(vmDir))
}

func collectUserAudit(agent userAuditAgent, user, vm string) (userAuditReport, error) {
	osName, err := detectGuestOS(agent)
	if err != nil {
		return userAuditReport{}, err
	}
	var script string
	switch osName {
	case guestOSDarwin:
		script = macOSUserAuditScript(user)
	case guestOSLinux:
		script = linuxUserAuditScript(user)
	default:
		return userAuditReport{}, fmt.Errorf("user audit: unsupported guest os %q", osName)
	}
	resp, err := agent.AgentExecTypedTimeout([]string{"/bin/sh", "-lc", script}, nil, "", 15*time.Second)
	if err != nil {
		return userAuditReport{}, fmt.Errorf("user audit: %w", err)
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(resp.GetStderr())
		if msg == "" {
			msg = strings.TrimSpace(resp.GetStdout())
		}
		return userAuditReport{}, fmt.Errorf("user audit: %s", msg)
	}
	report, err := parseUserAuditOutput(resp.GetStdout())
	if err != nil {
		return userAuditReport{}, err
	}
	report.VM = vm
	report.User = user
	report.GuestOS = osName
	return report, nil
}

func parseUserAuditOutput(out string) (userAuditReport, error) {
	var report userAuditReport
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "\t")
		if !ok {
			return userAuditReport{}, fmt.Errorf("parse user audit: malformed line %q", line)
		}
		switch key {
		case "exists":
			report.Exists = parseUserAuditBool(value)
		case "uid":
			report.UID = value
		case "gid":
			report.GID = value
		case "home":
			report.Home = value
		case "shell":
			report.Shell = value
		case "groups":
			report.Groups = strings.Fields(value)
		case "admin":
			report.Admin = parseUserAuditBool(value)
		case "sudo":
			report.Sudo = parseUserAuditBool(value)
		case "home_exists":
			report.HomeExists = parseUserAuditBool(value)
		case "ssh_authorized_keys":
			report.SSHAuthorizedKeys = parseUserAuditBool(value)
		case "launch_agent":
			report.LaunchAgents = append(report.LaunchAgents, value)
		case "keychain":
			report.Keychains = append(report.Keychains, value)
		case "cove_file":
			report.CoveFiles = append(report.CoveFiles, value)
		case "residue":
			kind, path, ok := strings.Cut(value, "|")
			if !ok {
				return userAuditReport{}, fmt.Errorf("parse user audit: malformed residue %q", value)
			}
			report.Residue = append(report.Residue, userAuditResidue{Kind: kind, Path: path})
		}
	}
	return report, nil
}

func parseUserAuditBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func writeUserAuditJSON(w io.Writer, report userAuditReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func writeUserAuditText(w io.Writer, report userAuditReport) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "vm:\t%s\n", report.VM)
	fmt.Fprintf(tw, "user:\t%s\n", report.User)
	fmt.Fprintf(tw, "guest os:\t%s\n", report.GuestOS)
	fmt.Fprintf(tw, "exists:\t%s\n", userAuditYesNo(report.Exists))
	if report.UID != "" || report.GID != "" {
		fmt.Fprintf(tw, "id:\tuid=%s gid=%s\n", report.UID, report.GID)
	}
	if report.Home != "" {
		fmt.Fprintf(tw, "home:\t%s (%s)\n", report.Home, userAuditYesNo(report.HomeExists))
	}
	if report.Shell != "" {
		fmt.Fprintf(tw, "shell:\t%s\n", report.Shell)
	}
	fmt.Fprintf(tw, "groups:\t%s\n", userAuditList(report.Groups))
	fmt.Fprintf(tw, "admin:\t%s\n", userAuditYesNo(report.Admin))
	fmt.Fprintf(tw, "sudo:\t%s\n", userAuditYesNo(report.Sudo))
	fmt.Fprintf(tw, "ssh authorized_keys:\t%s\n", userAuditYesNo(report.SSHAuthorizedKeys))
	if len(report.Residue) == 0 {
		fmt.Fprintf(tw, "residue:\t(none)\n")
		return tw.Flush()
	}
	fmt.Fprintf(tw, "residue:\t%d item(s)\n", len(report.Residue))
	for _, r := range report.Residue {
		fmt.Fprintf(tw, "  %s:\t%s\n", r.Kind, r.Path)
	}
	return tw.Flush()
}

func userAuditList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}

func userAuditYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func printUserUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove user <command> [options]

Manage and inspect guest user state through the running VM guest agent.

Commands:
  audit    Audit a guest user for identity and leftover state
  create   Create a guest user through the guest agent
  delete   Delete a guest user through the guest agent`)
}

func printUserAuditUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove user audit <vm> --user <name> [-json]
       cove user audit --user <name> [-vm <vm>] [-json]

Audit a guest user through the running VM guest agent without changing guest
state. macOS audits identity, admin group, home, SSH keys, LaunchAgents,
keychains, login-item storage, and cove provisioning files. Linux audits
identity, sudo/admin groups, home, SSH keys, systemd user units, sudoers, and
cove provisioning files.`)
}

func macOSUserAuditScript(user string) string {
	return strings.ReplaceAll(macOSUserAuditScriptTemplate, "__USER__", shellEscape(user))
}

func linuxUserAuditScript(user string) string {
	return strings.ReplaceAll(linuxUserAuditScriptTemplate, "__USER__", shellEscape(user))
}

const macOSUserAuditScriptTemplate = `u=__USER__
emit() { printf '%s	%s\n' "$1" "$2"; }
exists=false
uid=
gid=
home=
shell=
if dscl . -read "/Users/$u" >/dev/null 2>&1; then
	exists=true
	uid=$(dscl . -read "/Users/$u" UniqueID 2>/dev/null | awk '{print $2; exit}')
	gid=$(dscl . -read "/Users/$u" PrimaryGroupID 2>/dev/null | awk '{print $2; exit}')
	home=$(dscl . -read "/Users/$u" NFSHomeDirectory 2>/dev/null | sed 's/^NFSHomeDirectory: //')
	shell=$(dscl . -read "/Users/$u" UserShell 2>/dev/null | sed 's/^UserShell: //')
fi
[ -n "$home" ] || home="/Users/$u"
groups=$(id -Gn "$u" 2>/dev/null || true)
admin=false
printf '%s\n' "$groups" | tr ' ' '\n' | grep -qx admin && admin=true
emit exists "$exists"
emit uid "$uid"
emit gid "$gid"
emit home "$home"
emit shell "$shell"
emit groups "$groups"
emit admin "$admin"
emit sudo "$admin"
home_exists=false
if [ -d "$home" ]; then
	home_exists=true
	emit residue "home|$home"
fi
emit home_exists "$home_exists"
ssh_keys=false
if [ -f "$home/.ssh/authorized_keys" ]; then
	ssh_keys=true
	emit residue "ssh_authorized_keys|$home/.ssh/authorized_keys"
fi
emit ssh_authorized_keys "$ssh_keys"
if [ -d "$home/Library/LaunchAgents" ]; then
	for p in "$home/Library/LaunchAgents"/*.plist; do
		[ -e "$p" ] || continue
		emit launch_agent "$p"
		emit residue "launch_agent|$p"
	done
fi
if [ -d "$home/Library/Keychains" ]; then
	for p in "$home/Library/Keychains"/*; do
		[ -e "$p" ] || continue
		emit keychain "$p"
		emit residue "keychain|$p"
	done
fi
for p in "$home/Library/Application Support/com.apple.backgroundtaskmanagementagent" "$home/Library/Preferences/com.apple.loginitems.plist"; do
	[ -e "$p" ] || continue
	emit residue "login_items|$p"
done
for p in /private/var/db/.vz-provisioned /private/var/db/vz-provision.sh /Library/LaunchDaemons/com.tmc.cove.provision.plist /Library/LaunchDaemons/com.tmc.cove.autologin.plist /Library/LaunchDaemons/com.tmc.cove.vz-agent.plist /Library/LaunchAgents/com.tmc.cove.vz-agent-user.plist /usr/local/bin/vz-agent /private/etc/kcpassword; do
	[ -e "$p" ] || continue
	emit cove_file "$p"
	emit residue "cove_file|$p"
done
`

const linuxUserAuditScriptTemplate = `u=__USER__
emit() { printf '%s	%s\n' "$1" "$2"; }
exists=false
uid=
gid=
home=
shell=
entry=$(getent passwd "$u" 2>/dev/null || true)
if [ -n "$entry" ]; then
	exists=true
	oldIFS=$IFS
	IFS=:
	set -- $entry
	IFS=$oldIFS
	uid=$3
	gid=$4
	home=$6
	shell=$7
fi
[ -n "$home" ] || home="/home/$u"
groups=$(id -Gn "$u" 2>/dev/null || true)
admin=false
sudo=false
for g in $groups; do
	case "$g" in
	sudo|wheel)
		admin=true
		sudo=true
		;;
	esac
done
if [ -f "/etc/sudoers.d/$u" ]; then
	sudo=true
	emit residue "sudoers|/etc/sudoers.d/$u"
fi
emit exists "$exists"
emit uid "$uid"
emit gid "$gid"
emit home "$home"
emit shell "$shell"
emit groups "$groups"
emit admin "$admin"
emit sudo "$sudo"
home_exists=false
if [ -d "$home" ]; then
	home_exists=true
	emit residue "home|$home"
fi
emit home_exists "$home_exists"
ssh_keys=false
if [ -f "$home/.ssh/authorized_keys" ]; then
	ssh_keys=true
	emit residue "ssh_authorized_keys|$home/.ssh/authorized_keys"
fi
emit ssh_authorized_keys "$ssh_keys"
if [ -d "$home/.config/systemd/user" ]; then
	emit residue "systemd_user|$home/.config/systemd/user"
fi
for p in /etc/cove-provisioned /var/lib/cove-setup.done /etc/cloud/cloud-init.disabled /etc/systemd/system/vz-agent.service /usr/local/bin/vz-agent; do
	[ -e "$p" ] || continue
	emit cove_file "$p"
	emit residue "cove_file|$p"
done
`
