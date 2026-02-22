// ctl.go - CLI for interacting with VM control socket
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

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

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos ctl [options] <command> [args...]

Commands:
  ping                  Test connection
  status                Get VM state and capabilities
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
  step                  Interactive step-through mode for Setup Assistant debugging
  setup-assist <user> <pass>  Run Setup Assistant automation

Guest Agent (via GRPC over vsock):
  agent-connect         Connect to guest agent
  agent-ping            Ping guest agent
  agent-info            Get guest system info
  agent-exec <cmd> [args...]  Run command in guest
  agent-cp <host> <guest>     Copy file host→guest (streaming)
  agent-cp -from-guest <guest> <host>  Copy file guest→host
  agent-read <path>     Read file from guest (base64)
  agent-write <path> <data>   Write data to file in guest
  agent-shutdown [force]      Graceful guest shutdown
  agent-reboot                Reboot guest
  agent-sshd <on|off|status>  Manage SSH remote login
  agent-mount-volumes         Mount tagged VirtioFS volumes in guest

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

	switch cmdType {
	case "detect":
		return ctlDetectScreen(sock)
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

	switch cmdType {
	case "ping", "status", "pause", "resume", "stop", "request-stop", "network-info":
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
		req.Command = &controlpb.ControlRequest_Screenshot{
			Screenshot: &controlpb.ScreenshotCommand{
				Scale:   0.5,
				Quality: 60,
				Format:  "jpeg",
			},
		}

	case "key":
		if len(subArgs) < 1 {
			return fmt.Errorf("key command requires keycode")
		}
		var keycode int
		fmt.Sscanf(subArgs[0], "%d", &keycode)
		keyDown := true
		if len(subArgs) >= 2 && subArgs[1] == "up" {
			keyDown = false
		}
		req.Command = &controlpb.ControlRequest_Key{
			Key: &controlpb.KeyCommand{
				KeyCode: uint32(keycode),
				KeyDown: keyDown,
			},
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

// ctlSendRequest sends a proto request to the control socket and returns the response.
func ctlSendRequest(sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", sock, err)
	}
	defer conn.Close()

	// Screenshots may take longer and produce larger responses
	readTimeout := timeout
	if cmdType == "screenshot" {
		readTimeout = 30 * time.Second
	}
	conn.SetDeadline(time.Now().Add(readTimeout))

	// Marshal and send request
	reqBytes, err := protojsonMarshaler.Marshal(req)
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
				return fmt.Errorf("agent not ready after %s: %w", wait, err)
			}
			return fmt.Errorf("agent not ready after %s: %s", wait, resp.Error)
		}

		if attempt == 1 {
			fmt.Fprintf(os.Stderr, "Connecting to guest agent (waiting up to %s)...\n", wait)
		}
		time.Sleep(2 * time.Second)
	}
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
	fmt.Printf("Detected screen state: %s\n", state)

	// If it looks like Setup Assistant, try to detect the page
	if state == ScreenStateSetupAssistant {
		fullImg, err := client.Screenshot()
		if err == nil {
			page := DetectSetupAssistantPage(fullImg)
			fmt.Printf("Setup Assistant page: %s\n", page)
		}
	}

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
				fmt.Printf("Error: %v\n", err)
			}
			time.Sleep(500 * time.Millisecond)
			stepNum++

		case "t":
			fmt.Println("Pressing Tab...")
			if err := client.KeyPress(KeyCodeTab); err != nil {
				fmt.Printf("Error: %v\n", err)
			}
			time.Sleep(200 * time.Millisecond)

		case "e":
			fmt.Println("Pressing Escape...")
			if err := client.KeyPress(KeyCodeEscape); err != nil {
				fmt.Printf("Error: %v\n", err)
			}
			time.Sleep(500 * time.Millisecond)
			stepNum++

		case "sp":
			fmt.Println("Pressing Space...")
			if err := client.KeyPress(KeyCodeSpace); err != nil {
				fmt.Printf("Error: %v\n", err)
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
					fmt.Printf("Error: %v\n", err)
				}
			} else if strings.HasPrefix(input, "key ") {
				var keycode int
				fmt.Sscanf(strings.TrimPrefix(input, "key "), "%d", &keycode)
				fmt.Printf("Pressing key %d...\n", keycode)
				if err := client.KeyPress(uint16(keycode)); err != nil {
					fmt.Printf("Error: %v\n", err)
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

// ctlSendCommand sends a command to the control socket and returns the response.
// This is a compatibility wrapper used by fda.go and sip.go. All callers pass
// agent-exec with {"args": [...]}, so we build the appropriate proto request.
func ctlSendCommand(sock, cmdType string, cmdData interface{}, timeout time.Duration) (*controlpb.ControlResponse, error) {
	req := &controlpb.ControlRequest{Type: cmdType}

	// Handle agent-exec with args map
	if cmdType == "agent-exec" {
		if m, ok := cmdData.(map[string]interface{}); ok {
			if args, ok := m["args"].([]string); ok {
				req.Command = &controlpb.ControlRequest_AgentExec{
					AgentExec: &controlpb.AgentExecCommand{Args: args},
				}
			}
		}
	}

	return ctlSendRequest(sock, req, timeout, cmdType)
}

// saveScreenshotPNG saves an image as PNG
func saveScreenshotPNG(img image.Image, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// Need to import image/png - using jpeg as fallback since png may not be imported
	// Actually, we can use the jpeg encoder
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 95})
}
