package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/secrets"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type userLifecycleAgent interface {
	userAuditAgent
	AgentDaemonExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error)
}

type userCreateOptions struct {
	VM           string
	User         string
	PasswordSpec string
	Admin        bool
	SSHKeyPath   string
	JSON         bool
}

type userDeleteOptions struct {
	VM       string
	User     string
	KeepHome bool
	JSON     bool
}

type userMutationReport struct {
	VM                string `json:"vm"`
	User              string `json:"user"`
	GuestOS           string `json:"guest_os"`
	Action            string `json:"action"`
	Admin             bool   `json:"admin,omitempty"`
	Home              string `json:"home,omitempty"`
	SSHAuthorizedKeys bool   `json:"ssh_authorized_keys,omitempty"`
	Existed           bool   `json:"existed,omitempty"`
	Deleted           bool   `json:"deleted,omitempty"`
	HomeRemoved       bool   `json:"home_removed,omitempty"`
	KeepHome          bool   `json:"keep_home,omitempty"`
}

func runUserCreateCommand(env commandEnv, args []string, newAgent func(string) userLifecycleAgent) error {
	opts, err := parseUserCreateArgs(env, args)
	if errors.Is(err, errFlagHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	dir, label, err := resolveUserVM(env, opts.VM, "user create")
	if err != nil {
		return err
	}
	if !isVMRunningAt(dir) {
		state := detectVMState(dir)
		return fmt.Errorf("vm %q is %s; user create requires a running VM\n  start it with: cove -vm %s run", label, state, label)
	}
	password, err := resolveUserPassword(env, opts.User, opts.PasswordSpec)
	if err != nil {
		return err
	}
	sshKey, err := readUserSSHKey(opts.SSHKeyPath)
	if err != nil {
		return err
	}
	report, err := createGuestUser(newAgent(dir), opts, label, password, sshKey)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeUserMutationJSON(env.Stdout, report)
	}
	return writeUserMutationText(env.Stdout, report)
}

func runUserDeleteCommand(env commandEnv, args []string, newAgent func(string) userLifecycleAgent) error {
	opts, err := parseUserDeleteArgs(env, args)
	if errors.Is(err, errFlagHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	dir, label, err := resolveUserVM(env, opts.VM, "user delete")
	if err != nil {
		return err
	}
	if !isVMRunningAt(dir) {
		state := detectVMState(dir)
		return fmt.Errorf("vm %q is %s; user delete requires a running VM\n  start it with: cove -vm %s run", label, state, label)
	}
	report, err := deleteGuestUser(newAgent(dir), opts, label)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeUserMutationJSON(env.Stdout, report)
	}
	return writeUserMutationText(env.Stdout, report)
}

func parseUserCreateArgs(env commandEnv, args []string) (userCreateOptions, error) {
	env = env.WithDefaultIO()
	fs := flag.NewFlagSet("user create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts userCreateOptions
	fs.StringVar(&opts.VM, "vm", "", "VM name")
	fs.StringVar(&opts.User, "user", "", "guest username")
	fs.StringVar(&opts.PasswordSpec, "password", "", "password literal or env://, file://, fd:// secret reference")
	fs.BoolVar(&opts.Admin, "admin", false, "make the user an administrator")
	fs.StringVar(&opts.SSHKeyPath, "ssh-key", "", "SSH public key file to install as authorized_keys")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON")
	fs.Usage = func() { printUserCreateUsage(env.Stdout) }
	if err := parseFlagsOrHelp(fs, moveKnownFlagsFirst(args, map[string]bool{
		"vm": true, "user": true, "password": true, "admin": false, "ssh-key": true, "json": false,
	})); err != nil {
		return userCreateOptions{}, err
	}
	if fs.NArg() > 1 {
		return userCreateOptions{}, fmt.Errorf("usage: cove user create <vm> --user <name> [flags]")
	}
	if fs.NArg() == 1 {
		positional := strings.TrimSpace(fs.Arg(0))
		if opts.VM != "" && opts.VM != positional {
			return userCreateOptions{}, fmt.Errorf("user create: -vm %q does not match positional VM %q", opts.VM, positional)
		}
		opts.VM = positional
	}
	opts.User = strings.TrimSpace(opts.User)
	if err := validateGuestUsername("user create", opts.User); err != nil {
		return userCreateOptions{}, err
	}
	return opts, nil
}

func parseUserDeleteArgs(env commandEnv, args []string) (userDeleteOptions, error) {
	env = env.WithDefaultIO()
	fs := flag.NewFlagSet("user delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts userDeleteOptions
	fs.StringVar(&opts.VM, "vm", "", "VM name")
	fs.StringVar(&opts.User, "user", "", "guest username")
	fs.BoolVar(&opts.KeepHome, "keep-home", false, "leave the user's home directory on disk")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON")
	fs.Usage = func() { printUserDeleteUsage(env.Stdout) }
	if err := parseFlagsOrHelp(fs, moveKnownFlagsFirst(args, map[string]bool{
		"vm": true, "user": true, "keep-home": false, "json": false,
	})); err != nil {
		return userDeleteOptions{}, err
	}
	if fs.NArg() > 1 {
		return userDeleteOptions{}, fmt.Errorf("usage: cove user delete <vm> --user <name> [flags]")
	}
	if fs.NArg() == 1 {
		positional := strings.TrimSpace(fs.Arg(0))
		if opts.VM != "" && opts.VM != positional {
			return userDeleteOptions{}, fmt.Errorf("user delete: -vm %q does not match positional VM %q", opts.VM, positional)
		}
		opts.VM = positional
	}
	opts.User = strings.TrimSpace(opts.User)
	if err := validateGuestUsername("user delete", opts.User); err != nil {
		return userDeleteOptions{}, err
	}
	return opts, nil
}

func validateGuestUsername(command, name string) error {
	if name == "" {
		return fmt.Errorf("%s: -user is required", command)
	}
	if strings.ContainsAny(name, "\x00/:\n\r\t") || strings.HasPrefix(name, "-") {
		return fmt.Errorf("%s: invalid username %q", command, name)
	}
	return nil
}

func resolveUserPassword(env commandEnv, user, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		pw, err := readPassword(fmt.Sprintf("Password for %s: ", user))
		if err != nil {
			return "", fmt.Errorf("read user password: %w", err)
		}
		return strings.TrimRight(string(pw), "\r\n"), nil
	}
	if strings.HasPrefix(spec, "fd://") {
		value, err := readUserPasswordFD(strings.TrimPrefix(spec, "fd://"))
		if err != nil {
			return "", err
		}
		return value, nil
	}
	if strings.HasPrefix(spec, "env://") || strings.HasPrefix(spec, "file://") {
		value, err := secrets.Resolve(spec)
		if err != nil {
			return "", fmt.Errorf("resolve user password: %w", err)
		}
		return strings.TrimRight(string(value), "\r\n"), nil
	}
	return spec, nil
}

func readUserPasswordFD(raw string) (string, error) {
	fd, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || fd < 0 {
		return "", fmt.Errorf("resolve user password: invalid fd://%s", raw)
	}
	dup, err := syscall.Dup(fd)
	if err != nil {
		return "", fmt.Errorf("resolve user password: dup fd://%d: %w", fd, err)
	}
	f := os.NewFile(uintptr(dup), "cove-user-password")
	if f == nil {
		_ = syscall.Close(dup)
		return "", fmt.Errorf("resolve user password: invalid fd://%d", fd)
	}
	defer f.Close()
	value, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("resolve user password: read fd://%d: %w", fd, err)
	}
	return strings.TrimRight(string(value), "\r\n"), nil
}

func readUserSSHKey(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read ssh key %s: %w", path, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("read ssh key %s: empty key", path)
	}
	return key, nil
}

func newControlUserLifecycleAgent(vmDir string) userLifecycleAgent {
	return NewControlClient(GetControlSocketPathForVM(vmDir))
}

func createGuestUser(agent userLifecycleAgent, opts userCreateOptions, vm, password, sshKey string) (userMutationReport, error) {
	osName, err := detectGuestOS(agent)
	if err != nil {
		return userMutationReport{}, err
	}
	var script string
	switch osName {
	case guestOSDarwin:
		script = macOSUserCreateScript
	case guestOSLinux:
		script = linuxUserCreateScript
	default:
		return userMutationReport{}, fmt.Errorf("user create: unsupported guest os %q", osName)
	}
	env := map[string]string{
		"COVE_USER_NAME":     opts.User,
		"COVE_USER_PASSWORD": password,
		"COVE_USER_ADMIN":    strconv.FormatBool(opts.Admin),
		"COVE_USER_SSH_KEY":  sshKey,
	}
	report, err := runUserMutationScript(agent, "user create", script, env)
	if err != nil {
		return userMutationReport{}, err
	}
	report.VM = vm
	report.User = opts.User
	report.GuestOS = osName
	report.Action = "create"
	return report, nil
}

func deleteGuestUser(agent userLifecycleAgent, opts userDeleteOptions, vm string) (userMutationReport, error) {
	osName, err := detectGuestOS(agent)
	if err != nil {
		return userMutationReport{}, err
	}
	var script string
	switch osName {
	case guestOSDarwin:
		script = macOSUserDeleteScript
	case guestOSLinux:
		script = linuxUserDeleteScript
	default:
		return userMutationReport{}, fmt.Errorf("user delete: unsupported guest os %q", osName)
	}
	env := map[string]string{
		"COVE_USER_NAME":      opts.User,
		"COVE_USER_KEEP_HOME": strconv.FormatBool(opts.KeepHome),
	}
	report, err := runUserMutationScript(agent, "user delete", script, env)
	if err != nil {
		return userMutationReport{}, err
	}
	report.VM = vm
	report.User = opts.User
	report.GuestOS = osName
	report.Action = "delete"
	return report, nil
}

func runUserMutationScript(agent userLifecycleAgent, action, script string, env map[string]string) (userMutationReport, error) {
	resp, err := agent.AgentDaemonExecTypedTimeout([]string{"/bin/sh", "-lc", script}, env, "", 30*time.Second)
	if err != nil {
		return userMutationReport{}, fmt.Errorf("%s: %w", action, err)
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(resp.GetStderr())
		if msg == "" {
			msg = strings.TrimSpace(resp.GetStdout())
		}
		return userMutationReport{}, fmt.Errorf("%s: %s", action, msg)
	}
	return parseUserMutationOutput(resp.GetStdout())
}

func parseUserMutationOutput(out string) (userMutationReport, error) {
	var report userMutationReport
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "\t")
		if !ok {
			return userMutationReport{}, fmt.Errorf("parse user lifecycle: malformed line %q", line)
		}
		switch key {
		case "admin":
			report.Admin = parseUserAuditBool(value)
		case "home":
			report.Home = value
		case "ssh_authorized_keys":
			report.SSHAuthorizedKeys = parseUserAuditBool(value)
		case "existed":
			report.Existed = parseUserAuditBool(value)
		case "deleted":
			report.Deleted = parseUserAuditBool(value)
		case "home_removed":
			report.HomeRemoved = parseUserAuditBool(value)
		case "keep_home":
			report.KeepHome = parseUserAuditBool(value)
		}
	}
	return report, nil
}

func writeUserMutationJSON(w io.Writer, report userMutationReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func writeUserMutationText(w io.Writer, report userMutationReport) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "vm:\t%s\n", report.VM)
	fmt.Fprintf(tw, "user:\t%s\n", report.User)
	fmt.Fprintf(tw, "guest os:\t%s\n", report.GuestOS)
	fmt.Fprintf(tw, "action:\t%s\n", report.Action)
	if report.Action == "create" {
		fmt.Fprintf(tw, "admin:\t%s\n", userAuditYesNo(report.Admin))
		if report.Home != "" {
			fmt.Fprintf(tw, "home:\t%s\n", report.Home)
		}
		fmt.Fprintf(tw, "ssh authorized_keys:\t%s\n", userAuditYesNo(report.SSHAuthorizedKeys))
		return tw.Flush()
	}
	fmt.Fprintf(tw, "existed:\t%s\n", userAuditYesNo(report.Existed))
	fmt.Fprintf(tw, "deleted:\t%s\n", userAuditYesNo(report.Deleted))
	fmt.Fprintf(tw, "home removed:\t%s\n", userAuditYesNo(report.HomeRemoved))
	if report.KeepHome {
		fmt.Fprintf(tw, "keep home:\tyes\n")
	}
	return tw.Flush()
}

func printUserCreateUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove user create <vm> --user <name> [flags]
       cove user create --user <name> [-vm <vm>] [flags]

Create a standard guest user through the running VM guest agent. Pass --admin
to make the user an administrator. If --password is omitted, cove prompts.

Flags:
  --admin             make the user an administrator
  --password SPEC     password literal, env://VAR, file:///abs/path, or fd://N
  --ssh-key PATH      public key to install as authorized_keys
  --json              emit machine-readable JSON`)
}

func printUserDeleteUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove user delete <vm> --user <name> [flags]
       cove user delete --user <name> [-vm <vm>] [flags]

Delete a guest user through the running VM guest agent and remove the standard
home directory by default. Use --keep-home when you only want to remove the
account record.

Flags:
  --keep-home   leave the user's home directory on disk
  --json        emit machine-readable JSON`)
}

const macOSUserCreateScript = `set -eu
emit() { printf '%s	%s\n' "$1" "$2"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }
u=$COVE_USER_NAME
p=$COVE_USER_PASSWORD
admin=${COVE_USER_ADMIN:-false}
key=${COVE_USER_SSH_KEY:-}
home="/Users/$u"
if dscl . -read "/Users/$u" >/dev/null 2>&1; then
	fail "user $u already exists"
fi
err=/tmp/cove-user-create.err
if command -v sysadminctl >/dev/null 2>&1; then
	if [ "$admin" = "true" ]; then
		sysadminctl -addUser "$u" -password "$p" -fullName "$u" -shell /bin/zsh -home "$home" -admin >"$err" 2>&1 || { cat "$err" >&2; exit 1; }
	else
		sysadminctl -addUser "$u" -password "$p" -fullName "$u" -shell /bin/zsh -home "$home" >"$err" 2>&1 || { cat "$err" >&2; exit 1; }
	fi
else
	nextid=$(dscl . -list /Users UniqueID | awk '{print $2}' | sort -n | tail -1)
	nextid=$((nextid + 1))
	dscl . -create "/Users/$u"
	dscl . -create "/Users/$u" UserShell /bin/zsh
	dscl . -create "/Users/$u" RealName "$u"
	dscl . -create "/Users/$u" UniqueID "$nextid"
	dscl . -create "/Users/$u" PrimaryGroupID 20
	dscl . -create "/Users/$u" NFSHomeDirectory "$home"
	dscl . -passwd "/Users/$u" "$p"
fi
if [ "$admin" = "true" ]; then
	dseditgroup -o edit -a "$u" -t user admin >/dev/null 2>&1 || true
fi
createhomedir -c -u "$u" >/dev/null 2>&1 || true
if [ -n "$key" ]; then
	mkdir -p "$home/.ssh"
	touch "$home/.ssh/authorized_keys"
	grep -qxF "$key" "$home/.ssh/authorized_keys" 2>/dev/null || printf '%s\n' "$key" >> "$home/.ssh/authorized_keys"
	chown -R "$u:staff" "$home/.ssh" >/dev/null 2>&1 || true
	chmod 700 "$home/.ssh"
	chmod 600 "$home/.ssh/authorized_keys"
fi
emit admin "$admin"
emit home "$home"
if [ -f "$home/.ssh/authorized_keys" ]; then emit ssh_authorized_keys true; else emit ssh_authorized_keys false; fi
`

const linuxUserCreateScript = `set -eu
emit() { printf '%s	%s\n' "$1" "$2"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }
u=$COVE_USER_NAME
p=$COVE_USER_PASSWORD
admin=${COVE_USER_ADMIN:-false}
key=${COVE_USER_SSH_KEY:-}
if getent passwd "$u" >/dev/null 2>&1; then
	fail "user $u already exists"
fi
if [ "$admin" = "true" ]; then
	if getent group sudo >/dev/null 2>&1; then
		useradd -m -s /bin/bash -G sudo "$u"
	elif getent group wheel >/dev/null 2>&1; then
		useradd -m -s /bin/bash -G wheel "$u"
	else
		useradd -m -s /bin/bash "$u"
	fi
else
	useradd -m -s /bin/bash "$u"
fi
printf '%s:%s\n' "$u" "$p" | chpasswd
home=$(getent passwd "$u" | awk -F: '{print $6}')
[ -n "$home" ] || home="/home/$u"
if [ -n "$key" ]; then
	mkdir -p "$home/.ssh"
	touch "$home/.ssh/authorized_keys"
	grep -qxF "$key" "$home/.ssh/authorized_keys" 2>/dev/null || printf '%s\n' "$key" >> "$home/.ssh/authorized_keys"
	uid=$(id -u "$u")
	gid=$(id -g "$u")
	chown -R "$uid:$gid" "$home/.ssh"
	chmod 700 "$home/.ssh"
	chmod 600 "$home/.ssh/authorized_keys"
fi
emit admin "$admin"
emit home "$home"
if [ -f "$home/.ssh/authorized_keys" ]; then emit ssh_authorized_keys true; else emit ssh_authorized_keys false; fi
`

const macOSUserDeleteScript = `set -eu
emit() { printf '%s	%s\n' "$1" "$2"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }
u=$COVE_USER_NAME
keep=${COVE_USER_KEEP_HOME:-false}
home="/Users/$u"
if dscl . -read "/Users/$u" NFSHomeDirectory >/dev/null 2>&1; then
	home=$(dscl . -read "/Users/$u" NFSHomeDirectory | sed 's/^NFSHomeDirectory: //')
fi
existed=false
deleted=false
home_removed=false
if dscl . -read "/Users/$u" >/dev/null 2>&1; then
	existed=true
	if command -v sysadminctl >/dev/null 2>&1; then
		sysadminctl -deleteUser "$u" >/tmp/cove-user-delete.err 2>&1 || dscl . -delete "/Users/$u" >/dev/null 2>&1 || { cat /tmp/cove-user-delete.err >&2; exit 1; }
	else
		dscl . -delete "/Users/$u" >/dev/null 2>&1 || fail "delete user $u failed"
	fi
	deleted=true
fi
if [ "$keep" != "true" ]; then
	case "$home" in
	/Users/"$u"|/Users/"$u"/)
		if [ -d "$home" ]; then
			rm -rf "$home"
			home_removed=true
		fi
		;;
	esac
fi
emit existed "$existed"
emit deleted "$deleted"
emit home "$home"
emit home_removed "$home_removed"
emit keep_home "$keep"
`

const linuxUserDeleteScript = `set -eu
emit() { printf '%s	%s\n' "$1" "$2"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }
u=$COVE_USER_NAME
keep=${COVE_USER_KEEP_HOME:-false}
home="/home/$u"
if getent passwd "$u" >/dev/null 2>&1; then
	home=$(getent passwd "$u" | awk -F: '{print $6}')
fi
existed=false
deleted=false
home_removed=false
if getent passwd "$u" >/dev/null 2>&1; then
	existed=true
	userdel "$u" || fail "delete user $u failed"
	deleted=true
fi
rm -f "/etc/sudoers.d/$u"
if [ "$keep" != "true" ]; then
	case "$home" in
	/home/"$u"|/home/"$u"/)
		if [ -d "$home" ]; then
			rm -rf "$home"
			home_removed=true
		fi
		;;
	esac
fi
emit existed "$existed"
emit deleted "$deleted"
emit home "$home"
emit home_removed "$home_removed"
emit keep_home "$keep"
`
