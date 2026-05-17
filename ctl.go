// ctl.go - CLI for interacting with VM control socket
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
	agentstate "github.com/tmc/vz-macos/internal/agent"
	pw "github.com/tmc/vz-macos/internal/password"
	"github.com/tmc/vz-macos/internal/vmconfig"
	"github.com/tmc/vz-macos/internal/vmstate"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func newCtlFlagSet() (*flag.FlagSet, *string, *time.Duration, *string, *bool, *time.Duration, *string) {
	fs := flag.NewFlagSet("ctl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	socketPath := fs.String("socket", "", "Control socket path (default: auto-detected from VM dir)")
	vmFlag := fs.String("vm", "", "VM name; resolves -socket from ~/.vz/vms/<name>/control.sock")
	timeout := fs.Duration("timeout", 10*time.Second, "Command timeout")
	outputFile := fs.String("o", "", "Output file for screenshot data (default: stdout)")
	raw := fs.Bool("raw", false, "Output raw JSON response")
	wait := fs.Duration("wait", 0, "Wait duration for agent commands (retries until agent is ready)")
	token := fs.String("token", "", "Control socket auth token (default: env or VM control.token file)")
	fs.Usage = func() {
		printCtlUsage(os.Stderr, fs)
	}
	// Resolve -vm to -socket lazily at parse time. Done in ctlCommand because
	// the flag set returns pointers parsed later.
	_ = vmFlag
	// Stash the vm flag pointer in a closure-friendly way: we return it via
	// an env var bridge here to keep the existing return signature.
	ctlVMFlag = vmFlag
	return fs, socketPath, timeout, outputFile, raw, wait, token
}

// ctlVMFlag holds the most recent -vm flag pointer so ctlCommand can resolve
// it after fs.Parse without changing newCtlFlagSet's return signature
// (which is also called from cli_help.go).
var ctlVMFlag *string

func printCtlUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `Usage: cove ctl [options] <command> [args...]

Commands:
  ping                  Test connection
  status                Get VM state and capabilities
  capabilities          Get machine-readable control protocol capabilities
  screenshot            Capture VM screen (base64 JPEG)
  screenshot -o file    Save screenshot to file
  pause                 Pause VM
  resume                Resume paused VM
  stop                  Force stop VM
  request-stop          Send ACPI power button (guest may ignore it)
  reboot-to-recovery    Stop VM and start macOS Recovery
  network-info          Get VM network info (MAC address, guest IP, mode)
  gui status            Report whether the VM is currently headed or headless
  gui open              Show the live VM window for a headless runtime
  gui close             Return the runtime to headless mode without stopping it
  gui backend <mode>    Set automation backend: auto, framebuffer, or window
  gui capture-backend <mode>  Set screenshot backend: auto, framebuffer, or window
  gui input-backend <mode>    Set input backend: auto, direct, or window
  gui terminal [--user <user>] -- <cmd>  Open a visible guest terminal
  vnc status            Report VNC server status
  debug-stub status     Report debug stub status
  disk list             List runtime storage devices
  disk swap <n> <path[:ro|rw]>  Swap a live disk-image backing
  disk resize <n> <size>  Resize live disk backing; macOS disk 0 expands APFS
  usb list              List runtime USB controllers and devices
  usb attach-storage <path[:ro|rw]> [controller]  Attach USB mass storage
  usb attach-host-service <id> [controller]       Attach host USB by service ID
  usb attach-host-location <id> [controller]      Attach host USB by location ID
  usb detach <device> [controller]                Detach runtime USB device
  memory info           Get memory balloon info
  memory set <GB>       Set memory target (for example: memory set 8)
  power status          Show guest macOS sleep/display-sleep settings
  power keep-awake      Disable guest display sleep, system sleep, and screen saver
  power allow-sleep [minutes]  Restore guest sleep timers (default: 10)
  snapshot list         List snapshots
  snapshot save <name>  Save snapshot (sync; blocks until complete)
  snapshot save -async <name>  Save snapshot in background; prints op id
  snapshot restore <name> Restore snapshot
  snapshot delete <name>  Delete snapshot
  operations get <id>   Print state of a long-running operation
  operations list       List all known long-running operations
  operations wait <id>  Block until <id> reaches succeeded or failed
  key <keycode> [down|up]  Send keyboard event
  mouse <x> <y> <action>   Send mouse event (action: move|down|up|click)
  text <string>         Type text string
  detect                Detect current screen state (setup_assistant, login, desktop)
  ocr [-region <spec>]  Run OCR on current screen (spec: menu or x1,y1,x2,y2)
  click-text [options] <text>  Find text via OCR and click its center
                        options: -region <spec> -timeout <duration>
  click-menu [options] <menu> <item>  Click menu title, then menu item
                        options: -timeout <duration>
  shared-folders-apply  Reload shared_folders.json into a running VM
  shared-folders-runtime-status  Report runtime shared-folder support
  boot-script <file>    Execute a vzscript automation file
  step                  Interactive step-through mode for Setup Assistant debugging
  setup-assist <user> <pass>  Run Setup Assistant automation

iTerm2 API proxy (WebSocket-to-vsock):
  iterm2-proxy [--port N]     Start WebSocket proxy to guest iTerm2 (default: 1913)
  iterm2-proxy-stop           Stop the iTerm2 proxy
  iterm2-proxy-status         Check proxy status

Guest agent (gRPC over vsock):
  agent-connect         Connect to guest agent
  agent-ping            Ping guest agent
  agent-info            Get guest system info
  exec <cmd> [args...]        Run command in guest (auto-routed by path)
  exec --daemon <cmd>         Run command via root daemon instead
  agent-exec <cmd> [args...]  Legacy alias for exec
  agent-exec-stream <cmd> [args...]  Stream command output (auto-routed by path)
  agent-exec-stream --daemon <cmd>   Stream via root daemon instead
  agent-cp <host> <guest>     Copy file host→guest (streaming)
  agent-cp -from-guest <guest> <host>  Copy file guest→host
  agent-read <path>     Read file from guest (base64)
  agent-write <path> <data>   Write data to file in guest
  agent-shutdown [force]      Graceful guest shutdown
  agent-reboot                Reboot guest
  agent-sshd <on|off|start|stop|enable|status>  Manage SSH remote login
  agent-mount-volumes         Mount tagged VirtioFS volumes in guest
  agent-status                Agent health status (daemon + user agent)
  ready [--require <names>]   Run readiness checks (xcode-cli, go, homebrew, ...)
                              Exit 0 = all pass, 1 = some failed, 2 = agent unreachable

Port forwarding:
  port-forward start <hostPort:guestPort>  Forward host TCP to guest vsock
  port-forward stop <hostPort>             Stop a port forward
  port-forward list                        List active port forwards

VM management:
  reset-password <user> <pass>  Reset user password (agent if running, disk if stopped)

Options:
`)
	if fs != nil {
		fs.PrintDefaults()
	}
	fmt.Fprintf(w, `
Examples:
  cove ctl ping
  cove ctl status
  cove ctl gui status
  cove ctl gui open
  cove ctl vnc status
  cove ctl debug-stub status
  cove ctl disk list
  cove ctl disk swap 0 /tmp/other.img:ro
  cove ctl disk resize 0 96G      # macOS disk 0 also grows the APFS container
  cove ctl usb list
  cove ctl usb attach-storage /tmp/installer.iso:ro
  cove ctl screenshot -o screen.jpg
  cove ctl key 36 down    # Press Return
  cove ctl key 36 up      # Release Return
  cove ctl mouse 0.5 0.5 click  # Click center
  cove ctl text "hello"
  cove ctl click-text -region menu Utilities
  cove ctl click-menu Utilities Terminal
  cove ctl step
  cove ctl -wait 60s agent-ping
  cove ctl exec ls /tmp
  cove ctl exec --daemon whoami
  cove ctl -vm smoke ping
  cove ctl ready --require xcode-cli,go,homebrew
  cove ctl ready --require go --json
`)
}

// extractCtlSubcommandFlags pulls the ctl-level flags ("-o <file>", "--daemon")
// from subArgs and reports whether --daemon was set. Scanning stops at the
// conventional "--" separator (which is consumed) so flag-shaped values that
// follow it are passed through verbatim to the subcommand's payload — for
// example "agent-exec -- ls --color" forwards "ls" "--color" to the guest
// instead of stealing --color or treating "--" itself as the command name.
func extractCtlSubcommandFlags(subArgs []string, outputFile *string) ([]string, bool) {
	useDaemon := false
	out := subArgs[:0]
	stop := false
	for i := 0; i < len(subArgs); i++ {
		if stop {
			out = append(out, subArgs[i])
			continue
		}
		switch {
		case subArgs[i] == "--":
			stop = true
		case subArgs[i] == "-o" && i+1 < len(subArgs):
			*outputFile = subArgs[i+1]
			i++
		case subArgs[i] == "--daemon" || subArgs[i] == "-daemon":
			useDaemon = true
		default:
			out = append(out, subArgs[i])
		}
	}
	return out, useDaemon
}

func parseCtlScreenshotArgs(subArgs []string, outputFile *string) (string, error) {
	format := "jpeg"
	var positional []string
	for i := 0; i < len(subArgs); i++ {
		arg := subArgs[i]
		value := ""
		switch {
		case arg == "-format" || arg == "--format":
			if i+1 >= len(subArgs) {
				return "", fmt.Errorf("screenshot -format requires png, jpeg, or jpg")
			}
			value = subArgs[i+1]
			i++
		case strings.HasPrefix(arg, "-format="):
			value = strings.TrimPrefix(arg, "-format=")
		case strings.HasPrefix(arg, "--format="):
			value = strings.TrimPrefix(arg, "--format=")
		default:
			positional = append(positional, arg)
			continue
		}

		switch strings.ToLower(value) {
		case "png":
			format = "png"
		case "jpeg", "jpg":
			format = "jpeg"
		default:
			return "", fmt.Errorf("screenshot -format must be png, jpeg, or jpg")
		}
	}
	if len(positional) > 0 && *outputFile == "" {
		*outputFile = positional[0]
	}
	return format, nil
}

// ctlCommand handles the "ctl" subcommand for control socket interaction
func ctlCommand(args []string) error {
	fs, socketPath, timeout, outputFile, raw, wait, token := newCtlFlagSet()

	if len(args) > 0 && isHelpArg(args[0]) {
		fs.Usage()
		return nil
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// If a VM was selected and -socket was not, resolve the socket from the VM dir.
	if *socketPath == "" {
		switch {
		case ctlVMFlag != nil && *ctlVMFlag != "":
			dir, err := requireExistingVMForControl(*ctlVMFlag)
			if err != nil {
				return err
			}
			*socketPath = GetControlSocketPathForVM(dir)
		case strings.TrimSpace(vmName) != "":
			dir, err := requireExistingVMForControl(vmName)
			if err != nil {
				return err
			}
			*socketPath = GetControlSocketPathForVM(dir)
		}
	}

	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("command required")
	}

	// Extract subcommand. Copy remaining args so flag extraction can safely
	// modify the slice without aliasing fs.Args()'s backing array.
	cmdType := fs.Arg(0)
	subArgs := append([]string{}, fs.Args()[1:]...)
	if cmdType == "help" {
		fs.Usage()
		return nil
	}
	if ctlVMFlag != nil && *ctlVMFlag == "" && *socketPath == "" && len(subArgs) > 0 {
		if _, ok := vmconfig.ExistingPath(cmdType); ok {
			return fmt.Errorf("unknown ctl command %q; did you mean: cove ctl -vm %s %s", cmdType, cmdType, strings.Join(subArgs, " "))
		}
	}
	if cmdType == "exec" {
		cmdType = "agent-exec"
	}

	// Subcommands that own their flag parsing (including --daemon, -o, --) get
	// the raw subArgs before the generic strippers below mangle them.
	if cmdType == "ready" {
		sock := *socketPath
		if sock == "" {
			sock = GetControlSocketPath()
		}
		if strings.TrimSpace(*token) != "" {
			os.Setenv(controlTokenEnvVar, strings.TrimSpace(*token))
		}
		return ctlReady(sock, subArgs)
	}

	subArgs, useDaemon := extractCtlSubcommandFlags(subArgs, outputFile)

	// Determine socket path
	sock := *socketPath
	if sock == "" {
		sock = GetControlSocketPath()
	}
	if strings.TrimSpace(*token) != "" {
		os.Setenv(controlTokenEnvVar, strings.TrimSpace(*token))
	}

	switch cmdType {
	case "detect":
		return ctlDetectScreen(sock)
	case "ocr":
		region, err := parseOCROptions(subArgs)
		if err != nil {
			return err
		}
		return ctlOCR(sock, region)
	case "click-text":
		text, region, clickTimeout, err := parseClickTextOptions(subArgs)
		if err != nil {
			return err
		}
		return ctlClickText(sock, text, region, clickTimeout)
	case "click-menu":
		menu, item, clickTimeout, err := parseClickMenuOptions(subArgs)
		if err != nil {
			return err
		}
		return ctlClickMenu(sock, menu, item, clickTimeout)
	case "boot-script":
		if len(subArgs) < 1 {
			return fmt.Errorf("boot-script requires a script file path")
		}
		return ctlBootScript(sock, subArgs[0])
	case "step":
		return ctlStepMode(sock, *outputFile)
	case "setup-assist":
		if len(subArgs) < 2 {
			return fmt.Errorf("setup-assist requires: <username> <password>")
		}
		return ctlSetupAssist(sock, subArgs[0], subArgs[1])
	case "iterm2-proxy":
		return ctlITerm2Proxy(sock, subArgs, *raw)
	case "iterm2-proxy-stop":
		return ctlITerm2ProxyCommand(sock, "iterm2-proxy-stop", *raw)
	case "iterm2-proxy-status":
		return ctlITerm2ProxyCommand(sock, "iterm2-proxy-status", *raw)
	case "gui":
		if len(subArgs) < 1 {
			return fmt.Errorf("gui requires action: status, open, close, backend, capture-backend, input-backend, or terminal")
		}
		action := subArgs[0]
		if isHelpArg(action) {
			printCtlUsage(os.Stderr, fs)
			return nil
		}
		switch action {
		case "status":
			return ctlSimpleCommand(sock, "gui-status", *timeout, *raw)
		case "open":
			return ctlSimpleCommand(sock, "gui-open", *timeout, *raw)
		case "close":
			return ctlSimpleCommand(sock, "gui-close", *timeout, *raw)
		case "backend":
			if len(subArgs) < 2 {
				return fmt.Errorf("gui backend requires mode: auto, framebuffer, or window")
			}
			mode, err := parseAutomationBackend(subArgs[1])
			if err != nil {
				return err
			}
			return ctlSimpleCommand(sock, "gui-backend-"+mode.String(), *timeout, *raw)
		case "capture-backend":
			if len(subArgs) < 2 {
				return fmt.Errorf("gui capture-backend requires mode: auto, framebuffer, or window")
			}
			mode, err := parseAutomationCaptureBackend(subArgs[1])
			if err != nil {
				return err
			}
			return ctlSimpleCommand(sock, "gui-capture-backend-"+mode.String(), *timeout, *raw)
		case "input-backend":
			if len(subArgs) < 2 {
				return fmt.Errorf("gui input-backend requires mode: auto, direct, or window")
			}
			mode, err := parseAutomationInputBackend(subArgs[1])
			if err != nil {
				return err
			}
			return ctlSimpleCommand(sock, "gui-input-backend-"+mode.inputString(), *timeout, *raw)
		case "terminal":
			return ctlGUITerminal(sock, subArgs[1:])
		default:
			return fmt.Errorf("unknown gui action: %s (use status, open, close, backend, capture-backend, input-backend, or terminal)", action)
		}
	case "vnc":
		if len(subArgs) < 1 {
			return fmt.Errorf("vnc requires action: status")
		}
		switch subArgs[0] {
		case "status":
			return ctlSimpleCommand(sock, "vnc-status", *timeout, *raw)
		default:
			return fmt.Errorf("unknown vnc action: %s (use status; start VNC with cove run -vnc :5901 -vnc-password <password>)", subArgs[0])
		}
	case "debug-stub":
		if len(subArgs) < 1 {
			return fmt.Errorf("debug-stub requires action: status")
		}
		switch subArgs[0] {
		case "status":
			return ctlSimpleCommand(sock, "debug-stub-status", *timeout, *raw)
		default:
			return fmt.Errorf("unknown debug-stub action: %s (use status)", subArgs[0])
		}
	case "disk":
		return ctlRuntimeDiskCommand(sock, subArgs, *timeout, *raw)
	case "usb":
		return ctlRuntimeUSBCommand(sock, subArgs, *timeout, *raw)
	case "power":
		return ctlPowerCommand(sock, subArgs, *timeout, *raw)
	case "network-info":
		return ctlNetworkInfoCommand(sock, *timeout, *raw)
	}

	// Build proto request
	req := &controlpb.ControlRequest{Type: cmdType}
	req.AuthToken = resolveControlTokenForSocket(sock)

	switch cmdType {
	case "ping", "status", "capabilities", "pause", "resume", "stop", "request-stop", "reboot-to-recovery", "boot-recovery", "shared-folders-apply", "shared-folders-runtime-status":
		// No payload needed

	case "memory":
		if len(subArgs) < 1 {
			return fmt.Errorf("memory requires action: info or set <GB>")
		}
		action := subArgs[0]
		cmd := &controlpb.MemoryCommand{Action: action}
		if action == "set" {
			if len(subArgs) < 2 {
				return fmt.Errorf("memory set requires size in GB (e.g., memory set 8)")
			}
			var gb float64
			if _, err := fmt.Sscanf(subArgs[1], "%f", &gb); err != nil {
				return fmt.Errorf("invalid size: %s", subArgs[1])
			}
			cmd.SizeGb = gb
		}
		req.Command = &controlpb.ControlRequest_Memory{Memory: cmd}

	case "snapshot":
		if len(subArgs) < 1 {
			return fmt.Errorf("snapshot requires action: list, save, restore, or delete")
		}
		action := subArgs[0]
		rest := subArgs[1:]
		// Strip optional flags from the action's positional args. Currently
		// only "save" recognises -async; flags may appear before or after
		// the snapshot name so scripts can be written either way.
		var async bool
		var positional []string
		for _, a := range rest {
			switch a {
			case "-async", "--async":
				async = true
			default:
				positional = append(positional, a)
			}
		}
		cmd := &controlpb.SnapshotCommand{Action: action, Async: async}
		if action != "list" {
			if len(positional) < 1 {
				return fmt.Errorf("snapshot %s requires a name", action)
			}
			cmd.Name = positional[0]
		}
		if async && action != "save" {
			return fmt.Errorf("snapshot %s does not support -async (only save)", action)
		}
		req.Command = &controlpb.ControlRequest_Snapshot{Snapshot: cmd}

	case "operations":
		if len(subArgs) < 1 {
			return fmt.Errorf("operations requires action: get <id>, list, or wait <id>")
		}
		action := subArgs[0]
		switch action {
		case "list":
			req.Command = &controlpb.ControlRequest_Operations{
				Operations: &controlpb.OperationsCommand{Action: "list"},
			}
		case "get", "wait":
			if len(subArgs) < 2 {
				return fmt.Errorf("operations %s requires an op id", action)
			}
			// "wait" is a CLI-side polling loop; on the wire we send "get".
			req.Command = &controlpb.ControlRequest_Operations{
				Operations: &controlpb.OperationsCommand{Action: "get", Id: subArgs[1]},
			}
			if action == "wait" {
				return runOperationsWait(sock, subArgs[1], *timeout, *raw, *outputFile)
			}
		default:
			return fmt.Errorf("unknown operations action: %s", action)
		}

	case "screenshot":
		format, err := parseCtlScreenshotArgs(subArgs, outputFile)
		if err != nil {
			return err
		}
		req.Command = &controlpb.ControlRequest_Screenshot{
			Screenshot: &controlpb.ScreenshotCommand{
				Scale:   0.5,
				Quality: 60,
				Format:  format,
			},
		}

	case "key":
		if len(subArgs) < 1 {
			return fmt.Errorf("key command requires keycode or key name (e.g., return, tab, space)")
		}
		// Try key name first (return, tab, space, etc.), fall back to numeric keycode
		keySpec := subArgs[0]
		keycode := keyNameToCode(keySpec)
		if keycode == 0 && keySpec != "a" {
			// Not a known name and not "a" — try numeric
			var numCode int
			if _, err := fmt.Sscanf(keySpec, "%d", &numCode); err != nil {
				return fmt.Errorf("unknown key: %q (use a name like return, tab, space, or a numeric keycode)", keySpec)
			}
			keycode = uint16(numCode)
		}
		// Support explicit "down"/"up" for individual events, otherwise send
		// a complete press (down + up) as two sequential requests.
		if len(subArgs) >= 2 && (subArgs[1] == "down" || subArgs[1] == "up") {
			keyDown := subArgs[1] == "down"
			req.Command = &controlpb.ControlRequest_Key{
				Key: &controlpb.KeyCommand{
					KeyCode: uint32(keycode),
					KeyDown: keyDown,
				},
			}
		} else {
			// Full key press: send down, then up as two requests.
			downReq := &controlpb.ControlRequest{
				Type: "key",
				Command: &controlpb.ControlRequest_Key{
					Key: &controlpb.KeyCommand{
						KeyCode: uint32(keycode),
						KeyDown: true,
					},
				},
			}
			resp, err := ctlSendRequest(sock, downReq, *timeout, "key")
			if err != nil {
				return fmt.Errorf("key down: %w", err)
			}
			if !resp.Success {
				return fmt.Errorf("key down: %s", resp.Error)
			}
			time.Sleep(50 * time.Millisecond)
			// Now send key up
			req.Command = &controlpb.ControlRequest_Key{
				Key: &controlpb.KeyCommand{
					KeyCode: uint32(keycode),
					KeyDown: false,
				},
			}
		}

	case "mouse":
		if len(subArgs) < 3 {
			return fmt.Errorf("mouse command requires: x y action")
		}
		var x, y float64
		fmt.Sscanf(subArgs[0], "%f", &x)
		fmt.Sscanf(subArgs[1], "%f", &y)
		action := subArgs[2]
		req.Command = &controlpb.ControlRequest_Mouse{
			Mouse: &controlpb.MouseCommand{
				X:      x,
				Y:      y,
				Action: action,
			},
		}

	case "text":
		if len(subArgs) < 1 {
			return fmt.Errorf("text command requires string")
		}
		req.Command = &controlpb.ControlRequest_Text{
			Text: &controlpb.TextCommand{
				Text: strings.Join(subArgs, " "),
			},
		}

	// Agent commands
	case "agent-connect", "agent-ping", "agent-info", "agent-reboot", "agent-mount-volumes", "agent-status":
		// No payload needed

	case "agent-exec":
		if len(subArgs) < 1 {
			return fmt.Errorf("exec requires at least one argument")
		}
		if !useDaemon {
			req.Type = "agent-exec-auto"
		}
		req.Command = &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: subArgs,
			},
		}

	case "agent-exec-auto":
		return fmt.Errorf("agent-exec-auto is an internal control request; use \"exec\" or \"agent-exec\"")

	case "agent-read":
		if len(subArgs) < 1 {
			return fmt.Errorf("agent-read requires a file path")
		}
		req.Command = &controlpb.ControlRequest_AgentRead{
			AgentRead: &controlpb.AgentFileReadCommand{
				Path: subArgs[0],
			},
		}

	case "agent-write":
		if len(subArgs) < 2 {
			return fmt.Errorf("agent-write requires: <path> <data>")
		}
		req.Command = &controlpb.ControlRequest_AgentWrite{
			AgentWrite: &controlpb.AgentFileWriteCommand{
				Path: subArgs[0],
				Data: base64.StdEncoding.EncodeToString([]byte(subArgs[1])),
			},
		}

	case "agent-shutdown":
		req.Command = &controlpb.ControlRequest_AgentShutdown{
			AgentShutdown: &controlpb.AgentShutdownCommand{
				Force: len(subArgs) > 0 && subArgs[0] == "force",
			},
		}

	case "agent-sshd":
		if len(subArgs) < 1 {
			return fmt.Errorf("agent-sshd requires: on, off, start, stop, enable, or status")
		}
		action := subArgs[0]
		switch action {
		case "on", "off", "start", "stop", "enable", "status":
		default:
			return fmt.Errorf("agent-sshd action must be on, off, start, stop, enable, or status")
		}
		req.Command = &controlpb.ControlRequest_AgentSshd{
			AgentSshd: &controlpb.AgentSSHDCommand{
				Action: action,
			},
		}

	case "agent-cp":
		if len(subArgs) < 2 {
			return fmt.Errorf("agent-cp requires: <host-path> <guest-path> or -from-guest <guest-path> <host-path>")
		}
		toGuest := true
		if subArgs[0] == "-from-guest" {
			toGuest = false
			subArgs = subArgs[1:]
			if len(subArgs) < 2 {
				return fmt.Errorf("agent-cp -from-guest requires: <guest-path> <host-path>")
			}
		}
		var hostPath, guestPath string
		if toGuest {
			hostPath = subArgs[0]
			guestPath = subArgs[1]
		} else {
			guestPath = subArgs[0]
			hostPath = subArgs[1]
		}
		// Resolve relative host paths.
		if !filepath.IsAbs(hostPath) {
			wd, _ := os.Getwd()
			hostPath = filepath.Join(wd, hostPath)
		}
		req.Command = &controlpb.ControlRequest_AgentCp{
			AgentCp: &controlpb.AgentCopyCommand{
				HostPath:  hostPath,
				GuestPath: guestPath,
				ToGuest:   toGuest,
			},
		}
		*timeout = 10 * time.Minute // large file default

	case "agent-exec-stream":
		if len(subArgs) < 1 {
			return fmt.Errorf("agent-exec-stream requires at least one argument")
		}
		if !useDaemon {
			req.Type = "agent-exec-stream-auto"
		}
		req.Command = &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: subArgs,
			},
		}
		if *wait > 0 {
			pingReq := &controlpb.ControlRequest{Type: "agent-ping"}
			if err := ctlAgentWithRetry(sock, pingReq, *wait, *timeout, false); err != nil {
				return err
			}
		}
		return ctlExecStream(sock, req, *timeout)

	case "port-forward":
		if len(subArgs) < 1 {
			return fmt.Errorf("usage: ctl port-forward <start hostPort:guestPort | stop hostPort | list>")
		}
		action := subArgs[0]
		pfCmd := &controlpb.PortForwardCommand{Action: action}
		switch action {
		case "start":
			if len(subArgs) < 2 {
				return fmt.Errorf("usage: ctl port-forward start <hostPort:guestPort>")
			}
			parts := strings.SplitN(subArgs[1], ":", 2)
			if len(parts) != 2 {
				return fmt.Errorf("expected hostPort:guestPort, got %q", subArgs[1])
			}
			hp, hpErr := strconv.ParseUint(parts[0], 10, 32)
			if hpErr != nil {
				return fmt.Errorf("invalid host port: %w", hpErr)
			}
			gp, gpErr := strconv.ParseUint(parts[1], 10, 32)
			if gpErr != nil {
				return fmt.Errorf("invalid guest port: %w", gpErr)
			}
			pfCmd.HostPort = uint32(hp)
			pfCmd.GuestPort = uint32(gp)
		case "stop":
			if len(subArgs) < 2 {
				return fmt.Errorf("usage: ctl port-forward stop <hostPort>")
			}
			hp, parseErr := strconv.ParseUint(subArgs[1], 10, 32)
			if parseErr != nil {
				return fmt.Errorf("invalid host port: %w", parseErr)
			}
			pfCmd.HostPort = uint32(hp)
		case "list":
			// no extra args
		default:
			return fmt.Errorf("unknown port-forward action: %s (use start, stop, or list)", action)
		}
		req.Command = &controlpb.ControlRequest_PortForward{
			PortForward: pfCmd,
		}

	case "reset-password":
		if len(subArgs) < 2 {
			return fmt.Errorf("usage: ctl reset-password <username> <new-password>")
		}
		return ctlResetPassword(sock, *timeout, subArgs[0], subArgs[1])

	default:
		return fmt.Errorf("unknown command: %s\nRun 'cove ctl help' for usage.", cmdType)
	}

	// For agent commands with -wait, retry until agent is ready
	isAgentCmd := strings.HasPrefix(cmdType, "agent-")
	if isAgentCmd && *wait > 0 {
		return ctlAgentWithRetry(sock, req, *wait, *timeout, *raw)
	}

	// Send request
	resp, err := ctlSendRequest(sock, req, *timeout, cmdType)
	if err != nil {
		return err
	}
	if !resp.Success && strings.HasPrefix(cmdType, "agent-") {
		return ctlAgentCommandError(sock, cmdType, resp.Error)
	}
	if err := markAgentCapabilityForCommand(sock, cmdType, resp); err != nil && verbose {
		fmt.Printf("warning: record guest agent capability: %v\n", err)
	}

	return ctlPrintResponse(resp, cmdType, *raw, *outputFile)
}

func controlAliasArgs(kind string, args []string) []string {
	out := []string{kind}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-vm" || arg == "--vm":
			if i+1 < len(args) {
				out = append([]string{arg, args[i+1]}, out...)
				i++
				continue
			}
		case strings.HasPrefix(arg, "-vm=") || strings.HasPrefix(arg, "--vm="):
			out = append([]string{arg}, out...)
			continue
		}
		out = append(out, arg)
	}
	return out
}

func ctlSimpleCommand(sock, cmdType string, timeout time.Duration, raw bool) error {
	req := &controlpb.ControlRequest{
		Type:      cmdType,
		AuthToken: resolveControlTokenForSocket(sock),
	}
	resp, err := ctlSendRequest(sock, req, timeout, cmdType)
	if err != nil {
		return err
	}
	return ctlPrintResponse(resp, cmdType, raw, "")
}

func ctlNetworkInfoCommand(sock string, timeout time.Duration, raw bool) error {
	req := &controlpb.ControlRequest{
		Type:      "network-info",
		AuthToken: resolveControlTokenForSocket(sock),
	}
	resp, err := ctlSendRequest(sock, req, timeout, "network-info")
	if err != nil {
		return err
	}
	info := networkInfoFromResponse(resp)
	if ctlGuestIsLinux(sock) {
		client := NewControlClient(sock)
		client.SetTimeout(timeout)
		if info.GuestIp == "" {
			if res, err := client.AgentExecTypedTimeout(guestIPProbeArgs(true), nil, "", 5*time.Second); err == nil && res.GetExitCode() == 0 {
				info.GuestIp = parseGuestIP(res.GetStdout())
			}
		}
		if info.MacAddress == "" {
			info.MacAddress = linuxNetworkInfoMAC(sock, client)
		}
	}
	if info != nil {
		data, _ := protojsonMarshaler.Marshal(info)
		resp.Data = string(data)
		resp.Result = &controlpb.ControlResponse_NetworkInfo{NetworkInfo: info}
	}
	return ctlPrintResponse(resp, "network-info", raw, "")
}

func networkInfoFromResponse(resp *controlpb.ControlResponse) *controlpb.NetworkInfoResponse {
	if resp == nil {
		return nil
	}
	if info := resp.GetNetworkInfo(); info != nil {
		return info
	}
	info := &controlpb.NetworkInfoResponse{}
	if resp.Data != "" {
		_ = protojson.Unmarshal([]byte(resp.Data), info)
	}
	return info
}

func linuxNetworkInfoMAC(sock string, client *ControlClient) string {
	vmDirectory := filepath.Dir(sock)
	macPath := filepath.Join(vmDirectory, "mac.address")
	if data, err := os.ReadFile(macPath); err == nil {
		if mac := strings.TrimSpace(string(data)); mac != "" {
			return mac
		}
	}
	res, err := client.AgentExecTypedTimeout(linuxGuestMACProbeArgs(), nil, "", 5*time.Second)
	if err != nil || res.GetExitCode() != 0 {
		return ""
	}
	mac := parseGuestMAC(res.GetStdout())
	if mac == "" {
		return ""
	}
	if err := os.WriteFile(macPath, []byte(mac+"\n"), 0644); err != nil && verbose {
		fmt.Fprintf(os.Stderr, "warning: save linux MAC address: %v\n", err)
	}
	return mac
}

func ctlRuntimeDiskCommand(sock string, args []string, timeout time.Duration, raw bool) error {
	if len(args) < 1 {
		return fmt.Errorf("disk requires action: list, swap, or resize")
	}

	data := map[string]any{}
	switch args[0] {
	case "list":
		data["action"] = "list"
	case "swap":
		if len(args) < 3 {
			return fmt.Errorf("usage: ctl disk swap <index> <path[:ro|rw]>")
		}
		index, err := parseNonNegativeInt(args[1], "disk index")
		if err != nil {
			return err
		}
		spec, err := ParseUSBStorageFlag(args[2])
		if err != nil {
			return err
		}
		data["action"] = "swap"
		data["index"] = index
		data["path"] = spec.Path
		data["read_only"] = spec.ReadOnly
	case "resize":
		if len(args) < 3 {
			return fmt.Errorf("usage: ctl disk resize <index> <size>")
		}
		index, err := parseNonNegativeInt(args[1], "disk index")
		if err != nil {
			return err
		}
		sizeBytes, err := parseByteSize(args[2])
		if err != nil {
			return err
		}
		data["action"] = "resize"
		data["index"] = index
		data["size_bytes"] = sizeBytes
	default:
		return fmt.Errorf("unknown disk action: %s (use list, swap, or resize)", args[0])
	}

	resp, err := ctlSendJSON(sock, map[string]any{
		"type": "disk",
		"data": data,
	}, timeout)
	if err != nil {
		return err
	}
	return ctlPrintResponse(resp, "disk", raw, "")
}

func ctlRuntimeUSBCommand(sock string, args []string, timeout time.Duration, raw bool) error {
	if len(args) < 1 {
		return fmt.Errorf("usb requires action: list, attach-storage, attach-host-service, attach-host-location, or detach")
	}

	data := map[string]any{}
	switch args[0] {
	case "list":
		data["action"] = "list"
	case "attach-storage":
		if len(args) < 2 {
			return fmt.Errorf("usage: ctl usb attach-storage <path[:ro|rw]> [controller]")
		}
		spec, err := ParseUSBStorageFlag(args[1])
		if err != nil {
			return err
		}
		controllerIndex, err := parseOptionalNonNegativeInt(args[2:], "controller index")
		if err != nil {
			return err
		}
		data["action"] = "attach-mass-storage"
		data["controller_index"] = controllerIndex
		data["path"] = spec.Path
		data["read_only"] = spec.ReadOnly
	case "attach-host-service":
		if len(args) < 2 {
			return fmt.Errorf("usage: ctl usb attach-host-service <service-id> [controller]")
		}
		serviceID, err := parseUint32(args[1], "service id")
		if err != nil {
			return err
		}
		controllerIndex, err := parseOptionalNonNegativeInt(args[2:], "controller index")
		if err != nil {
			return err
		}
		data["action"] = "attach-passthrough"
		data["controller_index"] = controllerIndex
		data["service_id"] = serviceID
	case "attach-host-location":
		if len(args) < 2 {
			return fmt.Errorf("usage: ctl usb attach-host-location <location-id> [controller]")
		}
		locationID, err := parseUint32(args[1], "location id")
		if err != nil {
			return err
		}
		controllerIndex, err := parseOptionalNonNegativeInt(args[2:], "controller index")
		if err != nil {
			return err
		}
		data["action"] = "attach-passthrough"
		data["controller_index"] = controllerIndex
		data["location_id"] = locationID
	case "detach":
		if len(args) < 2 {
			return fmt.Errorf("usage: ctl usb detach <device-index> [controller]")
		}
		deviceIndex, err := parseNonNegativeInt(args[1], "device index")
		if err != nil {
			return err
		}
		controllerIndex, err := parseOptionalNonNegativeInt(args[2:], "controller index")
		if err != nil {
			return err
		}
		data["action"] = "detach"
		data["controller_index"] = controllerIndex
		data["device_index"] = deviceIndex
	default:
		return fmt.Errorf("unknown usb action: %s (use list, attach-storage, attach-host-service, attach-host-location, or detach)", args[0])
	}

	resp, err := ctlSendJSON(sock, map[string]any{
		"type": "usb",
		"data": data,
	}, timeout)
	if err != nil {
		return err
	}
	return ctlPrintResponse(resp, "usb", raw, "")
}

func parseNonNegativeInt(value, label string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", label, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must be >= 0", label)
	}
	return n, nil
}

func parseOptionalNonNegativeInt(values []string, label string) (int, error) {
	if len(values) == 0 {
		return 0, nil
	}
	if len(values) > 1 {
		return 0, fmt.Errorf("too many arguments")
	}
	return parseNonNegativeInt(values[0], label)
}

func parseUint32(value, label string) (uint32, error) {
	n, err := strconv.ParseUint(value, 0, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", label, err)
	}
	return uint32(n), nil
}

func parseByteSize(value string) (uint64, error) {
	s := strings.TrimSpace(value)
	if s == "" {
		return 0, fmt.Errorf("size required")
	}

	i := len(s)
	for i > 0 {
		c := s[i-1]
		if (c >= '0' && c <= '9') || c == '.' {
			break
		}
		i--
	}
	numPart := strings.TrimSpace(s[:i])
	unitPart := strings.ToLower(strings.TrimSpace(s[i:]))
	if numPart == "" {
		return 0, fmt.Errorf("invalid size %q", value)
	}

	n, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", value, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}

	multiplier := float64(1)
	switch unitPart {
	case "", "b":
	case "k", "kb", "kib":
		multiplier = 1024
	case "m", "mb", "mib":
		multiplier = 1024 * 1024
	case "g", "gb", "gib":
		multiplier = 1024 * 1024 * 1024
	case "t", "tb", "tib":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown size unit %q", unitPart)
	}

	return uint64(n * multiplier), nil
}

func ctlExecStream(sock string, req *controlpb.ControlRequest, timeout time.Duration) error {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return ctlConnectError(sock, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout))

	reqToSend := req
	if req.AuthToken == "" {
		if token := resolveControlTokenForSocket(sock); token != "" {
			reqToSend = proto.Clone(req).(*controlpb.ControlRequest)
			reqToSend.AuthToken = token
		}
	}
	reqBytes, err := protojsonMarshaler.Marshal(reqToSend)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 256*1024)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("receive: %w", err)
		}

		var resp controlpb.ControlResponse
		if err := protojson.Unmarshal([]byte(line), &resp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if !resp.Success {
			if strings.HasPrefix(req.Type, "agent-") {
				return ctlAgentCommandError(sock, req.Type, resp.Error)
			}
			return fmt.Errorf("command failed: %s", resp.Error)
		}
		if resp.Data == "" {
			continue
		}

		var event struct {
			Stream   string `json:"stream"`
			Data     string `json:"data"`
			Done     bool   `json:"done"`
			ExitCode int32  `json:"exitCode"`
		}
		if err := json.Unmarshal([]byte(resp.Data), &event); err != nil {
			fmt.Println(resp.Data)
			continue
		}

		if event.Data != "" {
			chunk, err := base64.StdEncoding.DecodeString(event.Data)
			if err != nil {
				fmt.Print(event.Data)
			} else if event.Stream == "stderr" {
				_, _ = os.Stderr.Write(chunk)
			} else {
				_, _ = os.Stdout.Write(chunk)
			}
		}

		if event.Done {
			if event.ExitCode != 0 {
				return fmt.Errorf("command exited with code %d", event.ExitCode)
			}
			return nil
		}
	}
}

// ctlSendRequest sends a proto request to the control socket and returns the response.
func ctlSendRequest(sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return nil, ctlConnectError(sock, err)
	}
	defer conn.Close()

	// Screenshots may take longer and produce larger responses.
	// Text input may also be slow (80ms per character).
	readTimeout := timeout
	switch cmdType {
	case "screenshot":
		readTimeout = 30 * time.Second
	case "text":
		readTimeout = 60 * time.Second
	}
	conn.SetDeadline(time.Now().Add(readTimeout))

	// Marshal and send request
	reqToSend := req
	if req.AuthToken == "" {
		if token := resolveControlTokenForSocket(sock); token != "" {
			reqToSend = proto.Clone(req).(*controlpb.ControlRequest)
			reqToSend.AuthToken = token
		}
	}
	reqBytes, err := protojsonMarshaler.Marshal(reqToSend)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	// Read response with larger buffer for screenshots
	reader := bufio.NewReaderSize(conn, 256*1024) // 256KB buffer
	respLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, ctlReceiveError(sock, cmdType, err)
	}

	var resp controlpb.ControlResponse
	if err := protojson.Unmarshal([]byte(respLine), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &resp, nil
}

// ctlSendJSON sends a raw JSON object over the control socket and reads
// the protojson-encoded response. This is used for commands (like OCR)
// that pass parameters in a "data" field not modeled in the proto schema.
func ctlSendJSON(sock string, obj map[string]interface{}, timeout time.Duration) (*controlpb.ControlResponse, error) {
	if token := resolveControlTokenForSocket(sock); token != "" {
		obj["auth_token"] = token
	}
	reqBytes, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return nil, ctlConnectError(sock, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 256*1024)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, ctlReceiveError(sock, fmt.Sprint(obj["type"]), err)
	}

	var resp controlpb.ControlResponse
	if err := protojson.Unmarshal([]byte(respLine), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
}

func ctlReceiveError(sock, cmdType string, err error) error {
	msg := fmt.Sprintf("receive %s response", cmdType)
	if strings.HasPrefix(cmdType, "agent-") || cmdType == "exec" {
		msg += fmt.Sprintf("; guest agent may be unavailable, check: cove ctl -socket %s agent-status", sock)
	}
	return fmt.Errorf("%s: %w", msg, err)
}

func resolveControlTokenForSocket(sock string) string {
	if token := strings.TrimSpace(os.Getenv(controlTokenEnvVar)); token != "" {
		return token
	}
	tokenPath := filepath.Join(filepath.Dir(sock), controlTokenFileName)
	token, err := LoadControlTokenFromPath(tokenPath)
	if err != nil {
		return ""
	}
	return token
}

// ctlPrintResponse handles formatting and output of a control response.
func ctlPrintResponse(resp *controlpb.ControlResponse, cmdType string, raw bool, outputFile string) error {
	if raw {
		data, _ := protojsonMarshaler.Marshal(resp)
		fmt.Println(string(data))
		return nil
	}

	if !resp.Success {
		return fmt.Errorf("command failed: %s", resp.Error)
	}

	// Special handling for screenshot
	if cmdType == "screenshot" && resp.Data != "" {
		imgData, err := base64.StdEncoding.DecodeString(resp.Data)
		if err != nil {
			return fmt.Errorf("decode image: %w", err)
		}

		if outputFile != "" {
			if err := os.WriteFile(outputFile, imgData, 0644); err != nil {
				return fmt.Errorf("write file: %w", err)
			}
			fmt.Printf("Screenshot saved to %s (%d bytes)\n", outputFile, len(imgData))
		} else {
			// Write to stdout
			os.Stdout.Write(imgData)
		}
		return nil
	}

	if cmdType == "agent-sshd" {
		if rendered := formatAgentSSHDResponse(resp); rendered != "" {
			fmt.Print(rendered)
			return nil
		}
	}

	// Newer commands carry results in typed proto Result fields rather than
	// the legacy resp.Data string. Render those before falling back to Data.
	if rendered := formatOperationsResponse(resp); rendered != "" {
		fmt.Print(rendered)
		return nil
	}

	// Print response data
	if resp.Data != "" {
		// Try to pretty print JSON
		var parsed interface{}
		if err := json.Unmarshal([]byte(resp.Data), &parsed); err == nil {
			pretty, _ := json.MarshalIndent(parsed, "", "  ")
			fmt.Println(string(pretty))
		} else {
			fmt.Println(resp.Data)
		}
	} else {
		fmt.Println("OK")
	}

	return nil
}

func formatAgentSSHDResponse(resp *controlpb.ControlResponse) string {
	exitCode, stdout, stderr, ok := agentSSHDResult(resp)
	if !ok {
		return ""
	}
	status := sshdStatusFromOutput(stdout, stderr)
	if status == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\n", status)
	fmt.Fprintf(&b, "exitCode: %d\n", exitCode)
	if errText := strings.TrimSpace(stderr); errText != "" {
		fmt.Fprintf(&b, "stderr: %s\n", errText)
	}
	return b.String()
}

func agentSSHDResult(resp *controlpb.ControlResponse) (int32, string, string, bool) {
	if exec := resp.GetAgentExecResult(); exec != nil {
		return exec.GetExitCode(), exec.GetStdout(), exec.GetStderr(), true
	}
	if strings.TrimSpace(resp.Data) == "" {
		return 0, "", "", false
	}
	var result struct {
		ExitCode int32  `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &result); err != nil {
		return 0, "", "", false
	}
	return result.ExitCode, result.Stdout, result.Stderr, true
}

func sshdStatusFromOutput(stdout, stderr string) string {
	text := strings.TrimSpace(stdout)
	if text == "" {
		text = strings.TrimSpace(stderr)
	}
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "active", "inactive", "failed", "activating", "deactivating", "unknown":
			return line
		}
		if strings.HasPrefix(line, "Active:") {
			fields := strings.Fields(strings.TrimPrefix(line, "Active:"))
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

// formatOperationsResponse renders Result.Operation (single-op get/wait) or
// Result.OperationsList (operations list) as a human-readable text block.
// Returns "" when the response carries neither field, signalling the caller
// to fall through to legacy resp.Data rendering.
func formatOperationsResponse(resp *controlpb.ControlResponse) string {
	if op := resp.GetOperation(); op != nil {
		return formatOperationInfo(op)
	}
	if list := resp.GetOperationsList(); list != nil {
		ops := list.GetOperations()
		if len(ops) == 0 {
			return "no operations\n"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%-22s %-10s %-24s %s\n", "ID", "STATUS", "RESOURCE", "UPDATED")
		for _, op := range ops {
			fmt.Fprintf(&b, "%-22s %-10s %-24s %s\n", op.GetId(), op.GetStatus(), op.GetResource(), op.GetUpdatedAt())
		}
		return b.String()
	}
	return ""
}

func formatOperationInfo(op *controlpb.OperationInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id:        %s\n", op.GetId())
	fmt.Fprintf(&b, "status:    %s\n", op.GetStatus())
	fmt.Fprintf(&b, "resource:  %s\n", op.GetResource())
	if v := op.GetCreatedAt(); v != "" {
		fmt.Fprintf(&b, "created:   %s\n", v)
	}
	if v := op.GetUpdatedAt(); v != "" {
		fmt.Fprintf(&b, "updated:   %s\n", v)
	}
	if v := op.GetErrorCode(); v != "" {
		fmt.Fprintf(&b, "errorCode: %s\n", v)
	}
	if v := op.GetErrorMessage(); v != "" {
		fmt.Fprintf(&b, "error:     %s\n", v)
	}
	return b.String()
}

// ctlAgentWithRetry retries an agent command until it succeeds or the wait deadline expires.
func ctlAgentWithRetry(sock string, req *controlpb.ControlRequest, wait, timeout time.Duration, raw bool) error {
	deadline := time.Now().Add(wait)
	attempt := 0
	for {
		attempt++
		resp, err := ctlSendRequest(sock, req, timeout, req.Type)
		if err == nil && resp.Success {
			if markErr := markAgentCapabilityForCommand(sock, req.Type, resp); markErr != nil && verbose {
				fmt.Printf("warning: record guest agent capability: %v\n", markErr)
			}
			return ctlPrintResponse(resp, req.Type, raw, "")
		}

		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("guest agent not ready after %s: %w\n  possible causes:\n  - VM is still booting (try a longer -wait)\n  - agent not installed (run: cove provision-agent)\n  - agent crashed (check /var/log/vz-agent.log inside guest)", wait, err)
			}
			return fmt.Errorf("guest agent not ready after %s: %w", wait, ctlAgentCommandError(sock, req.Type, resp.Error))
		}

		if attempt == 1 {
			fmt.Fprintf(os.Stderr, "Connecting to guest agent (waiting up to %s)...\n", wait)
		}
		time.Sleep(2 * time.Second)
	}
}

func markAgentCapabilityForCommand(sock, cmdType string, resp *controlpb.ControlResponse) error {
	if resp == nil || !resp.Success {
		return nil
	}
	switch cmdType {
	case "agent-ping":
		return agentstate.MarkVerifiedForSocket(sock, agentstate.SourceRuntime)
	default:
		return nil
	}
}

func parseOCROptions(args []string) (string, error) {
	fs := flag.NewFlagSet("ocr", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	region := fs.String("region", "", "OCR region selector")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("ocr: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if _, err := ocrx.ParseSearchOptions(*region); err != nil {
		return "", err
	}
	return *region, nil
}

func parseGUITerminalOptions(args []string) (string, []string, error) {
	var user string
	for len(args) > 0 {
		switch args[0] {
		case "--":
			args = args[1:]
			if len(args) == 0 {
				return "", nil, fmt.Errorf("gui terminal requires command after --")
			}
			return user, args, nil
		case "--user", "-user":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return "", nil, fmt.Errorf("gui terminal --user requires a username")
			}
			user = args[1]
			args = args[2:]
		default:
			return user, args, nil
		}
	}
	return "", nil, fmt.Errorf("gui terminal requires command after --")
}

func ctlGUITerminal(socketPath string, args []string) error {
	user, command, err := parseGUITerminalOptions(args)
	if err != nil {
		return err
	}
	client := NewControlClient(socketPath)
	if err := launchGuestTerminal(client, user, command); err != nil {
		return err
	}
	fmt.Println("opened guest terminal")
	return nil
}

func parseClickTextOptions(args []string) (text, region string, timeout time.Duration, err error) {
	timeout = 10 * time.Second
	var textParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-region":
			if i+1 >= len(args) {
				return "", "", 0, fmt.Errorf("click-text: -region requires a value")
			}
			region = args[i+1]
			i++
		case "-timeout":
			if i+1 >= len(args) {
				return "", "", 0, fmt.Errorf("click-text: -timeout requires a value")
			}
			d, parseErr := time.ParseDuration(args[i+1])
			if parseErr != nil {
				return "", "", 0, fmt.Errorf("click-text: invalid timeout %q", args[i+1])
			}
			timeout = d
			i++
		default:
			textParts = append(textParts, args[i])
		}
	}
	if len(textParts) == 0 {
		return "", "", 0, fmt.Errorf("click-text requires text argument")
	}
	if _, err := ocrx.ParseSearchOptions(region); err != nil {
		return "", "", 0, err
	}
	return strings.Join(textParts, " "), region, timeout, nil
}

func parseClickMenuOptions(args []string) (menu, item string, timeout time.Duration, err error) {
	timeout = 10 * time.Second
	var parts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-timeout":
			if i+1 >= len(args) {
				return "", "", 0, fmt.Errorf("click-menu: -timeout requires a value")
			}
			d, parseErr := time.ParseDuration(args[i+1])
			if parseErr != nil {
				return "", "", 0, fmt.Errorf("click-menu: invalid timeout %q", args[i+1])
			}
			timeout = d
			i++
		default:
			parts = append(parts, args[i])
		}
	}
	if len(parts) != 2 {
		return "", "", 0, fmt.Errorf("click-menu requires exactly 2 arguments: <menu> <item>")
	}
	return parts[0], parts[1], timeout, nil
}

// ctlOCR runs OCR on the current screen and prints all recognized text.
func ctlOCR(socketPath, regionSpec string) error {
	client := NewControlClient(socketPath)
	client.SetTimeout(30 * time.Second)

	img, err := client.Screenshot()
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}

	ocr := ocrx.NewService(false)
	opts, err := ocrx.ParseSearchOptions(regionSpec)
	if err != nil {
		return err
	}
	observations, err := ocr.RecognizeText(img)
	if err != nil {
		return fmt.Errorf("ocr: %w", err)
	}

	if opts.Region != nil {
		filtered := observations[:0]
		for _, obs := range observations {
			if observationInSearchRegion(obs, img.Bounds(), opts.Region) {
				filtered = append(filtered, obs)
			}
		}
		observations = filtered
	}

	if len(observations) == 0 {
		fmt.Println("(no text detected)")
		return nil
	}

	for _, obs := range observations {
		bounds := img.Bounds()
		normX := float64(obs.Center.X) / float64(bounds.Dx())
		normY := float64(obs.Center.Y) / float64(bounds.Dy())
		fmt.Printf("[%.2f] %q at norm(%.3f, %.3f) px(%d, %d)\n",
			obs.Confidence, obs.Text, normX, normY, obs.Center.X, obs.Center.Y)
	}
	return nil
}

// ctlClickText finds text on screen via OCR and clicks its center.
func ctlClickText(socketPath, text, region string, timeout time.Duration) error {
	resp, err := ctlSendOCRWithRegion(socketPath, "ocr-click", text, timeout.String(), region, timeout+10*time.Second)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Println(resp.Data)
	return nil
}

func ctlClickMenu(socketPath, menu, item string, timeout time.Duration) error {
	client := NewControlClient(socketPath)
	client.SetTimeout(timeout + 10*time.Second)
	ocr := ocrx.NewService(false)
	return clickMenuItemViaClient(client, ocr, menu, item, timeout)
}

// ctlBootScript loads and executes a vzscript automation file via the control socket.
func ctlBootScript(socketPath, scriptPath string) error {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}

	if !isVZScriptAutomationFile(scriptPath, data) {
		return unsupportedAutomationScriptError(scriptPath)
	}
	fmt.Printf("Executing vzscript automation from %s\n", scriptPath)
	cfg := vzscriptConfig{
		socketPath: socketPath,
		verbose:    true,
	}
	return runVZScript(data, filepath.Base(scriptPath), cfg)
}

func activateStartupOptionsViaClient(client *ControlClient, ocr *ocrx.Service, timeout time.Duration) error {
	if err := client.SetGUIInputBackend("direct"); err != nil {
		return fmt.Errorf("set input backend direct: %w", err)
	}
	defer func() {
		_ = client.SetGUIInputBackend("auto")
	}()
	if err := client.SetGUICaptureBackend("window"); err != nil {
		return fmt.Errorf("set capture backend window: %w", err)
	}
	defer func() {
		_ = client.SetGUICaptureBackend("auto")
	}()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, err := client.Screenshot()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		width := float64(img.Bounds().Dx())
		height := float64(img.Bounds().Dy())
		if width == 0 || height == 0 {
			time.Sleep(250 * time.Millisecond)
			continue
		}

		if continueX, continueY, continueFound := ocr.FindTextWithOptions(img, "Continue", ocrx.SearchOptions{}); continueFound && continueBelongsToOptions(width, continueX) {
			if err := client.KeyPress(KeyCodeReturn); err == nil {
				time.Sleep(100 * time.Millisecond)
				return nil
			}
			time.Sleep(100 * time.Millisecond)
			return client.MouseClick(continueX/width, continueY/height)
		}

		optX, optY, found := ocr.FindTextWithOptions(img, "Options", ocrx.SearchOptions{})
		if !found {
			time.Sleep(time.Second)
			continue
		}
		if ok, err := activateStartupOptionsWithKeyboardViaClient(client, ocr); err != nil {
			return err
		} else if ok {
			return nil
		}
		for _, pt := range startupOptionsTilePoints(width, height, optX, optY) {
			if err := client.MouseClick(startupNorm(pt.X, width), startupNorm(pt.Y, height)); err != nil {
				return err
			}
			continueX, continueY, continueFound := waitForStartupOptionsContinueViaClient(client, ocr, 750*time.Millisecond)
			if continueFound {
				if err := client.MouseClick(startupNorm(continueX, width), startupNorm(continueY, height)); err == nil {
					time.Sleep(100 * time.Millisecond)
					return nil
				}
				if err := client.KeyPress(KeyCodeReturn); err == nil {
					time.Sleep(100 * time.Millisecond)
					return nil
				}
			}
		}
	}

	return fmt.Errorf("timeout activating Recovery Startup Options")
}

func activateStartupOptionsWithKeyboardViaClient(client *ControlClient, ocr *ocrx.Service) (bool, error) {
	for _, key := range []uint16{KeyCodeRightArrow, KeyCodeRightArrow} {
		if err := client.KeyPress(key); err != nil {
			return false, err
		}
		if _, _, found := waitForStartupOptionsContinueViaClient(client, ocr, 750*time.Millisecond); found {
			if err := client.KeyPress(KeyCodeReturn); err != nil {
				return false, err
			}
			time.Sleep(100 * time.Millisecond)
			return true, nil
		}
	}

	return false, nil
}

func waitForStartupOptionsContinueViaClient(client *ControlClient, ocr *ocrx.Service, timeout time.Duration) (float64, float64, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, err := client.Screenshot()
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		width := float64(img.Bounds().Dx())
		if x, y, found := ocr.FindTextWithOptions(img, "Continue", ocrx.SearchOptions{}); found && continueBelongsToOptions(width, x) {
			return x, y, true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return 0, 0, false
}

func startupNorm(v, size float64) float64 {
	if size <= 0 {
		return 0
	}
	n := v / size
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

func continueRecoveryLanguageViaClient(client *ControlClient, ocr *ocrx.Service, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	advanced := false
	for time.Now().Before(deadline) {
		img, err := client.Screenshot()
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if _, _, found := ocr.FindTextWithOptions(img, "Utilities", ocrx.MenuSearchOptions()); found {
			return nil
		}

		if normX, normY, found := ocr.FindTextNormalizedWithOptions(img, "Continue", ocrx.SearchOptions{}); found {
			if err := client.KeyPress(KeyCodeReturn); err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			next, err := client.Screenshot()
			if err == nil {
				if _, _, stillVisible := ocr.FindTextWithOptions(next, "Continue", ocrx.SearchOptions{}); stillVisible {
					if err := client.MouseClick(normX, normY); err != nil {
						return err
					}
				}
			}
			advanced = true
			time.Sleep(2 * time.Second)
			continue
		}

		if _, _, found := ocr.FindTextWithOptions(img, "Language", ocrx.SearchOptions{}); !found {
			if advanced {
				return nil
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if err := client.KeyPress(KeyCodeReturn); err != nil {
			return err
		}
		advanced = true
		time.Sleep(2 * time.Second)
	}
	if advanced {
		return fmt.Errorf("timeout leaving Recovery language page")
	}
	return nil
}

func clickMenuItemViaClient(client *ControlClient, ocr *ocrx.Service, menu, item string, timeout time.Duration) error {
	opts := ocrx.MenuSearchOptions()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Step 1: click the menu title.
		menuClicked := false
		menuPhaseDeadline := time.Now().Add(3 * time.Second)
		if menuPhaseDeadline.After(deadline) {
			menuPhaseDeadline = deadline
		}
		for time.Now().Before(menuPhaseDeadline) {
			img, err := client.Screenshot()
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			normX, normY, found := ocr.FindTextNormalizedWithOptions(img, menu, opts)
			if found {
				if err := client.MouseClick(normX, normY); err != nil {
					return err
				}
				menuClicked = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !menuClicked {
			continue
		}

		// Step 2: click the menu item while the menu is open.
		time.Sleep(250 * time.Millisecond)
		itemPhaseDeadline := time.Now().Add(2 * time.Second)
		if itemPhaseDeadline.After(deadline) {
			itemPhaseDeadline = deadline
		}
		for time.Now().Before(itemPhaseDeadline) {
			img, err := client.Screenshot()
			if err != nil {
				time.Sleep(150 * time.Millisecond)
				continue
			}
			normX, normY, found := ocr.FindTextNormalizedWithOptions(img, item, opts)
			if found {
				return client.MouseClick(normX, normY)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	return fmt.Errorf("timeout clicking menu item %q from menu %q", item, menu)
}

// ctlDetectScreen detects the current screen state
func ctlDetectScreen(socketPath string) error {
	client := NewControlClient(socketPath)
	client.SetTimeout(30 * time.Second)

	fmt.Println("Taking screenshot...")
	img, err := client.ScreenshotScaled(0.5)
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}

	// Print debug stats
	stats := DebugScreenAnalysis(img)
	fmt.Printf("Screen analysis:\n")
	fmt.Printf("  Overall brightness: %.1f\n", stats.OverallBrightness)
	fmt.Printf("  Center brightness: %.1f\n", stats.CenterBrightness)
	fmt.Printf("  Corner brightness: %.1f\n", stats.CornerBrightness)
	fmt.Printf("  Top brightness: %.1f\n", stats.TopBrightness)
	fmt.Printf("  Bottom brightness: %.1f\n", stats.BottomBrightness)
	fmt.Printf("  Colorfulness: %.1f\n", stats.Colorfulness)
	fmt.Printf("  Has gradient: %v\n", stats.HasGradient)
	fmt.Printf("  Has dock: %v\n", stats.HasDock)
	fmt.Printf("  Has login elements: %v\n", stats.HasLoginElements)

	state := DetectScreenState(img)
	fmt.Printf("Detected screen state (pixel): %s\n", state)

	// OCR-based detection
	ocr := ocrx.NewService(false)
	ocrState := DetectScreenStateOCR(img, ocr)
	fmt.Printf("Detected screen state (OCR):   %s\n", ocrState)
	effectiveState := state
	if ocrState != ScreenStateUnknown {
		effectiveState = ocrState
	}
	fmt.Printf("Detected screen state:         %s\n", effectiveState)

	// If it looks like Setup Assistant, detect the page with both methods
	if effectiveState == ScreenStateSetupAssistant {
		fullImg, err := client.Screenshot()
		if err == nil {
			page := DetectSetupAssistantPage(fullImg)
			fmt.Printf("Setup Assistant page (pixel): %s\n", page)
			ocrPage := OCRDetectSetupAssistantPage(fullImg, ocr)
			fmt.Printf("Setup Assistant page (OCR):   %s\n", ocrPage)
		}
	}

	return nil
}

// ctlResetPassword resets a user's password. If the VM is running and the
// guest agent is reachable, it uses dscl inside the guest. Otherwise it
// re-injects kcpassword via disk mount for auto-login with the new password.
func requireAgentExecSuccess(action string, resp *controlpb.ControlResponse) error {
	if resp == nil {
		return fmt.Errorf("%s: empty response", action)
	}
	if !resp.Success {
		if msg := strings.TrimSpace(resp.Error); msg != "" {
			return fmt.Errorf("%s: %s", action, msg)
		}
		return fmt.Errorf("%s: request failed", action)
	}
	if exec := resp.GetAgentExecResult(); exec != nil {
		if exec.GetExitCode() == 0 {
			return nil
		}
		return fmt.Errorf("%s: %s", action, pickReadyDetail(exec.GetStdout(), exec.GetStderr(), int(exec.GetExitCode())))
	}
	result := readyResultFromData(action, resp.Data)
	if result.OK {
		return nil
	}
	return fmt.Errorf("%s: %s", action, result.Detail)
}

func ctlResetPassword(sock string, timeout time.Duration, username, password string) error {
	return ctlResetPasswordForVM(currentVMSelection(), sock, timeout, username, password)
}

func ctlResetPasswordForVM(target vmSelection, sock string, timeout time.Duration, username, password string) error {
	if ctlGuestIsLinux(sock) {
		return ctlResetLinuxPassword(sock, timeout, username, password)
	}

	// Try agent first (VM running).
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"dscl", ".", "-passwd", "/Users/" + username, password},
			},
		},
	}
	resp, err := ctlSendRequest(sock, req, timeout, "agent-exec")
	if err == nil && resp.Success {
		if err := requireAgentExecSuccess("reset password", resp); err != nil {
			return err
		}
		// Also update kcpassword for auto-login consistency.
		refreshCmd, cmdErr := autoLoginRefreshCommand(username, password)
		if cmdErr != nil {
			return cmdErr
		}
		kcReq := &controlpb.ControlRequest{
			Type: "agent-exec",
			Command: &controlpb.ControlRequest_AgentExec{
				AgentExec: &controlpb.AgentExecCommand{
					Args: []string{"bash", "-c", refreshCmd},
				},
			},
		}
		kcResp, err := ctlSendRequest(sock, kcReq, timeout, "agent-exec")
		if err != nil {
			return fmt.Errorf("refresh autologin artifacts: %w", err)
		}
		if err := requireAgentExecSuccess("refresh autologin artifacts", kcResp); err != nil {
			return err
		}
		if err := writeLoginScreenCredentialsCache(target.Directory, loginScreenCredentials{
			Username: username,
			Password: password,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cache autologin credentials: %v\n", err)
		}
		fmt.Printf("Password reset for %s (via guest agent)\n", username)
		return nil
	}

	// Agent not available — try offline disk injection.
	fmt.Println("Guest agent not reachable, attempting offline password reset...")
	diskPath := target.diskPath()
	if _, statErr := os.Stat(diskPath); os.IsNotExist(statErr) {
		return fmt.Errorf("vm disk not found: %s", diskPath)
	}

	mountPoint, device, _, mountErr := attachAndMountDataVolume(diskPath)
	if mountErr != nil {
		return fmt.Errorf("mount data volume: %w", mountErr)
	}
	defer detachDiskForPath(device, diskPath)

	// Update kcpassword.
	kcData := pw.EncodeKC(password)
	kcPath := filepath.Join(mountPoint, "private", "etc", "kcpassword")
	if writeErr := os.WriteFile(kcPath, kcData, 0600); writeErr != nil {
		return fmt.Errorf("write kcpassword: %w", writeErr)
	}
	fmt.Printf("Updated kcpassword for auto-login at: %s\n", kcPath)

	// Update loginwindow.plist autoLoginUser.
	lwPath := filepath.Join(mountPoint, "Library", "Preferences", "com.apple.loginwindow.plist")
	lwData, err := pw.EncodeLoginWindowPlist(pw.CreateLoginWindowPlist(username))
	if err != nil {
		return fmt.Errorf("encode loginwindow plist: %w", err)
	}
	if writeErr := os.WriteFile(lwPath, lwData, 0644); writeErr != nil {
		return fmt.Errorf("write loginwindow.plist: %w", writeErr)
	}

	// Write a LaunchDaemon that resets the password on next boot via dscl.
	escapedUserPath := shellEscape("/Users/" + username)
	escapedPassword := shellEscape(password)
	script := fmt.Sprintf(`#!/bin/bash
dscl . -passwd %s %s
rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.pwreset.plist /var/db/vz-pwreset.sh
`, escapedUserPath, escapedPassword)
	scriptPath := filepath.Join(mountPoint, "private", "var", "db", "vz-pwreset.sh")
	if writeErr := os.WriteFile(scriptPath, []byte(script), 0755); writeErr != nil {
		return fmt.Errorf("write reset script: %w", writeErr)
	}

	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.github.tmc.vz-macos.pwreset</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>/var/db/vz-pwreset.sh</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>`
	plistPath := filepath.Join(mountPoint, "Library", "LaunchDaemons", "com.github.tmc.vz-macos.pwreset.plist")
	if writeErr := os.WriteFile(plistPath, []byte(plist), 0644); writeErr != nil {
		return fmt.Errorf("write plist: %w", writeErr)
	}

	// Fix ownership if running as root.
	if os.Getuid() == 0 {
		os.Chown(scriptPath, 0, 0)
		os.Chown(plistPath, 0, 0)
		os.Chown(kcPath, 0, 0)
		os.Chown(lwPath, 0, 0)
	} else {
		// Set root:wheel ownership on the password reset files so launchd loads them.
		fmt.Println()
		fmt.Println("Administrator privileges required to set root:wheel ownership")
		fmt.Printf("on password reset files so launchd will load them on next boot.\n")
		fmt.Println()
		em := &elevatedManifest{
			ChownFiles: []elevatedChown{
				{Path: scriptPath, Owner: "root:wheel"},
				{Path: plistPath, Owner: "root:wheel"},
				{Path: kcPath, Owner: "root:wheel"},
				{Path: lwPath, Owner: "root:wheel"},
			},
		}
		if err := runElevated(em, elevationPrompt(
			fmt.Sprintf("Reset password on VM %q.", target.elevationLabel()),
		)); err != nil {
			fmt.Fprintf(os.Stderr, "ownership fix failed: %v\n", err)
		}
	}

	if err := writeLoginScreenCredentialsCache(target.Directory, loginScreenCredentials{
		Username: username,
		Password: password,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cache autologin credentials: %v\n", err)
	}

	fmt.Printf("Password reset staged for %s (will apply on next boot)\n", username)
	return nil
}

func ctlResetLinuxPassword(sock string, timeout time.Duration, username, password string) error {
	checkReq := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"id", "-u", username},
			},
		},
	}
	checkResp, err := ctlSendRequest(sock, checkReq, timeout, "agent-exec")
	if err != nil {
		return fmt.Errorf("linux reset-password requires a running guest agent: %w", err)
	}
	if err := requireLinuxUserExists(username, checkResp); err != nil {
		return err
	}

	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"sh", "-lc", linuxResetPasswordScript(username, password)},
			},
		},
	}
	resp, err := ctlSendRequest(sock, req, timeout, "agent-exec")
	if err != nil {
		return fmt.Errorf("linux reset-password requires a running guest agent: %w", err)
	}
	if err := requireAgentExecSuccess("reset password", resp); err != nil {
		return err
	}
	fmt.Printf("Password reset for %s (via Linux guest agent)\n", username)
	return nil
}

func requireLinuxUserExists(username string, resp *controlpb.ControlResponse) error {
	if resp == nil {
		return fmt.Errorf("check linux user: empty response")
	}
	if !resp.Success {
		if msg := strings.TrimSpace(resp.Error); msg != "" {
			return fmt.Errorf("check linux user: %s", msg)
		}
		return fmt.Errorf("check linux user: request failed")
	}
	if exec := resp.GetAgentExecResult(); exec != nil {
		if exec.GetExitCode() == 0 {
			return nil
		}
		return linuxUserMissingError(username)
	}
	result := readyResultFromData("check linux user", resp.Data)
	if result.OK {
		return nil
	}
	return linuxUserMissingError(username)
}

func linuxUserMissingError(username string) error {
	return fmt.Errorf("user %q does not exist on this VM; create it first with cove ctl agent-exec --daemon -- useradd -m %s", username, username)
}

func linuxResetPasswordScript(username, password string) string {
	return fmt.Sprintf("printf %%s\\\\n %s | chpasswd", shellEscape(username+":"+password))
}

func autoLoginRefreshCommand(username, password string) (string, error) {
	loginWindowData, err := pw.EncodeLoginWindowPlist(pw.CreateLoginWindowPlist(username))
	if err != nil {
		return "", fmt.Errorf("encode loginwindow plist: %w", err)
	}
	kcpasswordB64 := shellEscape(base64.StdEncoding.EncodeToString(pw.EncodeKC(password)))
	loginWindowB64 := shellEscape(base64.StdEncoding.EncodeToString(loginWindowData))
	return fmt.Sprintf(
		"printf %%s %s | base64 -D > /etc/kcpassword && chmod 600 /etc/kcpassword && chown root:wheel /etc/kcpassword && "+
			"printf %%s %s | base64 -D > /Library/Preferences/com.apple.loginwindow.plist && "+
			"chmod 644 /Library/Preferences/com.apple.loginwindow.plist && "+
			"chown root:wheel /Library/Preferences/com.apple.loginwindow.plist",
		kcpasswordB64,
		loginWindowB64,
	), nil
}

// ctlSetupAssist runs the Setup Assistant automation
func ctlSetupAssist(socketPath, username, password string) error {
	if ctlGuestIsLinux(socketPath) {
		return fmt.Errorf("setup-assist is only supported for macOS guests; for Linux use cloud-init or cove up provisioning")
	}

	fmt.Println("=== Setup Assistant Automation ===")
	fmt.Printf("Username: %s\n", username)
	fmt.Println()

	client := NewControlClient(socketPath)
	client.SetTimeout(10 * time.Second)
	if _, err := client.sendRequest(&controlpb.ControlRequest{Type: "gui-input-backend-direct"}); err != nil {
		fmt.Printf("warning: set gui input backend to direct: %v\n", err)
	}

	// Create debug screenshot directory
	saveDir := filepath.Join(".", "setup_assistant_debug")
	fmt.Printf("Debug screenshots will be saved to: %s\n", saveDir)
	fmt.Println()

	assistant := NewSetupAssistant(SetupAssistantOptions{
		SocketPath: socketPath,
		Username:   username,
		Password:   password,
		Admin:      true,
		Verbose:    true,
		SaveDir:    saveDir,
	})

	if err := assistant.Run(); err != nil {
		return fmt.Errorf("setup assistant failed: %w\n  check whether the VM stopped: cove list\n  if it is still running, inspect the last screen: cove ctl -socket %s gui status", err, socketPath)
	}

	success, err := assistant.VerifyProvisioning()
	if err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}

	if success {
		fmt.Println("\nProvisioning appears successful!")
	} else {
		fmt.Println("\nProvisioning may be incomplete. Check the VM screen.")
	}

	return nil
}

// ctlStepMode provides interactive step-through debugging for Setup Assistant
func ctlStepMode(socketPath, outputDir string) error {
	fmt.Println("=== Setup Assistant Step Mode ===")
	fmt.Println("Interactive debugging mode for Setup Assistant navigation")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  [Enter]    - Detect state and suggest action")
	fmt.Println("  r          - Press Return key")
	fmt.Println("  t          - Press Tab key")
	fmt.Println("  e          - Press Escape key")
	fmt.Println("  s          - Save screenshot")
	fmt.Println("  a <action> - Execute suggested action")
	fmt.Println("  q          - Quit")
	fmt.Println()

	client := NewControlClient(socketPath)
	client.SetTimeout(30 * time.Second)

	// Test connection
	if err := client.Ping(); err != nil {
		return fmt.Errorf("cannot connect to VM: %w", err)
	}
	fmt.Println("Connected to VM control socket")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	stepNum := 0

	for {
		// Detect current state
		fmt.Printf("--- Step %d ---\n", stepNum)

		img, err := client.Screenshot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: screenshot: %v\n", err)
		} else {
			screenState := DetectScreenState(img)
			fmt.Printf("Screen state: %s\n", screenState)

			if screenState == ScreenStateDesktop {
				fmt.Println("Reached desktop! Setup complete.")
				return nil
			}

			if screenState == ScreenStateLoginScreen {
				fmt.Println("Reached login screen.")
			}

			if screenState == ScreenStateSetupAssistant || screenState == ScreenStateUnknown {
				page := DetectSetupAssistantPage(img)
				fmt.Printf("Detected page: %s\n", page)
				suggestAction(page)
			}
		}

		fmt.Print("\nCommand (Enter=detect, r=Return, t=Tab, e=Escape, s=save, q=quit): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "", "d":
			// Just re-detect (loop continues)
			stepNum++

		case "r":
			fmt.Println("Pressing Return...")
			if err := client.KeyPress(KeyCodeReturn); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			time.Sleep(500 * time.Millisecond)
			stepNum++

		case "t":
			fmt.Println("Pressing Tab...")
			if err := client.KeyPress(KeyCodeTab); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			time.Sleep(200 * time.Millisecond)

		case "e":
			fmt.Println("Pressing Escape...")
			if err := client.KeyPress(KeyCodeEscape); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			time.Sleep(500 * time.Millisecond)
			stepNum++

		case "sp":
			fmt.Println("Pressing Space...")
			if err := client.KeyPress(KeyCodeSpace); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			time.Sleep(200 * time.Millisecond)

		case "s":
			// Save screenshot
			saveDir := outputDir
			if saveDir == "" {
				saveDir = "."
			}
			filename := filepath.Join(saveDir, fmt.Sprintf("step_%03d_%d.png", stepNum, time.Now().Unix()))
			if img != nil {
				if err := saveScreenshotPNG(img, filename); err != nil {
					fmt.Fprintf(os.Stderr, "error: saving screenshot: %v\n", err)
				} else {
					fmt.Printf("Saved: %s\n", filename)
				}
			} else {
				// Capture fresh
				img, err := client.Screenshot()
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: screenshot: %v\n", err)
				} else {
					if err := saveScreenshotPNG(img, filename); err != nil {
						fmt.Fprintf(os.Stderr, "error: saving screenshot: %v\n", err)
					} else {
						fmt.Printf("Saved: %s\n", filename)
					}
				}
			}

		case "q":
			fmt.Println("Exiting step mode.")
			return nil

		default:
			// Try parsing as text to type
			if strings.HasPrefix(input, "type ") {
				text := strings.TrimPrefix(input, "type ")
				fmt.Printf("Typing: %s\n", text)
				if err := client.TypeText(text); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
				}
			} else if strings.HasPrefix(input, "key ") {
				var keycode int
				fmt.Sscanf(strings.TrimPrefix(input, "key "), "%d", &keycode)
				fmt.Printf("Pressing key %d...\n", keycode)
				if err := client.KeyPress(uint16(keycode)); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
				}
			} else {
				fmt.Println("Unknown command. Use: Enter, r, t, e, s, q, type <text>, key <code>")
			}
		}

		fmt.Println()
	}
}

// suggestAction prints a suggested action for the detected page
func suggestAction(page string) {
	suggestions := map[string]string{
		"hello":           "Press Return to dismiss",
		"language":        "Press Return to accept default (English)",
		"country_region":  "Press Return to accept default country",
		"accessibility":   "Press Escape to skip, then Return to confirm",
		"wifi":            "Press Escape to skip (VM uses NAT)",
		"network":         "Press Escape to skip",
		"migration":       "Tab 3x to 'Not Now', then Return",
		"apple_id":        "Tab 4x to 'Set Up Later', then Return, then Return to confirm",
		"signin":          "Tab 4x to 'Set Up Later', then Return",
		"terms":           "Tab 2x to 'Agree', then Return, then Return to confirm",
		"user_account":    "This is where user account form should be filled",
		"express_setup":   "Tab to 'Customize Settings', then Return",
		"analytics":       "Space to uncheck, Tab, Space, Tab 3x, Return",
		"screen_time":     "Tab to 'Set Up Later', then Return",
		"siri":            "Tab to 'Don't Enable Siri', then Return",
		"touch_id":        "Tab to 'Set Up Later', then Return",
		"choose_look":     "Press Return to accept default",
		"appearance":      "Press Return to accept default",
		"privacy":         "Press Return to continue",
		"filevault":       "Tab to skip, then Return",
		"icloud_keychain": "Tab to skip, then Return",
		"unknown":         "Try: Return, or Tab+Return, or Escape",
	}

	if suggestion, ok := suggestions[page]; ok {
		fmt.Printf("Suggested action: %s\n", suggestion)
	} else {
		fmt.Printf("Suggested action: Try Return or Tab+Return\n")
	}
}

func ctlAgentCommandError(sock, cmdType, detail string) error {
	if ctlAgentErrorLooksGuestFailure(detail) {
		return fmt.Errorf("%s failed: %s", cmdType, detail)
	}
	state, err := ctlVMStatusState(sock, 2*time.Second)
	if err != nil || state == "" {
		return fmt.Errorf("guest agent unavailable: %s", detail)
	}

	switch vmstate.Canonical(state) {
	case "starting", "resuming", "restoring":
		return fmt.Errorf("guest agent unavailable: vm is %s (still booting)\n  retry with: cove ctl -wait 60s %s\n  details: %s", state, cmdType, detail)
	case "paused":
		return fmt.Errorf("guest agent unavailable: vm is paused\n  resume it first: cove ctl resume\n  details: %s", detail)
	case "stopped", "stopping", "error":
		return fmt.Errorf("guest agent unavailable: vm is %s\n  start it first: %s\n  details: %s", state, vmRunHintForSocket(sock), detail)
	case "running":
		return fmt.Errorf("guest agent unavailable while vm is running\n  vm may still be booting or vz-agent may not be installed/started\n  retry with: cove ctl -wait 60s %s\n  install agent if needed: cove provision-agent\n  details: %s", cmdType, detail)
	default:
		return fmt.Errorf("guest agent unavailable: vm state is %s\n  details: %s", state, detail)
	}
}

func ctlAgentErrorLooksGuestFailure(detail string) bool {
	detail = strings.ToLower(strings.TrimSpace(detail))
	if detail == "" {
		return false
	}
	for _, prefix := range []string{
		"not_found:",
		"permission_denied:",
		"already_exists:",
		"invalid_argument:",
		"failed_precondition:",
		"out_of_range:",
		"read: not_found:",
		"write: not_found:",
		"copy: not_found:",
		"read: permission_denied:",
		"write: permission_denied:",
		"copy: permission_denied:",
	} {
		if strings.HasPrefix(detail, prefix) {
			return true
		}
	}
	for _, marker := range []string{
		": no such file or directory",
		": permission denied",
		"exit status ",
		"exit code ",
	} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func ctlVMStatusState(sock string, timeout time.Duration) (string, error) {
	req := &controlpb.ControlRequest{
		Type:      "status",
		AuthToken: resolveControlTokenForSocket(sock),
	}
	resp, err := ctlSendRequest(sock, req, timeout, "status")
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("status: %s", resp.Error)
	}
	if status := resp.GetStatus(); status != nil {
		return vmstate.Canonical(status.State), nil
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(resp.Data), &parsed); err != nil {
		return "", fmt.Errorf("parse status: %w", err)
	}
	rawState, _ := parsed["state"].(string)
	return vmstate.Canonical(rawState), nil
}

// ctlConnectError wraps a control socket dial error with actionable guidance.
func ctlConnectError(sock string, err error) error {
	return formatControlSocketDialError(sock, err)
}

// saveScreenshotPNG saves an image as PNG
func saveScreenshotPNG(img image.Image, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	return png.Encode(f, img)
}

// ctlITerm2Proxy starts the iTerm2 WebSocket proxy via the control socket.
func ctlITerm2Proxy(sock string, args []string, raw bool) error {
	fs := flag.NewFlagSet("iterm2-proxy", flag.ExitOnError)
	port := fs.Int("port", iterm2DefaultPort, "WebSocket listen port")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := NewControlClient(sock)
	req := &controlpb.ControlRequest{Type: "iterm2-proxy-start"}
	// Pass port in the JSON data field via the legacy raw-JSON path.
	// The server-side handler uses the default port; for non-default
	// ports we use a custom raw JSON approach.
	if *port != iterm2DefaultPort {
		// Send raw JSON directly for port override.
		timeout := client.Timeout()
		conn, err := net.DialTimeout("unix", sock, timeout)
		if err != nil {
			return ctlConnectError(sock, err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(timeout))

		token := resolveControlTokenForSocket(sock)
		rawReq := fmt.Sprintf(`{"type":"iterm2-proxy-start","auth_token":%q,"data":{"port":%d}}`, token, *port)
		if _, err := conn.Write(append([]byte(rawReq), '\n')); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		reader := bufio.NewReader(conn)
		respLine, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if raw {
			fmt.Print(respLine)
			return nil
		}
		var resp controlpb.ControlResponse
		if err := protojson.Unmarshal([]byte(respLine), &resp); err != nil {
			return fmt.Errorf("parse: %w", err)
		}
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		fmt.Println(resp.Data)
		return nil
	}

	resp, err := client.sendRequest(req)
	if err != nil {
		return err
	}
	if raw {
		data, _ := protojson.Marshal(resp)
		fmt.Println(string(data))
		return nil
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Println(resp.Data)
	return nil
}

// runOperationsWait polls the per-VM operations registry every 500ms until
// the named op reaches a terminal state (succeeded|failed) or the per-call
// timeout is exceeded. Each poll reuses ctlSendRequest with its own dial,
// so transient socket errors during a long-running save (e.g., the VM
// briefly pauses on its dispatch queue) don't abort the wait.
func runOperationsWait(sock, opID string, timeout time.Duration, raw bool, outputFile string) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	const pollInterval = 500 * time.Millisecond
	deadline := time.Now().Add(1 * time.Hour) // wait up to an hour for huge saves; per-call timeout still applies
	for {
		req := &controlpb.ControlRequest{
			Type:      "operations",
			AuthToken: resolveControlTokenForSocket(sock),
			Command: &controlpb.ControlRequest_Operations{
				Operations: &controlpb.OperationsCommand{Action: "get", Id: opID},
			},
		}
		resp, err := ctlSendRequest(sock, req, timeout, "operations")
		if err == nil && resp.Error == "" {
			info := resp.GetOperation()
			if info != nil {
				switch info.Status {
				case "succeeded":
					return ctlPrintResponse(resp, "operations", raw, outputFile)
				case "failed":
					if err := ctlPrintResponse(resp, "operations", raw, outputFile); err != nil {
						return err
					}
					if info.ErrorMessage != "" {
						return fmt.Errorf("operation %s failed: %s", opID, info.ErrorMessage)
					}
					return fmt.Errorf("operation %s failed", opID)
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("operation %s did not complete within %v", opID, time.Since(deadline.Add(-1*time.Hour)))
		}
		time.Sleep(pollInterval)
	}
}

// ctlITerm2ProxyCommand sends a simple iterm2-proxy-* command.
func ctlITerm2ProxyCommand(sock, cmdType string, raw bool) error {
	client := NewControlClient(sock)
	resp, err := client.sendRequest(&controlpb.ControlRequest{Type: cmdType})
	if err != nil {
		return err
	}
	if raw {
		data, _ := protojson.Marshal(resp)
		fmt.Println(string(data))
		return nil
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Println(resp.Data)
	return nil
}
