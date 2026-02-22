// control_socket.go - Socket-based control for keyboard, mouse, and screenshots
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/tmc/appledocs/generated/appkit"
	"github.com/tmc/appledocs/generated/corefoundation"
	"github.com/tmc/appledocs/generated/dispatch"
	vz "github.com/tmc/appledocs/generated/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

var (
	protojsonMarshaler = protojson.MarshalOptions{
		UseProtoNames: true,
	}
	protojsonUnmarshaler = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

// ControlServer manages the Unix socket for VM control
type ControlServer struct {
	socketPath     string
	listener       net.Listener
	vmView         vz.VZVirtualMachineView
	window         appkit.NSWindow
	vm             vz.VZVirtualMachine
	vmQueue        dispatch.Queue
	mu             sync.Mutex
	agentMu        sync.Mutex // separate mutex for agent operations (can be long-running)
	running        bool
	lastScreenshot image.Image  // For diff mode
	agent          *AgentClient // GRPC client to guest agent (nil until connected)
}

// NewControlServer creates a new control server
func NewControlServer(socketPath string) *ControlServer {
	return &ControlServer{
		socketPath: socketPath,
	}
}

// SetVMViewWithWindow sets the VM view and window for input/screenshot operations
func (s *ControlServer) SetVMViewWithWindow(view vz.VZVirtualMachineView, window appkit.NSWindow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vmView = view
	s.window = window
}

// SetVM sets the VM and dispatch queue for lifecycle operations (pause/resume/stop)
func (s *ControlServer) SetVM(vm vz.VZVirtualMachine, queue dispatch.Queue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vm = vm
	s.vmQueue = queue
}

// Start begins listening on the Unix socket
func (s *ControlServer) Start() error {
	// Remove existing socket file
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on socket: %w", err)
	}
	s.listener = listener
	s.running = true

	if verbose {
		fmt.Printf("Control socket listening at: %s\n", s.socketPath)
	}

	go s.acceptLoop()
	return nil
}

// Stop closes the control server
func (s *ControlServer) Stop() {
	s.running = false
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
}

func (s *ControlServer) acceptLoop() {
	for s.running {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.running {
				fmt.Printf("Accept error: %v\n", err)
			}
			continue
		}
		go s.handleConnection(conn)
	}
}

func (s *ControlServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var req controlpb.ControlRequest
		if err := protojsonUnmarshaler.Unmarshal([]byte(line), &req); err != nil {
			writeResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}

		resp := s.handleRequest(&req)
		writeResponse(conn, resp)
	}
}

func writeResponse(conn net.Conn, resp *controlpb.ControlResponse) {
	data, _ := protojsonMarshaler.Marshal(resp)
	conn.Write(append(data, '\n'))
}

func (s *ControlServer) handleRequest(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	// Agent commands use a separate mutex so long-running agent-exec calls
	// don't block non-agent operations (screenshots, key events, etc.).
	if resp, ok := s.handleAgentCommand(req); ok {
		return resp
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch req.Type {
	case "screenshot":
		cmd := req.GetScreenshot()
		if cmd == nil {
			cmd = &controlpb.ScreenshotCommand{}
		}
		return s.takeScreenshotWithOptions(cmd)
	case "key":
		cmd := req.GetKey()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing key command payload"}
		}
		return s.sendKeyEvent(cmd)
	case "mouse":
		cmd := req.GetMouse()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing mouse command payload"}
		}
		return s.sendMouseEvent(cmd)
	case "text":
		cmd := req.GetText()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing text command payload"}
		}
		return s.typeText(cmd)
	case "ping":
		return &controlpb.ControlResponse{Success: true, Data: "pong"}
	case "status":
		return s.getVMStatus()
	case "pause":
		return s.pauseVM()
	case "resume":
		return s.resumeVM()
	case "stop":
		return s.stopVM()
	case "request-stop":
		return s.requestStopVM()
	case "snapshot":
		cmd := req.GetSnapshot()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing snapshot command payload"}
		}
		return s.handleSnapshotCommand(cmd)
	case "memory":
		cmd := req.GetMemory()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing memory command payload"}
		}
		return s.handleMemoryCommand(cmd)
	case "network-info":
		return s.handleNetworkInfo()
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown command type: %s", req.Type)}
	}
}

// sendKeyEvent sends a keyboard event to the VM.
// By default uses CGEvent (system-level, thread-safe).
// Set UseCGEvent=false to try NSEvent (view-level, may have thread issues).
func (s *ControlServer) sendKeyEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	if s.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "keyboard input requires GUI mode (run with -gui)"}
	}
	if s.window.ID == 0 {
		return &controlpb.ControlResponse{Error: "keyboard input requires GUI mode (run with -gui)"}
	}

	// Default to CGEvent unless explicitly disabled
	useCGEvent := true
	if !cmd.UseCgEvent && cmd.Character != "" {
		// If character is specified and UseCGEvent is explicitly false,
		// use NSEvent method (for view-level typing)
		useCGEvent = false
	}

	if useCGEvent {
		return s.sendKeyEventCGEvent(cmd)
	}
	return s.sendKeyEventNSEvent(cmd)
}

// sendKeyEventCGEvent uses Quartz CGEvent for keyboard injection.
// This is thread-safe. Events are posted to our own process via CGEventPostToPid
// so they reach the VM window regardless of which app has focus.
func (s *ControlServer) sendKeyEventCGEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	event := CGEventCreateKeyboardEvent(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	// Apply modifier flags if specified
	if cmd.Modifiers != 0 {
		CGEventSetFlags(event, uint64(cmd.Modifiers))
	}

	// Post to our own process so keystrokes reach the VM window,
	// not whatever app the user currently has focused (e.g. iTerm).
	CGEventPostToSelf(event)

	return &controlpb.ControlResponse{Success: true}
}

// sendKeyEventNSEvent uses AppKit NSEvent for view-level keyboard injection.
// All AppKit calls are dispatched to the main thread.
func (s *ControlServer) sendKeyEventNSEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	var eventType appkit.NSEventType
	if cmd.KeyDown {
		eventType = appkit.NSEventTypeKeyDown
	} else {
		eventType = appkit.NSEventTypeKeyUp
	}

	chars := cmd.Character
	if chars == "" {
		switch cmd.KeyCode {
		case 36:
			chars = "\r"
		case 48:
			chars = "\t"
		case 51:
			chars = "\x7f"
		case 53:
			chars = "\x1b"
		case 49:
			chars = " "
		case 126:
			chars = "\x1b[A"
		case 125:
			chars = "\x1b[B"
		case 124:
			chars = "\x1b[C"
		case 123:
			chars = "\x1b[D"
		default:
			chars = ""
		}
	}

	// All AppKit calls must happen on the main thread.
	var resp *controlpb.ControlResponse
	done := make(chan struct{})
	DispatchAsync(GetMainDispatchQueue(), func() {
		defer close(done)
		windowNumber := s.window.WindowNumber()

		iEvent := appkit.GetNSEventClass().KeyEventWithTypeLocationModifierFlagsTimestampWindowNumberContextCharactersCharactersIgnoringModifiersIsARepeatKeyCode(
			eventType,
			corefoundation.CGPoint{X: 0, Y: 0},
			appkit.NSEventModifierFlags(cmd.Modifiers),
			0.0,
			int(windowNumber),
			nil,
			chars,
			chars,
			false,
			uint16(cmd.KeyCode),
		)

		event := appkit.NSEventFromID(iEvent.GetID())
		if event.ID == 0 {
			resp = &controlpb.ControlResponse{Error: "failed to create NSEvent"}
			return
		}

		if cmd.KeyDown {
			s.vmView.KeyDown(&event)
		} else {
			s.vmView.KeyUp(&event)
		}
		resp = &controlpb.ControlResponse{Success: true}
	})
	<-done
	return resp
}

// sendMouseEvent sends a mouse event to the VM using CGEvent.
// CGEvent is thread-safe and posts directly to the HID system.
// AppKit calls (window frame, view bounds) are dispatched to the main thread.
func (s *ControlServer) sendMouseEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	if s.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}
	if s.window.ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}

	// All AppKit calls must happen on the main thread.
	// Read window/view geometry and bring window to front synchronously.
	var windowFrame corefoundation.CGRect
	var bounds corefoundation.CGRect
	var screenHeight float64
	done := make(chan struct{})
	DispatchAsync(GetMainDispatchQueue(), func() {
		defer close(done)
		s.window.MakeKeyAndOrderFront(nil)
		windowFrame = s.window.Frame()
		bounds = s.vmView.Bounds()
		mainScreen := appkit.GetNSScreenClass().MainScreen()
		screenHeight = mainScreen.Frame().Size.Height
	})
	<-done

	var viewX, viewY float64
	if cmd.Absolute {
		viewX = cmd.X
		viewY = cmd.Y
	} else {
		// Normalized coordinates (0-1) to view coordinates
		viewX = cmd.X * bounds.Size.Width
		viewY = cmd.Y * bounds.Size.Height
	}

	// Convert view coordinates to screen coordinates
	// Window frame origin is bottom-left, CGEvent uses top-left screen coordinates
	screenX := windowFrame.Origin.X + viewX
	windowTop := screenHeight - (windowFrame.Origin.Y + windowFrame.Size.Height)
	screenY := windowTop + 22 + viewY

	position := corefoundation.CGPoint{X: screenX, Y: screenY}

	// Handle click as down+up sequence
	if cmd.Action == "click" {
		downCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "down", Absolute: cmd.Absolute,
		}
		downResp := s.sendMouseEvent(downCmd)
		if !downResp.Success {
			return downResp
		}
		upCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "up", Absolute: cmd.Absolute,
		}
		return s.sendMouseEvent(upCmd)
	}

	// Map action to CGEvent mouse type
	var eventType uint32
	var mouseButton uint32 = uint32(cmd.Button)
	switch cmd.Action {
	case "move":
		eventType = kCGEventMouseMoved
	case "down":
		switch cmd.Button {
		case 0:
			eventType = kCGEventLeftMouseDown
		case 1:
			eventType = kCGEventRightMouseDown
		default:
			return &controlpb.ControlResponse{Error: "only left (0) and right (1) buttons supported"}
		}
	case "up":
		switch cmd.Button {
		case 0:
			eventType = kCGEventLeftMouseUp
		case 1:
			eventType = kCGEventRightMouseUp
		default:
			return &controlpb.ControlResponse{Error: "only left (0) and right (1) buttons supported"}
		}
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown mouse action: %s", cmd.Action)}
	}

	// Create and post CGEvent
	event := CGEventCreateMouseEvent(0, eventType, position, mouseButton)
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	// Post to our own process so clicks reach the VM window.
	CGEventPostToSelf(event)

	return &controlpb.ControlResponse{Success: true}
}

// typeText types a string of text character by character
func (s *ControlServer) typeText(cmd *controlpb.TextCommand) *controlpb.ControlResponse {
	// Type each character using CGEvent with Unicode string support.
	// CGEventPost is thread-safe — no need for MakeKeyAndOrderFront
	// (which crashes when called from a background goroutine).
	for _, char := range cmd.Text {
		TypeCharacter(char)
		// Small delay between characters for reliability
		time.Sleep(20 * time.Millisecond)
	}
	return &controlpb.ControlResponse{Success: true}
}

// GetControlSocketPath returns the default socket path
func GetControlSocketPath() string {
	return filepath.Join(vmDir, "control.sock")
}

// =============================================================================
// VM lifecycle control commands
// =============================================================================

// getVMStatus returns the current VM state and available operations.
func (s *ControlServer) getVMStatus() *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	// VM state queries must be done on the VM's dispatch queue
	var state vz.VZVirtualMachineState
	var canPause, canResume, canStop, canRequestStop bool

	done := make(chan struct{})
	DispatchAsyncQueue(s.vmQueue, func() {
		defer close(done)
		state = vz.VZVirtualMachineState(s.vm.State())
		canPause = s.vm.CanPause()
		canResume = s.vm.CanResume()
		canStop = s.vm.CanStop()
		canRequestStop = s.vm.CanRequestStop()
	})
	<-done

	status := map[string]interface{}{
		"state":          state.String(),
		"canPause":       canPause,
		"canResume":      canResume,
		"canStop":        canStop,
		"canRequestStop": canRequestStop,
	}

	data, _ := json.Marshal(status)
	return &controlpb.ControlResponse{Success: true, Data: string(data)}
}

// pauseVM pauses the VM.
func (s *ControlServer) pauseVM() *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	// Check state on VM queue
	var canPause bool
	var state vz.VZVirtualMachineState
	checkDone := make(chan struct{})
	DispatchAsyncQueue(s.vmQueue, func() {
		defer close(checkDone)
		canPause = s.vm.CanPause()
		state = vz.VZVirtualMachineState(s.vm.State())
	})
	<-checkDone

	if !canPause {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("cannot pause VM in state: %s", state.String())}
	}

	errCh := make(chan error, 1)
	DispatchAsyncQueue(s.vmQueue, func() {
		s.vm.PauseWithCompletionHandler(func(err error) {
			errCh <- err
		})
	})

	// Wait for completion with timeout
	select {
	case err := <-errCh:
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("pause failed: %v", err)}
		}
		return &controlpb.ControlResponse{Success: true, Data: "paused"}
	case <-time.After(10 * time.Second):
		return &controlpb.ControlResponse{Error: "pause timed out"}
	}
}

// resumeVM resumes a paused VM.
func (s *ControlServer) resumeVM() *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	// Check state on VM queue
	var canResume bool
	var state vz.VZVirtualMachineState
	checkDone := make(chan struct{})
	DispatchAsyncQueue(s.vmQueue, func() {
		defer close(checkDone)
		canResume = s.vm.CanResume()
		state = vz.VZVirtualMachineState(s.vm.State())
	})
	<-checkDone

	if !canResume {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("cannot resume VM in state: %s", state.String())}
	}

	errCh := make(chan error, 1)
	DispatchAsyncQueue(s.vmQueue, func() {
		s.vm.ResumeWithCompletionHandler(func(err error) {
			errCh <- err
		})
	})

	// Wait for completion with timeout
	select {
	case err := <-errCh:
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("resume failed: %v", err)}
		}
		return &controlpb.ControlResponse{Success: true, Data: "resumed"}
	case <-time.After(10 * time.Second):
		return &controlpb.ControlResponse{Error: "resume timed out"}
	}
}

// stopVM forcefully stops the VM.
func (s *ControlServer) stopVM() *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	// Check state on VM queue
	var canStop bool
	var state vz.VZVirtualMachineState
	checkDone := make(chan struct{})
	DispatchAsyncQueue(s.vmQueue, func() {
		defer close(checkDone)
		canStop = s.vm.CanStop()
		state = vz.VZVirtualMachineState(s.vm.State())
	})
	<-checkDone

	if !canStop {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("cannot stop VM in state: %s", state.String())}
	}

	errCh := make(chan error, 1)
	DispatchAsyncQueue(s.vmQueue, func() {
		s.vm.StopWithCompletionHandler(func(err error) {
			errCh <- err
		})
	})

	// Wait for completion with timeout
	select {
	case err := <-errCh:
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("stop failed: %v", err)}
		}
		return &controlpb.ControlResponse{Success: true, Data: "stopped"}
	case <-time.After(30 * time.Second):
		return &controlpb.ControlResponse{Error: "stop timed out"}
	}
}

// requestStopVM sends an ACPI power button event for graceful shutdown.
func (s *ControlServer) requestStopVM() *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	// Check state and request stop on VM queue
	var canRequestStop bool
	var state vz.VZVirtualMachineState
	var success bool
	done := make(chan struct{})
	DispatchAsyncQueue(s.vmQueue, func() {
		defer close(done)
		canRequestStop = s.vm.CanRequestStop()
		state = vz.VZVirtualMachineState(s.vm.State())
		if canRequestStop {
			success, _ = s.vm.RequestStopWithError()
		}
	})
	<-done

	if !canRequestStop {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("cannot request stop in state: %s", state.String())}
	}

	if !success {
		return &controlpb.ControlResponse{Error: "request stop failed"}
	}

	return &controlpb.ControlResponse{Success: true, Data: "stop requested (ACPI power button sent)"}
}

// handleNetworkInfo returns the VM's network configuration including MAC address
// and optionally the guest IP address (if the agent is available).
func (s *ControlServer) handleNetworkInfo() *controlpb.ControlResponse {
	info := &controlpb.NetworkInfoResponse{
		Mode: networkMode,
	}

	// Read MAC address from saved file
	macPath := filepath.Join(vmDir, "mac.address")
	if data, err := os.ReadFile(macPath); err == nil {
		info.MacAddress = strings.TrimSpace(string(data))
	}

	// Try to get guest IP via agent (best-effort, short timeout)
	if err := s.ensureAgent(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, err := s.agent.Exec(ctx, []string{"ipconfig", "getifaddr", "en0"}, nil, "")
		if err == nil && result.ExitCode == 0 {
			info.GuestIp = strings.TrimSpace(string(result.Stdout))
		}
	}

	data, _ := protojsonMarshaler.Marshal(info)
	return &controlpb.ControlResponse{Success: true, Data: string(data)}
}

// Global control server instance
var controlServer *ControlServer
