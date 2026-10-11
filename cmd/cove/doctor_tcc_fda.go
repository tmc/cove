package main

import (
	"crypto/rand"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

const guestVZAgentPath = "/usr/local/bin/vz-agent"

type tccFDAOptions struct {
	vmName        string
	path          string
	timeout       time.Duration
	upgradeAgent  bool
	check         bool
	exportProfile string
}

func runTCCFDAAuthorize(args []string) error {
	opts, err := parseTCCFDAArgs(args)
	if err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	target := currentVMSelection()
	name := vmName
	if opts.vmName != "" {
		name = opts.vmName
	}
	if name != "" {
		target, err = requireExistingVMSelection("doctor tcc-fda", name)
		if err != nil {
			return err
		}
	}
	sock := target.controlSocketPath()
	if !isVMRunning(sock) {
		return fmt.Errorf("doctor tcc-fda: VM is not running\n  start it first, for example: cove run -vm %s -headless -no-resume -auto-upgrade-agent", target.elevationLabel())
	}

	if opts.upgradeAgent {
		fmt.Println("COVE_TCC_FDA_AGENT_UPGRADE start")
		if err := upgradeAgentAt(sock); err != nil {
			return fmt.Errorf("upgrade agent before FDA setup: %w", err)
		}
		fmt.Println("COVE_TCC_FDA_AGENT_UPGRADE complete")
		time.Sleep(5 * time.Second)
	}

	client := NewControlClient(sock)
	client.SetTimeout(30 * time.Second)

	req, reqErr := guestAgentRequirement(client)
	if reqErr != nil {
		fmt.Printf("COVE_TCC_FDA_IDENTITY agent=%s error=%s\n", guestVZAgentPath, shellQuote(reqErr.Error()))
	} else {
		printTCCFDAIdentity(os.Stdout, req)
	}

	if opts.exportProfile != "" {
		if reqErr != nil {
			return fmt.Errorf("doctor tcc-fda: export profile: %w", reqErr)
		}
		return exportTCCFDAProfile(os.Stdout, opts.exportProfile, req)
	}

	status, detail := probeTCCFDA(client)
	printTCCFDAStatus(os.Stdout, status, detail)
	switch {
	case status == tccFDAAuthorized:
		reportTCCPathAccess(sock, opts.path)
		return nil
	case opts.check:
		return fmt.Errorf("COVE_TCC_FDA_NOT_AUTHORIZED status=%s", status)
	case status == tccFDAUnknown:
		return fmt.Errorf("doctor tcc-fda: cannot verify Full Disk Access, so setup was not started\n  restart the VM with this cove build and rerun")
	}

	fmt.Println("COVE_TCC_FDA_PREAUTH_UNAVAILABLE reason='macOS grants Full Disk Access only by explicit Settings approval or an MDM-installed PPPC profile'")
	if err := openGUI(client); err != nil {
		return err
	}
	if err := client.SetGUICaptureBackend("auto"); err != nil && verbose {
		fmt.Printf("warning: set capture backend: %v\n", err)
	}
	if err := client.SetGUIInputBackend("auto"); err != nil && verbose {
		fmt.Printf("warning: set input backend: %v\n", err)
	}
	setup := &fdaSetup{
		ui:        client,
		recognize: recognizeFDAText,
		out:       os.Stdout,
		timeout:   opts.timeout,
		probe:     func() (tccFDAStatus, string) { return probeTCCFDA(client) },
		reopen:    func() error { return openFullDiskAccessPane(client) },
		sleep:     time.Sleep,
		now:       time.Now,
	}
	if err := setup.run(); err != nil {
		return err
	}
	reportTCCPathAccess(sock, opts.path)
	return nil
}

func parseTCCFDAArgs(args []string) (tccFDAOptions, error) {
	var opts tccFDAOptions
	fs := flag.NewFlagSet("doctor tcc-fda", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.vmName, "vm", "", "VM name")
	fs.StringVar(&opts.path, "tcc-path", "", "Guest path to report readability for after FDA verification (not FDA evidence)")
	fs.DurationVar(&opts.timeout, "timeout", 5*time.Minute, "Time to wait for interactive approval")
	fs.BoolVar(&opts.upgradeAgent, "upgrade-agent", false, "Upgrade guest agent before setup (changes its code identity)")
	fs.BoolVar(&opts.check, "check", false, "Verify only; do not open System Settings")
	fs.StringVar(&opts.exportProfile, "export-profile", "", "Write an MDM PPPC profile for the installed agent to `file` and exit")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: cove doctor tcc-fda [-check] [-tcc-path /Volumes/work] [-upgrade-agent] [-vm name]
       cove doctor tcc-fda -export-profile vz-agent.mobileconfig [-vm name]

Set up and verify Full Disk Access for /usr/local/bin/vz-agent.

Verification opens the guest system TCC database inside the root agent
process; no contents are read. A -tcc-path readdir is reported separately
and is path access evidence only.

Setup opens Privacy & Security > Full Disk Access in the guest window, adds
the agent, and turns on its switch when it can identify those controls on
the screenshot. After each input it waits for a later screenshot to show the
expected change before sending more, and never repeats an input. It hands
authentication, unidentified controls, and an on switch that still fails
verification to you. Cove never types passwords and sends no input while
the guest display is black or unreadable.

-export-profile writes a PPPC profile for the installed agent's code
requirement. macOS applies it only when delivered by user-approved MDM;
manual installation does not grant access.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("doctor tcc-fda: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func probeTCCFDA(client *ControlClient) (tccFDAStatus, string) {
	resp, err := client.SendRequest(&controlpb.ControlRequest{Type: "agent-tcc-fda-probe"})
	if err != nil {
		return tccFDAUnknown, err.Error()
	}
	if !resp.Success {
		return tccFDAUnknown, resp.Error
	}
	return parseTCCFDAProbe(resp.Data)
}

func printTCCFDAStatus(w io.Writer, status tccFDAStatus, detail string) {
	switch status {
	case tccFDAAuthorized:
		fmt.Fprintf(w, "COVE_TCC_FDA_AUTHORIZED agent=%s evidence=in-process-open target=%s\n", guestVZAgentPath, shellQuote(guestSystemTCCDB))
	case tccFDADenied:
		fmt.Fprintf(w, "COVE_TCC_FDA_DENIED agent=%s evidence=in-process-open detail=%s\n", guestVZAgentPath, shellQuote(detail))
	default:
		fmt.Fprintf(w, "COVE_TCC_FDA_UNVERIFIED agent=%s detail=%s\n", guestVZAgentPath, shellQuote(detail))
	}
}

// reportTCCPathAccess reports user-agent readability of a guest path.
// Readdir success shows path access only; it does not prove FDA.
func reportTCCPathAccess(sock, path string) {
	if path == "" {
		return
	}
	res, err := runTCCFDAProbe(sock, path)
	switch {
	case err != nil:
		fmt.Printf("COVE_TCC_PATH_UNVERIFIED path=%s detail=%s\n", shellQuote(path), shellQuote(err.Error()))
	case res.ExitCode == 0:
		fmt.Printf("COVE_TCC_PATH_READABLE path=%s evidence=user-agent-readdir\n", shellQuote(path))
	default:
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit %d", res.ExitCode)
		}
		fmt.Printf("COVE_TCC_PATH_UNREADABLE path=%s evidence=user-agent-readdir detail=%s\n", shellQuote(path), shellQuote(detail))
	}
}

// guestAgentRequirement returns the designated requirement of the
// installed guest agent, as reported by codesign in the guest.
func guestAgentRequirement(client *ControlClient) (string, error) {
	res, err := client.AgentDaemonExecTypedTimeout([]string{"/usr/bin/codesign", "-dr", "-", guestVZAgentPath}, nil, "", 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("read agent code requirement: %w", err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("read agent code requirement: %s", strings.TrimSpace(res.Stderr))
	}
	req, ok := parseDesignatedRequirement(res.Stdout + "\n" + res.Stderr)
	if !ok {
		return "", fmt.Errorf("read agent code requirement: no designated requirement in codesign output")
	}
	return req, nil
}

func parseDesignatedRequirement(out string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if req, ok := strings.CutPrefix(line, "designated => "); ok && req != "" {
			return strings.TrimSpace(req), true
		}
	}
	return "", false
}

// requirementBinding classifies a designated requirement. A requirement
// naming a cdhash matches one exact build, so a grant made against it does
// not carry over to a rebuilt agent. Any other requirement is reported as
// unqualified: whether it survives an upgrade depends on how later builds
// are signed, which cove does not verify here.
func requirementBinding(req string) string {
	if strings.Contains(req, "cdhash") {
		return "hash-bound"
	}
	return "unqualified"
}

func printTCCFDAIdentity(w io.Writer, req string) {
	binding := requirementBinding(req)
	fmt.Fprintf(w, "COVE_TCC_FDA_IDENTITY agent=%s requirement=%s binding=%s\n", guestVZAgentPath, shellQuote(req), binding)
	if binding == "hash-bound" {
		fmt.Fprintln(w, "  the requirement matches only this agent build; an approval made for it")
		fmt.Fprintln(w, "  is not expected to apply to an upgraded agent")
	}
}

func exportTCCFDAProfile(w io.Writer, path, req string) error {
	data, err := tccFDAProfile(req)
	if err != nil {
		return fmt.Errorf("doctor tcc-fda: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("doctor tcc-fda: export profile: %w", err)
	}
	fmt.Fprintf(w, "COVE_TCC_FDA_PROFILE_EXPORTED path=%s service=SystemPolicyAllFiles binding=%s\n", shellQuote(path), requirementBinding(req))
	fmt.Fprintln(w, "  this file grants nothing by itself: macOS applies PPPC payloads only from a")
	fmt.Fprintln(w, "  user-approved MDM; installing it manually is not supported")
	if requirementBinding(req) == "hash-bound" {
		fmt.Fprintln(w, "  the requirement pins this agent build's cdhash; re-export after every agent upgrade")
	}
	return nil
}

func tccFDAProfile(req string) ([]byte, error) {
	outer, err := newUUID()
	if err != nil {
		return nil, err
	}
	inner, err := newUUID()
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, tccFDAProfileTemplate, inner, xmlEscape(req), xmlEscape(guestVZAgentPath), outer), nil
}

const tccFDAProfileTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadDisplayName</key>
			<string>Cove guest agent Full Disk Access</string>
			<key>PayloadIdentifier</key>
			<string>com.tmc.cove.tcc.vz-agent.policy</string>
			<key>PayloadType</key>
			<string>com.apple.TCC.configuration-profile-policy</string>
			<key>PayloadUUID</key>
			<string>%[1]s</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>Services</key>
			<dict>
				<key>SystemPolicyAllFiles</key>
				<array>
					<dict>
						<key>Authorization</key>
						<string>Allow</string>
						<key>CodeRequirement</key>
						<string>%[2]s</string>
						<key>Comment</key>
						<string>Cove guest agent</string>
						<key>Identifier</key>
						<string>%[3]s</string>
						<key>IdentifierType</key>
						<string>path</string>
						<key>StaticCode</key>
						<false/>
					</dict>
				</array>
			</dict>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>Cove guest agent privacy preferences</string>
	<key>PayloadIdentifier</key>
	<string>com.tmc.cove.tcc.vz-agent</string>
	<key>PayloadScope</key>
	<string>System</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>%[4]s</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func openGUI(client *ControlClient) error {
	resp, err := client.SendRequest(&controlpb.ControlRequest{Type: "gui-open"})
	if err != nil {
		return fmt.Errorf("open VM GUI: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("open VM GUI: %s", resp.Error)
	}
	return nil
}

func openFullDiskAccessPane(client *ControlClient) error {
	script := `uid=$(stat -f %u /dev/console)
if [ -z "$uid" ] || [ "$uid" = 0 ]; then
	echo "no logged-in GUI user on /dev/console" >&2
	exit 1
fi
launchctl asuser "$uid" open 'x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles'`
	res, err := client.AgentDaemonExecTypedTimeout([]string{"sh", "-c", script}, nil, "", 10*time.Second)
	if err != nil {
		return fmt.Errorf("open Full Disk Access pane: %w", err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return fmt.Errorf("open Full Disk Access pane: %s", msg)
	}
	fmt.Println("COVE_TCC_FDA_UI_OPENED target='Full Disk Access'")
	return nil
}
