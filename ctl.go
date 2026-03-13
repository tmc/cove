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
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// ctlCommand handles the "ctl" subcommand for control socket interaction
func ctlCommand(args []string) error {
	fs := flag.NewFlagSet("ctl", flag.ExitOnError)
	socketPath := fs.String("socket", "", "Control socket path (default: auto-detected from VM dir)")
	timeout := fs.Duration("timeout", 10*time.Second, "Command timeout")
	outputFile := fs.String("o", "", "Output file for screenshot data (default: stdout)")
	raw := fs.Bool("raw", false, "Output raw JSON response")
	wait := fs.Duration("wait", 0, "Wait duration for agent commands (retries until agent is ready)")
	token := fs.String("token", "", "Control socket auth token (default: env or VM control.token file)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos ctl [options] <command> [args...]

Commands:
  ping                  Test connection
  status                Get VM state and capabilities
  capabilities          Get machine-readable control protocol capabilities
  screenshot            Capture VM screen (base64 JPEG)
  screenshot -o file    Save screenshot to file
  pause                 Pause VM
  resume                Resume paused VM
  stop                  Force stop VM
  request-stop          Send ACPI power button (graceful shutdown)
  network-info          Get VM network info (MAC address, guest IP, mode)
  memory info           Get memory balloon info
  memory set <GB>       Set memory target (e.g., memory set 8)
  snapshot list         List snapshots
  snapshot save <name>  Save snapshot
  snapshot restore <name> Restore snapshot
  snapshot delete <name>  Delete snapshot
  key <keycode> [down|up]  Send keyboard event
  mouse <x> <y> <action>   Send mouse event (action: move|down|up|click)
  text <string>         Type text string
  detect                Detect current screen state (setup_assistant, login, desktop)
  ocr [-region <spec>]  Run OCR on current screen (spec: menu or x1,y1,x2,y2)
  click-text [options] <text>  Find text via OCR and click its center
                        options: -region <spec> -timeout <duration>
  click-menu [options] <menu> <item>  Click menu title, then menu item (menu OCR region)
                        options: -timeout <duration>
  shared-folders-apply   Reload shared_folders.json into a running VM
  boot-script <file>    Execute a boot command script file
  step                  Interactive step-through mode for Setup Assistant debugging
  setup-assist <user> <pass>  Run Setup Assistant automation

Guest Agent (via GRPC over vsock):
  agent-connect         Connect to guest agent
  agent-ping            Ping guest agent
  agent-info            Get guest system info
  agent-exec <cmd> [args...]  Run command in guest
  agent-exec-stream <cmd> [args...]  Stream stdout/stderr from guest command
  agent-cp <host> <guest>     Copy file host→guest (streaming)
  agent-cp -from-guest <guest> <host>  Copy file guest→host
  agent-read <path>     Read file from guest (base64)
  agent-write <path> <data>   Write data to file in guest
  agent-shutdown [force]      Graceful guest shutdown
  agent-reboot                Reboot guest
  agent-sshd <on|off|status>  Manage SSH remote login
  agent-mount-volumes         Mount tagged VirtioFS volumes in guest

VM Management:
  reset-password <user> <pass>  Reset user password (agent if running, disk if stopped)

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  vz-macos ctl ping
  vz-macos ctl status
  vz-macos ctl screenshot -o screen.jpg
  vz-macos ctl key 36 down    # Press Return
  vz-macos ctl key 36 up      # Release Return
  vz-macos ctl mouse 0.5 0.5 click  # Click center
  vz-macos ctl text "hello"
  vz-macos ctl click-text -region menu Utilities
  vz-macos ctl click-menu Utilities Terminal
  vz-macos ctl step           # Interactive step mode
  vz-macos ctl agent-ping     # Auto-connects to agent
  vz-macos ctl -wait 60s agent-ping  # Wait up to 60s for agent
  vz-macos ctl agent-sshd on  # Enable SSH
  vz-macos ctl agent-reboot   # Reboot guest
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("command required")
	}

	// Extract subcommand. Any remaining positional args after the subcommand
	// are subcommand-specific arguments (not ctl flags).
	cmdType := fs.Arg(0)
	subArgs := fs.Args()[1:]

	// Handle flags that appear after the subcommand (e.g. "screenshot -o file.jpg").
	// Go's flag parser stops at the first non-flag arg, so flags after the subcommand
	// name are not parsed. Scan subArgs for known ctl flags and extract them.
	for i := 0; i < len(subArgs); i++ {
		if subArgs[i] == "-o" && i+1 < len(subArgs) {
			*outputFile = subArgs[i+1]
			subArgs = append(subArgs[:i], subArgs[i+2:]...)
			i--
		}
	}

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
	}

	// Build proto request
	req := &controlpb.ControlRequest{Type: cmdType}
	req.AuthToken = resolveControlTokenForSocket(sock)

	switch cmdType {
	case "ping", "status", "capabilities", "pause", "resume", "stop", "request-stop", "network-info", "shared-folders-apply":
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
		cmd := &controlpb.SnapshotCommand{Action: action}
		if action != "list" {
			if len(subArgs) < 2 {
				return fmt.Errorf("snapshot %s requires a name", action)
			}
			cmd.Name = subArgs[1]
		}
		req.Command = &controlpb.ControlRequest_Snapshot{Snapshot: cmd}

	case "screenshot":
		// Accept positional path: "ctl screenshot /tmp/screen.jpg"
		if len(subArgs) > 0 && *outputFile == "" {
			*outputFile = subArgs[0]
		}
		req.Command = &controlpb.ControlRequest_Screenshot{
			Screenshot: &controlpb.ScreenshotCommand{
				Scale:   0.5,
				Quality: 60,
				Format:  "jpeg",
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
	case "agent-connect", "agent-ping", "agent-info", "agent-reboot", "agent-mount-volumes":
		// No payload needed

	case "agent-exec":
		if len(subArgs) < 1 {
			return fmt.Errorf("agent-exec requires at least one argument")
		}
		req.Command = &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: subArgs,
			},
		}

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
			return fmt.Errorf("agent-sshd requires: on, off, or status")
		}
		action := subArgs[0]
		if action != "on" && action != "off" && action != "status" {
			return fmt.Errorf("agent-sshd action must be on, off, or status")
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

	case "reset-password":
		if len(subArgs) < 2 {
			return fmt.Errorf("usage: ctl reset-password <username> <new-password>")
		}
		return ctlResetPassword(sock, *timeout, subArgs[0], subArgs[1])

	default:
		return fmt.Errorf("unknown command: %s", cmdType)
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

	return ctlPrintResponse(resp, cmdType, *raw, *outputFile)
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
		return nil, fmt.Errorf("receive: %w", err)
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
		return nil, fmt.Errorf("connect to %s: %w", sock, err)
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
		return nil, fmt.Errorf("receive: %w", err)
	}

	var resp controlpb.ControlResponse
	if err := protojson.Unmarshal([]byte(respLine), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
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

// ctlAgentWithRetry retries an agent command until it succeeds or the wait deadline expires.
func ctlAgentWithRetry(sock string, req *controlpb.ControlRequest, wait, timeout time.Duration, raw bool) error {
	deadline := time.Now().Add(wait)
	attempt := 0
	for {
		attempt++
		resp, err := ctlSendRequest(sock, req, timeout, req.Type)
		if err == nil && resp.Success {
			return ctlPrintResponse(resp, req.Type, raw, "")
		}

		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("guest agent not ready after %s: %w\n  possible causes:\n  - VM is still booting (try a longer -wait)\n  - agent not installed (run: vz-macos inject -agent)\n  - agent crashed (check /var/log/vz-agent.log inside guest)", wait, err)
			}
			return fmt.Errorf("guest agent not ready after %s: %s", wait, resp.Error)
		}

		if attempt == 1 {
			fmt.Fprintf(os.Stderr, "Connecting to guest agent (waiting up to %s)...\n", wait)
		}
		time.Sleep(2 * time.Second)
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
	if _, err := ParseOCRSearchOptions(*region); err != nil {
		return "", err
	}
	return *region, nil
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
	if _, err := ParseOCRSearchOptions(region); err != nil {
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

	ocr := NewOCRService(false)
	opts, err := ParseOCRSearchOptions(regionSpec)
	if err != nil {
		return err
	}
	observations, err := ocr.RecognizeText(img)
	if err != nil {
		return fmt.Errorf("OCR: %w", err)
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
	ocr := NewOCRService(false)
	return clickMenuItemViaClient(client, ocr, menu, item, timeout)
}

// ctlBootScript loads and executes a boot command script via the control socket.
func ctlBootScript(socketPath, scriptPath string) error {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}

	commands, err := ParseBootCommands(string(data))
	if err != nil {
		return fmt.Errorf("parse script: %w", err)
	}

	fmt.Printf("Executing %d boot commands from %s\n", len(commands), scriptPath)

	// The boot command executor needs a ControlServer, but from ctl we only
	// have a socket. Create a thin ControlServer with just the socket path
	// for screenshot + input passthrough. In practice, boot-script from ctl
	// is less common than from the in-process path (unattended.go), but we
	// support it for manual debugging.
	//
	// For now, use the ControlClient-based approach by translating commands
	// to client calls.
	client := NewControlClient(socketPath)
	client.SetTimeout(60 * time.Second)
	ocr := NewOCRService(true)

	for i, cmd := range commands {
		fmt.Printf("[boot] step %d/%d: <%s %s>\n", i+1, len(commands), cmd.Type, cmd.Args)
		if err := executeBootCommandViaClient(client, ocr, cmd); err != nil {
			return fmt.Errorf("step %d <%s %s>: %w", i+1, cmd.Type, cmd.Args, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// executeBootCommandViaClient executes a single boot command using a ControlClient.
func executeBootCommandViaClient(client *ControlClient, ocr *OCRService, cmd BootCommand) error {
	switch cmd.Type {
	case "wait":
		d, _ := time.ParseDuration(cmd.Args)
		time.Sleep(d)
		return nil

	case "waitForText":
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			img, err := client.Screenshot()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			_, _, found := ocr.FindText(img, cmd.Args)
			if found {
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("timeout waiting for text %q", cmd.Args)

	case "waitForMenuText":
		deadline := time.Now().Add(60 * time.Second)
		opts := OCRMenuSearchOptions()
		for time.Now().Before(deadline) {
			img, err := client.Screenshot()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			_, _, found := ocr.FindTextWithOptions(img, cmd.Args, opts)
			if found {
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("timeout waiting for menu text %q", cmd.Args)

	case "click":
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			img, err := client.Screenshot()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			normX, normY, found := ocr.FindTextNormalized(img, cmd.Args)
			if found {
				return client.MouseClick(normX, normY)
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("timeout waiting for text %q to click", cmd.Args)

	case "clickMenu":
		deadline := time.Now().Add(60 * time.Second)
		opts := OCRMenuSearchOptions()
		for time.Now().Before(deadline) {
			img, err := client.Screenshot()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			normX, normY, found := ocr.FindTextNormalizedWithOptions(img, cmd.Args, opts)
			if found {
				return client.MouseClick(normX, normY)
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("timeout waiting for menu text %q to click", cmd.Args)

	case "clickMenuItem":
		menu, item := splitMenuItemArgs(cmd.Args)
		if menu == "" || item == "" {
			return fmt.Errorf("clickMenuItem requires \"menu|item\"")
		}
		return clickMenuItemViaClient(client, ocr, menu, item, 60*time.Second)

	case "type":
		return client.TypeText(cmd.Args)

	case "typeAndReturnIfText":
		needle, value := splitConditionalTypeArgs(cmd.Args)
		if needle == "" || value == "" {
			return fmt.Errorf("typeAndReturnIfText requires \"needle|value\"")
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			img, err := client.Screenshot()
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			_, _, found := ocr.FindText(img, needle)
			if found {
				if err := client.TypeText(value); err != nil {
					return err
				}
				return client.KeyPress(36)
			}
			time.Sleep(500 * time.Millisecond)
		}
		return nil

	case "key":
		keyCode, modifiers := parseKeySpec(cmd.Args)
		if modifiers != 0 {
			return client.KeyPressWithModifiers(keyCode, modifiers)
		}
		return client.KeyPress(keyCode)

	case "screenshot":
		return nil // no-op from ctl

	default:
		return fmt.Errorf("unknown command: %s", cmd.Type)
	}
}

func clickMenuItemViaClient(client *ControlClient, ocr *OCRService, menu, item string, timeout time.Duration) error {
	opts := OCRMenuSearchOptions()
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
	ocr := NewOCRService(false)
	ocrState := DetectScreenStateOCR(img, ocr)
	fmt.Printf("Detected screen state (OCR):   %s\n", ocrState)

	// If it looks like Setup Assistant, detect the page with both methods
	if state == ScreenStateSetupAssistant || ocrState == ScreenStateSetupAssistant {
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
func ctlResetPassword(sock string, timeout time.Duration, username, password string) error {
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
		fmt.Printf("Password reset for %s (via guest agent)\n", username)
		// Also update kcpassword for auto-login consistency.
		kcReq := &controlpb.ControlRequest{
			Type: "agent-exec",
			Command: &controlpb.ControlRequest_AgentExec{
				AgentExec: &controlpb.AgentExecCommand{
					Args: []string{"bash", "-c",
						fmt.Sprintf("printf '%s' | /usr/bin/python3 -c \""+
							"import sys; key=[0x7D,0x89,0x52,0x23,0xD2,0xBC,0xDD,0xEA,0xA3,0xB9,0x1F]; "+
							"pw=sys.stdin.buffer.read(); pw+=b'\\x00'*(11-len(pw)%%11); "+
							"sys.stdout.buffer.write(bytes(b^key[i%%len(key)] for i,b in enumerate(pw)))\" > /etc/kcpassword",
							password),
					},
				},
			},
		}
		ctlSendRequest(sock, kcReq, timeout, "agent-exec")
		return nil
	}

	// Agent not available — try offline disk injection.
	fmt.Println("Guest agent not reachable, attempting offline password reset...")
	diskPath := filepath.Join(vmDir, "disk.img")
	if _, statErr := os.Stat(diskPath); os.IsNotExist(statErr) {
		return fmt.Errorf("VM disk not found: %s", diskPath)
	}

	mountPoint, device, _, mountErr := attachAndMountDataVolume(diskPath)
	if mountErr != nil {
		return fmt.Errorf("mount data volume: %w", mountErr)
	}
	defer detachDisk(device)

	// Update kcpassword.
	kcData := EncodeKCPassword(password)
	kcPath := filepath.Join(mountPoint, "private", "etc", "kcpassword")
	if writeErr := os.WriteFile(kcPath, kcData, 0600); writeErr != nil {
		return fmt.Errorf("write kcpassword: %w", writeErr)
	}
	fmt.Printf("Updated kcpassword for auto-login at: %s\n", kcPath)

	// Update loginwindow.plist autoLoginUser.
	lwPath := filepath.Join(mountPoint, "Library", "Preferences", "com.apple.loginwindow.plist")
	lwPlist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>autoLoginUser</key>
	<string>%s</string>
</dict>
</plist>`, username)
	if writeErr := os.WriteFile(lwPath, []byte(lwPlist), 0644); writeErr != nil {
		return fmt.Errorf("write loginwindow.plist: %w", writeErr)
	}

	// Write a LaunchDaemon that resets the password on next boot via dscl.
	script := fmt.Sprintf(`#!/bin/bash
dscl . -passwd /Users/%s '%s'
rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.pwreset.plist /var/db/vz-pwreset.sh
`, username, password)
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
	} else {
		fmt.Println("Note: run with sudo for proper LaunchDaemon ownership, or password will reset on next boot only if files are root:wheel.")
		// Try elevated bash for the chown.
		tmpScript, tmpErr := os.CreateTemp("", "vz-pwreset-chown-*.sh")
		if tmpErr == nil {
			fmt.Fprintf(tmpScript, "#!/bin/bash\nchown root:wheel %q %q %q\n", scriptPath, plistPath, kcPath)
			tmpScript.Close()
			os.Chmod(tmpScript.Name(), 0755)
			runElevatedBash(tmpScript.Name())
			os.Remove(tmpScript.Name())
		}
	}

	fmt.Printf("Password reset staged for %s (will apply on next boot)\n", username)
	return nil
}

// ctlSetupAssist runs the Setup Assistant automation
func ctlSetupAssist(socketPath, username, password string) error {
	fmt.Println("=== Setup Assistant Automation ===")
	fmt.Printf("Username: %s\n", username)
	fmt.Println()

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
		return fmt.Errorf("setup assistant failed: %w", err)
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
			fmt.Printf("Screenshot error: %v\n", err)
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
				fmt.Printf("error: %v\n", err)
			}
			time.Sleep(500 * time.Millisecond)
			stepNum++

		case "t":
			fmt.Println("Pressing Tab...")
			if err := client.KeyPress(KeyCodeTab); err != nil {
				fmt.Printf("error: %v\n", err)
			}
			time.Sleep(200 * time.Millisecond)

		case "e":
			fmt.Println("Pressing Escape...")
			if err := client.KeyPress(KeyCodeEscape); err != nil {
				fmt.Printf("error: %v\n", err)
			}
			time.Sleep(500 * time.Millisecond)
			stepNum++

		case "sp":
			fmt.Println("Pressing Space...")
			if err := client.KeyPress(KeyCodeSpace); err != nil {
				fmt.Printf("error: %v\n", err)
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
					fmt.Printf("Error saving screenshot: %v\n", err)
				} else {
					fmt.Printf("Saved: %s\n", filename)
				}
			} else {
				// Capture fresh
				img, err := client.Screenshot()
				if err != nil {
					fmt.Printf("Screenshot error: %v\n", err)
				} else {
					if err := saveScreenshotPNG(img, filename); err != nil {
						fmt.Printf("Error saving screenshot: %v\n", err)
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
					fmt.Printf("error: %v\n", err)
				}
			} else if strings.HasPrefix(input, "key ") {
				var keycode int
				fmt.Sscanf(strings.TrimPrefix(input, "key "), "%d", &keycode)
				fmt.Printf("Pressing key %d...\n", keycode)
				if err := client.KeyPress(uint16(keycode)); err != nil {
					fmt.Printf("error: %v\n", err)
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

// ctlConnectError wraps a control socket dial error with actionable guidance.
func ctlConnectError(sock string, err error) error {
	if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
		return fmt.Errorf("VM is not running (no control socket at %s)\n  start the VM first: vz-macos run", sock)
	}
	if strings.Contains(err.Error(), "connection refused") {
		return fmt.Errorf("VM control socket exists but is not responding at %s\n  the VM may have crashed; try: vz-macos run", sock)
	}
	return fmt.Errorf("connect to control socket %s: %w", sock, err)
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
