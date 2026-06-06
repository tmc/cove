package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

const guestVZAgentPath = "/usr/local/bin/vz-agent"

type tccFDAOptions struct {
	vmName       string
	path         string
	password     string
	timeout      time.Duration
	upgradeAgent bool
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
	if opts.path == "" {
		return fmt.Errorf("doctor tcc-fda: -tcc-path is required")
	}
	sock := target.controlSocketPath()
	if !isVMRunning(sock) {
		return fmt.Errorf("doctor tcc-fda: VM is not running\n  start it first, for example: cove run -vm %s -headless -no-resume -auto-upgrade-agent", target.elevationLabel())
	}

	fmtTCCFDAPreAuthUnavailable()
	fmt.Println("Using guided Full Disk Access authorization. Final success is verified only by the bounded readdir probe.")

	if opts.upgradeAgent {
		fmt.Println("COVE_TCC_FDA_AGENT_UPGRADE start")
		if err := upgradeAgentAt(sock); err != nil {
			return fmt.Errorf("upgrade agent before FDA guide: %w", err)
		}
		fmt.Println("COVE_TCC_FDA_AGENT_UPGRADE complete")
		time.Sleep(5 * time.Second)
	}
	if err := waitTCCFDAUserAgent(sock, 30*time.Second); err != nil {
		return fmt.Errorf("COVE_TCC_FDA_USER_AGENT_UNAVAILABLE detail=%s", shellQuote(err.Error()))
	}

	if ok, err := probeTCCFDAReadable(sock, opts.path); err != nil {
		fmt.Printf("COVE_TCC_FDA_PROBE_INITIAL error=%s\n", shellQuote(err.Error()))
	} else if ok {
		fmt.Printf("COVE_TCC_FDA_AUTHORIZED path=%s agent=%s\n", shellQuote(opts.path), guestVZAgentPath)
		return nil
	}

	client := NewControlClient(sock)
	client.SetTimeout(30 * time.Second)
	if err := openGUI(client); err != nil {
		return err
	}
	if err := client.SetGUIInputBackend("auto"); err != nil && verbose {
		fmt.Printf("warning: set input backend: %v\n", err)
	}
	if err := client.SetGUICaptureBackend("auto"); err != nil && verbose {
		fmt.Printf("warning: set capture backend: %v\n", err)
	}

	if err := openFullDiskAccessPane(client); err != nil {
		return err
	}
	if err := driveFullDiskAccessUI(client, opts); err != nil {
		return err
	}

	deadline := time.Now().Add(opts.timeout)
	var last string
	for time.Now().Before(deadline) {
		ok, err := probeTCCFDAReadable(sock, opts.path)
		if ok {
			fmt.Printf("COVE_TCC_FDA_AUTHORIZED path=%s agent=%s\n", shellQuote(opts.path), guestVZAgentPath)
			fmt.Printf("Full Disk Access verified for %s via bounded readdir probe\n", opts.path)
			return nil
		}
		if err != nil {
			last = err.Error()
		} else {
			last = "not readable"
		}
		_ = clickIfVisible(client, "Allow", 500*time.Millisecond)
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("COVE_TCC_FDA_VERIFY_FAILED path=%s detail=%s", shellQuote(opts.path), shellQuote(last))
}

func fmtTCCFDAPreAuthUnavailable() {
	fmt.Println("COVE_TCC_FDA_PREAUTH_UNAVAILABLE reason='stock SIP macOS does not provide a silent FDA grant path without MDM PPPC enrollment'")
}

func waitTCCFDAUserAgent(sock string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		_, err := agentUserExec(sock, []string{"/usr/bin/true"}, 5*time.Second)
		if err == nil {
			fmt.Println("COVE_TCC_FDA_USER_AGENT_READY")
			return nil
		}
		last = err
		time.Sleep(2 * time.Second)
	}
	if last == nil {
		last = fmt.Errorf("timeout waiting for user agent")
	}
	return last
}

func parseTCCFDAArgs(args []string) (tccFDAOptions, error) {
	var opts tccFDAOptions
	fs := flag.NewFlagSet("doctor tcc-fda", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.vmName, "vm", "", "VM name")
	fs.StringVar(&opts.path, "tcc-path", "", "Guest /Volumes path to verify")
	fs.StringVar(&opts.password, "password", "", "Guest admin password for authorization prompts")
	fs.DurationVar(&opts.timeout, "timeout", 90*time.Second, "Time to wait for FDA verification")
	fs.BoolVar(&opts.upgradeAgent, "upgrade-agent", false, "Upgrade guest agent before guiding FDA")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: cove doctor tcc-fda -tcc-path /Volumes/work [-password pass] [-upgrade-agent] [-vm name]

Guide Full Disk Access authorization for /usr/local/bin/vz-agent, then verify
success only by the same bounded readdir probe used by cove doctor.`)
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
	time.Sleep(4 * time.Second)
	return nil
}

func driveFullDiskAccessUI(client *ControlClient, opts tccFDAOptions) error {
	_ = clickIfVisible(client, "Allow", time.Second)
	_ = clickIfVisible(client, "Full Disk Access", 2*time.Second)
	time.Sleep(time.Second)

	// The add button is not reliably exposed as OCR text. These points cover
	// the lower-left add control in the FDA app list across the current
	// System Settings layouts used by Cove test VMs.
	for _, pt := range [][2]float64{{0.36, 0.86}, {0.31, 0.86}, {0.42, 0.86}} {
		_ = client.MouseClick(pt[0], pt[1])
		time.Sleep(750 * time.Millisecond)
		if fileDialogVisible(client) {
			break
		}
	}

	if err := client.KeyPressWithModifiers(KeyCodeG, ModifierCommand|ModifierShift); err != nil {
		return fmt.Errorf("open path sheet: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := client.TypeText("/usr/local/bin"); err != nil {
		return fmt.Errorf("type agent directory: %w", err)
	}
	if err := client.KeyPress(KeyCodeReturn); err != nil {
		return fmt.Errorf("confirm agent directory: %w", err)
	}
	time.Sleep(time.Second)
	if err := client.TypeText("vz-agent"); err != nil {
		return fmt.Errorf("select agent binary: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := client.KeyPress(KeyCodeReturn); err != nil {
		return fmt.Errorf("open agent binary: %w", err)
	}
	time.Sleep(2 * time.Second)
	_ = clickIfVisible(client, "Open", 2*time.Second)
	_ = clickIfVisible(client, "Allow", 2*time.Second)

	if opts.password != "" {
		enterPasswordIfPrompted(client, opts.password)
	}

	// Some macOS versions add the binary disabled and require toggling it.
	_ = clickIfVisible(client, "vz-agent", 2*time.Second)
	if opts.password != "" {
		enterPasswordIfPrompted(client, opts.password)
	}
	return nil
}

func fileDialogVisible(client *ControlClient) bool {
	text, err := client.OCRAllText()
	if err != nil {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "applications") ||
		strings.Contains(lower, "go to the folder") ||
		strings.Contains(lower, "open")
}

func clickIfVisible(client *ControlClient, text string, timeout time.Duration) error {
	err := client.OCRClickText(text, timeout)
	if err == nil {
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

func enterPasswordIfPrompted(client *ControlClient, password string) {
	text, err := client.OCRAllText()
	if err != nil {
		return
	}
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "password") && !strings.Contains(lower, "touch id") && !strings.Contains(lower, "modify settings") {
		return
	}
	_ = clickIfVisible(client, "Use Password", time.Second)
	_ = clickIfVisible(client, "Password", time.Second)
	_ = client.TypeText(password)
	_ = client.KeyPress(KeyCodeReturn)
	time.Sleep(2 * time.Second)
}

func probeTCCFDAReadable(sock, path string) (bool, error) {
	res, err := runTCCFDAProbe(sock, path)
	if err != nil {
		return false, err
	}
	if res.ExitCode == 0 {
		return true, nil
	}
	detail := strings.TrimSpace(res.Stderr)
	if detail == "" {
		detail = fmt.Sprintf("exit %d", res.ExitCode)
	}
	return false, fmt.Errorf("%s", detail)
}
