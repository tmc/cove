// control_socket.go - Socket-based control for keyboard, mouse, and screenshots
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/tmc/apple/objc"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"

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

const (
	controlTokenFileName = "control.token"
	controlTokenEnvVar   = "VZ_MACOS_CTL_TOKEN"
)

// ControlServer manages the Unix socket for VM control
type ControlServer struct {
	socketPath        string
	vmDir             string
	authToken         string
	listener          net.Listener
	vmView            vz.VZVirtualMachineView
	window            appkit.NSWindow
	vm                vz.VZVirtualMachine
	vmQueue           dispatch.Queue
	mu                sync.Mutex
	agentMu           sync.Mutex // separate mutex for agent operations (can be long-running)
	screenshotMu      sync.Mutex // protects lastScreenshot for diff mode
	running           bool
	lastScreenshot    image.Image  // For diff mode
	agent             *AgentClient // GRPC client to guest agent (nil until connected)
	ocr               *OCRService  // lazily created OCR service for server-side OCR commands
	windowNum         int          // cached window number for thread-safe screenshot
	viewContentHeight int          // cached view content height in pixels (excludes title bar)
}

// NewControlServer creates a new control server
func NewControlServer(socketPath string) *ControlServer {
	return NewControlServerWithVMDir(socketPath, vmDir)
}

// NewControlServerWithVMDir creates a new control server bound to a specific VM directory.
func NewControlServerWithVMDir(socketPath, vmDirectory string) *ControlServer {
	if vmDirectory == "" {
		vmDirectory = vmDir
	}
	return &ControlServer{
		socketPath: socketPath,
		vmDir:      vmDirectory,
	}
}

func (s *ControlServer) effectiveVMDir() string {
	if s.vmDir != "" {
		return s.vmDir
	}
	return vmDir
}

// SetVMViewWithWindow sets the VM view and window for input/screenshot operations
func (s *ControlServer) SetVMViewWithWindow(view vz.VZVirtualMachineView, window appkit.NSWindow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vmView = view
	s.window = window
	// Cache window number for thread-safe access (avoids main-thread dispatch).
	s.windowNum = int(window.WindowNumber())
	// Cache view content height for title bar cropping in screenshots.
	// This runs on the main thread so we can safely read NSView bounds.
	s.viewContentHeight = int(vmViewAsNSView(view).Bounds().Size.Height)
	fmt.Printf("[control] SetVMViewWithWindow: vmView=%x window=%x windowNum=%d viewH=%d verbose=%v\n",
		view.ID, window.ID, s.windowNum, s.viewContentHeight, verbose)
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
	if s.authToken == "" {
		token, err := EnsureControlTokenForVM(s.effectiveVMDir())
		if err != nil {
			return fmt.Errorf("control token: %w", err)
		}
		s.authToken = token
	}

	// Remove existing socket file
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on socket: %w", err)
	}
	if err := os.Chmod(s.socketPath, 0600); err != nil {
		listener.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.listener = listener
	s.running = true

	if verbose {
		fmt.Printf("Control socket listening at: %s\n", s.socketPath)
		fmt.Printf("Control auth token: %s\n", GetControlTokenPathForVM(s.effectiveVMDir()))
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
		populateLegacyRequestPayloads(line, &req)
		if !s.authorizeRequest(req.AuthToken) {
			writeResponse(conn, &controlpb.ControlResponse{Error: "unauthorized"})
			continue
		}

		if req.Type == "agent-exec-stream" {
			s.handleAgentExecStreamConnection(conn, &req)
			continue
		}

		// Handle typed OCR command from proto oneof.
		if req.Type == "ocr" {
			if ocrCmd := req.GetOcr(); ocrCmd != nil {
				// Map proto OCR action to legacy command type for unified handling.
				mapped := &controlpb.ControlRequest{Type: "ocr-" + ocrCmd.Action}
				fakeJSON, _ := json.Marshal(map[string]any{
					"type": "ocr-" + ocrCmd.Action,
					"data": map[string]string{"text": ocrCmd.Text, "timeout": ocrCmd.Timeout},
				})
				if resp, ok := s.handleOCRSocketCommand(mapped, fakeJSON); ok {
					writeResponse(conn, resp)
					continue
				}
			}
			writeResponse(conn, &controlpb.ControlResponse{Error: "missing ocr command payload"})
			continue
		}

		// OCR commands use the raw JSON line to extract the "data" field
		// since these commands aren't in the protobuf schema.
		if resp, ok := s.handleOCRSocketCommand(&req, []byte(line)); ok {
			writeResponse(conn, resp)
			continue
		}

		resp := s.handleRequest(&req)
		writeResponse(conn, resp)
	}
}

func writeResponse(conn net.Conn, resp *controlpb.ControlResponse) {
	data, err := protojsonMarshaler.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "control socket: marshal response: %v\n", err)
		return
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "control socket: write response: %v\n", err)
	}
}

func (s *ControlServer) handleRequest(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	// Agent commands use a separate mutex so long-running agent-exec calls
	// don't block non-agent operations (screenshots, key events, etc.).
	if resp, ok := s.handleAgentCommand(req); ok {
		return resp
	}

	// Handle lock-free commands first. These use thread-safe APIs
	// (CGWindowListCreateImage, VM state queries) and must not block
	// behind the mutex — otherwise a slow screenshot holds up ping/status.
	switch req.Type {
	case "screenshot":
		cmd := req.GetScreenshot()
		if cmd == nil {
			cmd = &controlpb.ScreenshotCommand{}
		}
		return s.takeScreenshotWithOptions(cmd)
	case "ping":
		return &controlpb.ControlResponse{Success: true, Data: "pong", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "pong"}}}
	case "status":
		return s.getVMStatus()
	case "capabilities":
		return s.getCapabilities()
	case "shared-folders-apply":
		return s.handleSharedFoldersApply()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch req.Type {
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
		// Use CGEvent typing via window server (same path as real keyboard input).
		return s.typeText(cmd)
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

// getOCR returns the lazily-initialized OCR service.
func (s *ControlServer) getOCR() *OCRService {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ocr == nil {
		s.ocr = NewOCRService(verbose)
	}
	return s.ocr
}

// ocrDataParams extracts text and timeout from the JSON "data" field.
type ocrDataParams struct {
	Text    string `json:"text"`
	Timeout string `json:"timeout"`
	Region  string `json:"region"`
}

func parseOCRData(rawJSON []byte) ocrDataParams {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return ocrDataParams{}
	}
	blob, ok := raw["data"]
	if !ok {
		return ocrDataParams{}
	}
	var p ocrDataParams
	json.Unmarshal(blob, &p)
	return p
}

// handleOCRSocketCommand handles OCR-related commands that need the raw JSON
// line to extract parameters. Returns (response, true) if handled.
func (s *ControlServer) handleOCRSocketCommand(req *controlpb.ControlRequest, rawJSON []byte) (*controlpb.ControlResponse, bool) {
	switch req.Type {
	case "ocr-click":
		p := parseOCRData(rawJSON)
		if p.Text == "" {
			return &controlpb.ControlResponse{Error: "ocr-click requires text"}, true
		}
		opts, err := ParseOCRSearchOptions(p.Region)
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}, true
		}
		timeout := 10 * time.Second
		if p.Timeout != "" {
			if d, err := time.ParseDuration(p.Timeout); err == nil {
				timeout = d
			}
		}
		ocr := s.getOCR()
		if err := s.OCRClickTextWithOptions(ocr, p.Text, timeout, opts); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}, true
		}
		return &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("clicked %q", p.Text), Result: &controlpb.ControlResponse_OcrText{OcrText: &controlpb.OCRTextResponse{Text: fmt.Sprintf("clicked %q", p.Text)}}}, true

	case "ocr-wait":
		p := parseOCRData(rawJSON)
		if p.Text == "" {
			return &controlpb.ControlResponse{Error: "ocr-wait requires text"}, true
		}
		opts, err := ParseOCRSearchOptions(p.Region)
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}, true
		}
		timeout := 60 * time.Second
		if p.Timeout != "" {
			if d, err := time.ParseDuration(p.Timeout); err == nil {
				timeout = d
			}
		}
		ocr := s.getOCR()
		if err := s.OCRWaitForTextWithOptions(ocr, p.Text, timeout, opts); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}, true
		}
		return &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("found %q", p.Text), Result: &controlpb.ControlResponse_OcrText{OcrText: &controlpb.OCRTextResponse{Text: fmt.Sprintf("found %q", p.Text)}}}, true

	case "ocr-gone":
		p := parseOCRData(rawJSON)
		if p.Text == "" {
			return &controlpb.ControlResponse{Error: "ocr-gone requires text"}, true
		}
		opts, err := ParseOCRSearchOptions(p.Region)
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}, true
		}
		timeout := 30 * time.Second
		if p.Timeout != "" {
			if d, err := time.ParseDuration(p.Timeout); err == nil {
				timeout = d
			}
		}
		ocr := s.getOCR()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			img, errMsg := s.captureVMView()
			if errMsg != "" {
				time.Sleep(time.Second)
				continue
			}
			_, _, found := ocr.FindTextNormalizedWithOptions(img, p.Text, opts)
			if !found {
				return &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("%q gone", p.Text), Result: &controlpb.ControlResponse_OcrText{OcrText: &controlpb.OCRTextResponse{Text: fmt.Sprintf("%q gone", p.Text)}}}, true
			}
			time.Sleep(time.Second)
		}
		return &controlpb.ControlResponse{Error: fmt.Sprintf("timeout: text %q still visible", p.Text)}, true

	case "ocr-all-text":
		ocr := s.getOCR()
		text, err := s.OCRAllText(ocr)
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}, true
		}
		return &controlpb.ControlResponse{Success: true, Data: text, Result: &controlpb.ControlResponse_OcrText{OcrText: &controlpb.OCRTextResponse{Text: text}}}, true

	case "detect-page":
		ocr := s.getOCR()
		page := s.OCRDetectPage(ocr)
		return &controlpb.ControlResponse{Success: true, Data: page, Result: &controlpb.ControlResponse_OcrText{OcrText: &controlpb.OCRTextResponse{Text: page}}}, true

	case "detect-screen":
		ocr := s.getOCR()
		img, errMsg := s.captureVMView()
		if errMsg != "" {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("capture: %s", errMsg)}, true
		}
		state := DetectScreenStateOCR(img, ocr)
		return &controlpb.ControlResponse{Success: true, Data: state.String(), Result: &controlpb.ControlResponse_ScreenDetection{ScreenDetection: &controlpb.ScreenDetectionResponse{State: state.String()}}}, true
	}
	return nil, false
}

func populateLegacyRequestPayloads(line string, req *controlpb.ControlRequest) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return
	}
	populateLegacyAuthToken(raw, req)

	switch req.Type {
	case "screenshot":
		populateLegacyScreenshot(raw, req)
	case "snapshot":
		populateLegacySnapshot(raw, req)
	case "memory":
		populateLegacyMemory(raw, req)
	case "agent-exec", "agent-exec-stream":
		populateLegacyAgentExec(raw, req)
	}
}

func populateLegacyAuthToken(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.AuthToken != "" {
		return
	}
	if blob, ok := raw["token"]; ok {
		var v string
		if err := json.Unmarshal(blob, &v); err == nil {
			req.AuthToken = v
		}
	}
}

func (s *ControlServer) authorizeRequest(token string) bool {
	if s.authToken == "" {
		return true
	}
	provided := strings.TrimSpace(token)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.authToken)) == 1
}

func populateLegacyScreenshot(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetScreenshot() != nil {
		return
	}
	cmd := &controlpb.ScreenshotCommand{}
	seen := false

	if blob, ok := raw["screenshot"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}

	if blob, ok := raw["diff"]; ok {
		var v bool
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Diff = v
			seen = true
		}
	}
	if blob, ok := raw["scale"]; ok {
		var v float64
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Scale = v
			seen = true
		}
	}
	if blob, ok := raw["quality"]; ok {
		var v int32
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Quality = v
			seen = true
		}
	}
	if blob, ok := raw["format"]; ok {
		var v string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Format = v
			seen = true
		}
	}

	if seen {
		req.Command = &controlpb.ControlRequest_Screenshot{Screenshot: cmd}
	}
}

func populateLegacySnapshot(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetSnapshot() != nil {
		return
	}
	cmd := &controlpb.SnapshotCommand{}
	seen := false

	if blob, ok := raw["snapshot"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}

	type snapshotPayload struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if blob, ok := raw["data"]; ok {
		var payload snapshotPayload
		if err := json.Unmarshal(blob, &payload); err == nil {
			if payload.Action != "" {
				cmd.Action = payload.Action
			}
			if payload.Name != "" {
				cmd.Name = payload.Name
			}
			seen = seen || payload.Action != "" || payload.Name != ""
		}
	}

	if seen {
		req.Command = &controlpb.ControlRequest_Snapshot{Snapshot: cmd}
	}
}

func populateLegacyMemory(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetMemory() != nil {
		return
	}
	cmd := &controlpb.MemoryCommand{}
	seen := false

	if blob, ok := raw["memory"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}

	type memoryPayload struct {
		Action string  `json:"action"`
		SizeGB float64 `json:"size_gb"`
	}
	if blob, ok := raw["data"]; ok {
		var payload memoryPayload
		if err := json.Unmarshal(blob, &payload); err == nil {
			if payload.Action != "" {
				cmd.Action = payload.Action
			}
			if payload.SizeGB != 0 {
				cmd.SizeGb = payload.SizeGB
			}
			seen = seen || payload.Action != "" || payload.SizeGB != 0
		}
	}

	if seen {
		req.Command = &controlpb.ControlRequest_Memory{Memory: cmd}
	}
}

func populateLegacyAgentExec(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetAgentExec() != nil {
		return
	}
	cmd := &controlpb.AgentExecCommand{}
	seen := false

	if blob, ok := raw["agent_exec"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}
	if blob, ok := raw["args"]; ok {
		var v []string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Args = v
			seen = true
		}
	}
	if blob, ok := raw["env"]; ok {
		var v map[string]string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Env = v
			seen = true
		}
	}
	if blob, ok := raw["working_dir"]; ok {
		var v string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.WorkingDir = v
			seen = true
		}
	}

	if seen {
		req.Command = &controlpb.ControlRequest_AgentExec{AgentExec: cmd}
	}
}

// sendKeyEvent sends a keyboard event to the VM.
// Uses direct HID report injection via VZVirtualMachine's private
// sendKeyboardEvents:keyboardID: API. This is the same path that
// VZVirtualMachineView.keyDown: uses internally.
// Falls back to NSEvent or CGEvent delivery if HID injection fails.
func (s *ControlServer) sendKeyEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	if s.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "keyboard input requires GUI mode (run with -gui)"}
	}

	// Modifier chords are fragile in Recovery UI when represented only as
	// event flags. Synthesize real modifier-key presses instead.
	if cmd.Modifiers != 0 {
		return s.sendKeyChordEvent(cmd)
	}

	// For non-modifier keys, keep the simple path unless caller explicitly asks
	// for CGEvent fallback behavior.
	return s.sendKeyEventPrimitive(cmd)
}

func (s *ControlServer) sendKeyEventPrimitive(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	if cmd.UseCgEvent {
		return s.sendKeyEventMultiPath(cmd, false)
	}
	return s.sendKeyEventNSEvent(cmd)
}

func (s *ControlServer) sendKeyChordEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	mods := modifierKeySequence(cmd.Modifiers)
	if len(mods) == 0 {
		// Unknown modifier bit pattern; fall back to flag-based injection.
		return s.sendKeyEventMultiPath(cmd, true)
	}

	var errs []string
	send := func(name string, c *controlpb.KeyCommand) bool {
		resp := s.sendKeyEventPrimitive(c)
		if resp != nil && resp.Success {
			return true
		}
		msg := "unknown error"
		if resp != nil && resp.Error != "" {
			msg = resp.Error
		}
		errs = append(errs, name+": "+msg)
		return false
	}

	if cmd.KeyDown {
		for _, keyCode := range mods {
			send(fmt.Sprintf("modifier-down-%d", keyCode), &controlpb.KeyCommand{
				KeyCode:    keyCode,
				KeyDown:    true,
				UseCgEvent: cmd.UseCgEvent,
			})
		}
		if send("chord-key-down", &controlpb.KeyCommand{
			KeyCode:    cmd.KeyCode,
			Character:  cmd.Character,
			KeyDown:    true,
			UseCgEvent: cmd.UseCgEvent,
		}) {
			return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
		}
		return &controlpb.ControlResponse{Error: "keyboard chord down failed: " + strings.Join(errs, "; ")}
	}

	// Key up: release primary key first, then modifiers in reverse order.
	send("chord-key-up", &controlpb.KeyCommand{
		KeyCode:    cmd.KeyCode,
		Character:  cmd.Character,
		KeyDown:    false,
		UseCgEvent: cmd.UseCgEvent,
	})
	for i := len(mods) - 1; i >= 0; i-- {
		keyCode := mods[i]
		send(fmt.Sprintf("modifier-up-%d", keyCode), &controlpb.KeyCommand{
			KeyCode:    keyCode,
			KeyDown:    false,
			UseCgEvent: cmd.UseCgEvent,
		})
	}
	if len(errs) == 0 {
		return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	}
	return &controlpb.ControlResponse{Error: "keyboard chord up failed: " + strings.Join(errs, "; ")}
}

func modifierKeySequence(flags uint32) []uint32 {
	var seq []uint32
	if flags&(1<<18) != 0 { // Control
		seq = append(seq, 59)
	}
	if flags&(1<<19) != 0 { // Option
		seq = append(seq, 58)
	}
	if flags&(1<<17) != 0 { // Shift
		seq = append(seq, 56)
	}
	if flags&(1<<20) != 0 { // Command
		seq = append(seq, 55)
	}
	return seq
}

func (s *ControlServer) sendKeyEventMultiPath(cmd *controlpb.KeyCommand, fanout bool) *controlpb.ControlResponse {
	type keyInjector struct {
		name string
		fn   func(*controlpb.KeyCommand) *controlpb.ControlResponse
	}
	paths := []keyInjector{
		{name: "nsevent", fn: s.sendKeyEventNSEvent},
		{name: "cgevent", fn: s.sendKeyEventCGEvent},
		{name: "hid", fn: s.sendKeyEventHID},
	}
	if cmd.UseCgEvent {
		// For shortcut-style input, prefer lower-level injectors first.
		paths = []keyInjector{
			{name: "hid", fn: s.sendKeyEventHID},
			{name: "cgevent", fn: s.sendKeyEventCGEvent},
			{name: "nsevent", fn: s.sendKeyEventNSEvent},
		}
	}

	var errs []string
	succeeded := false
	for _, p := range paths {
		resp := p.fn(cmd)
		if resp != nil && resp.Success {
			succeeded = true
			if verbose {
				fmt.Printf("[key-fallback] %s ok keyCode=%d down=%v mods=%d\n",
					p.name, cmd.KeyCode, cmd.KeyDown, cmd.Modifiers)
			}
			if !fanout {
				return resp
			}
			continue
		}

		msg := "unknown error"
		if resp != nil && resp.Error != "" {
			msg = resp.Error
		}
		errs = append(errs, p.name+": "+msg)
		if verbose {
			fmt.Printf("[key-fallback] %s failed: %s\n", p.name, msg)
		}
	}

	if succeeded {
		return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	}
	if len(errs) == 0 {
		return &controlpb.ControlResponse{Error: "keyboard injection failed"}
	}
	return &controlpb.ControlResponse{Error: "keyboard injection failed: " + strings.Join(errs, "; ")}
}

// macKeycodeToHIDUsage maps macOS virtual keycodes to USB HID usage IDs.
var macKeycodeToHIDUsage = map[uint32]byte{
	0:  0x04, // a
	1:  0x16, // s
	2:  0x07, // d
	3:  0x09, // f
	4:  0x0B, // h
	5:  0x0A, // g
	6:  0x1D, // z
	7:  0x1B, // x
	8:  0x06, // c
	9:  0x19, // v
	11: 0x05, // b
	12: 0x14, // q
	13: 0x1A, // w
	14: 0x08, // e
	15: 0x15, // r
	16: 0x1C, // y
	17: 0x17, // t
	18: 0x1E, // 1
	19: 0x1F, // 2
	20: 0x20, // 3
	21: 0x21, // 4
	22: 0x23, // 6
	23: 0x22, // 5
	24: 0x2E, // =
	25: 0x26, // 9
	26: 0x24, // 7
	27: 0x2D, // -
	28: 0x25, // 8
	29: 0x27, // 0
	30: 0x30, // ]
	31: 0x12, // o
	32: 0x18, // u
	33: 0x2F, // [
	34: 0x0C, // i
	35: 0x13, // p
	36: 0x28, // Return
	37: 0x0F, // l
	38: 0x0D, // j
	39: 0x34, // '
	40: 0x0E, // k
	41: 0x33, // ;
	42: 0x31, // backslash
	43: 0x36, // ,
	44: 0x38, // /
	45: 0x11, // n
	46: 0x10, // m
	47: 0x37, // .
	48: 0x2B, // Tab
	49: 0x2C, // Space
	50: 0x35, // `
	51: 0x2A, // Delete (Backspace)
	53: 0x29, // Escape
	// Arrow keys
	123: 0x50, // Left
	124: 0x4F, // Right
	125: 0x51, // Down
	126: 0x52, // Up
	// Function keys
	122: 0x3A, // F1
	120: 0x3B, // F2
	99:  0x3C, // F3
	118: 0x3D, // F4
	96:  0x3E, // F5
	97:  0x3F, // F6
	98:  0x40, // F7
	100: 0x41, // F8
	101: 0x42, // F9
	109: 0x43, // F10
	103: 0x44, // F11
	111: 0x45, // F12
}

// sendKeyEventHID sends a USB HID keyboard report directly to the VM
// via the private sendKeyboardEvents:keyboardID: method.
func (s *ControlServer) sendKeyEventHID(cmd *controlpb.KeyCommand) (resp *controlpb.ControlResponse) {
	// Recover from panics — the private API may crash if args are wrong.
	defer func() {
		if r := recover(); r != nil {
			resp = &controlpb.ControlResponse{Error: fmt.Sprintf("HID inject panic: %v", r)}
		}
	}()

	hidUsage, ok := macKeycodeToHIDUsage[cmd.KeyCode]
	if !ok && cmd.KeyDown {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("no HID mapping for keycode %d", cmd.KeyCode)}
	}

	// Build 8-byte USB HID keyboard report:
	// [modifier, reserved, key1, key2, key3, key4, key5, key6]
	var report [8]byte
	if cmd.Modifiers&(1<<17) != 0 { // Shift
		report[0] |= 0x02
	}
	if cmd.Modifiers&(1<<18) != 0 { // Control
		report[0] |= 0x01
	}
	if cmd.Modifiers&(1<<19) != 0 { // Option/Alt
		report[0] |= 0x04
	}
	if cmd.Modifiers&(1<<20) != 0 { // Command
		report[0] |= 0x08
	}
	if cmd.KeyDown {
		report[2] = hidUsage
	}

	if verbose {
		fmt.Printf("[key-hid] report=%x keycode=%d hid=0x%02x down=%v\n",
			report, cmd.KeyCode, hidUsage, cmd.KeyDown)
	}

	// Dispatch on the VM queue (not main queue) since VZVirtualMachine
	// operations should run on the queue it was created with.
	done := make(chan struct{})
	if s.vmQueue.Handle() != 0 {
		DispatchAsyncQueue(s.vmQueue, func() {
			defer close(done)
			objc.Send[struct{}](s.vm.ID, objc.Sel("sendKeyboardEvents:keyboardID:"),
				unsafe.Pointer(&report[0]), uint32(0))
			resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
		})
	} else {
		// Fall back to main queue
		DispatchAsync(GetMainDispatchQueue(), func() {
			defer close(done)
			objc.Send[struct{}](s.vm.ID, objc.Sel("sendKeyboardEvents:keyboardID:"),
				unsafe.Pointer(&report[0]), uint32(0))
			resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
		})
	}
	<-done
	return resp
}

// sendKeyEventCGEvent uses Quartz CGEvent for keyboard injection.
// Events are posted through the system HID event tap so they travel
// through the window server to VZVirtualMachineView (the same path
// as real keyboard input). The VM window must be key and frontmost.
func (s *ControlServer) sendKeyEventCGEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	// Activate and focus the VM window on the main thread first.
	done := make(chan struct{})
	DispatchAsync(GetMainDispatchQueue(), func() {
		defer close(done)
		appkit.GetNSApplicationClass().SharedApplication().Activate()
		s.window.MakeKeyAndOrderFront(nil)
		s.window.MakeFirstResponder(vmViewAsNSView(s.vmView).NSResponder)
	})
	<-done

	event, err := CGEventCreateKeyboardEvent(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	if cmd.Modifiers != 0 {
		CGEventSetFlags(event, uint64(cmd.Modifiers))
	}

	// Try both delivery methods: first activate the app, then post through
	// the HID event tap (window server path). Also post to PID as fallback.
	if err := ensureCGInit(); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}
	cgEventPost(kCGHIDEventTap, event)
	if verbose {
		fmt.Printf("[key-cgevent] posted keyCode=%d down=%v via kCGHIDEventTap\n", cmd.KeyCode, cmd.KeyDown)
	}

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// sendKeyEventNSEvent uses AppKit NSEvent for view-level keyboard injection.
// All AppKit calls are dispatched to the main thread.
func (s *ControlServer) sendKeyEventNSEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	// Create a CGEvent with the correct virtual keycode. CGEventCreateKeyboardEvent
	// is a C function registered via purego.RegisterLibFunc, which handles uint16
	// argument passing correctly (unlike objc.Send which has ARM64 stack-passing bugs
	// for arguments beyond position 8).
	cgEvent, err := CGEventCreateKeyboardEvent(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("create CGEvent: %v", err)}
	}
	if cgEvent == 0 {
		return &controlpb.ControlResponse{Error: "CGEventCreateKeyboardEvent returned nil"}
	}

	// Set Unicode string on the CGEvent for character input.
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
		}
	}
	if chars != "" {
		CGEventKeyboardSetUnicodeString(cgEvent, chars)
	}

	// Set modifier flags if specified.
	if cmd.Modifiers != 0 {
		CGEventSetFlags(cgEvent, uint64(cmd.Modifiers))
	}

	// Convert CGEvent to NSEvent and deliver to VZVirtualMachineView on the main thread.
	var resp *controlpb.ControlResponse
	done := make(chan struct{})
	DispatchAsync(GetMainDispatchQueue(), func() {
		defer close(done)

		// Convert CGEvent → NSEvent via +[NSEvent eventWithCGEvent:]
		eventID := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSEvent")),
			objc.Sel("eventWithCGEvent:"),
			uintptr(cgEvent),
		)
		if eventID == 0 {
			resp = &controlpb.ControlResponse{Error: "NSEvent eventWithCGEvent: returned nil"}
			return
		}
		event := appkit.NSEventFromID(eventID)

		// Make sure the VM view is first responder.
		s.window.MakeKeyAndOrderFront(nil)
		s.window.MakeFirstResponder(vmViewAsNSView(s.vmView).NSResponder)

		actualKeyCode := event.KeyCode()
		if verbose {
			fmt.Printf("[key-nsevent] vmView=%x keyCode=%d actualKeyCode=%d down=%v chars=%q\n",
				s.vmView.ID, cmd.KeyCode, actualKeyCode, cmd.KeyDown, chars)
		}

		// Route through VZVirtualMachineView's keyDown:/keyUp: directly,
		// matching the pattern used for mouse events (mouseDown:/mouseUp:).
		if cmd.KeyDown {
			objc.Send[struct{}](s.vmView.ID, objc.Sel("keyDown:"), event.ID)
		} else {
			objc.Send[struct{}](s.vmView.ID, objc.Sel("keyUp:"), event.ID)
		}

		if verbose {
			fmt.Printf("[key-nsevent] sent successfully\n")
		}

		resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	})
	<-done
	return resp
}

// sendMouseEvent sends a mouse event to the VM.
// Uses VZVirtualMachine's private sendPointerNSEvent:pointingDeviceIndex:
// to deliver mouse events directly through the VM's input pipeline,
// bypassing CGEvent entirely. Falls back to CGEvent if the VM doesn't
// support the private API.
func (s *ControlServer) sendMouseEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	if s.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}
	if s.window.ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}

	// Handle click as move+down+up sequence.
	if cmd.Action == "click" {
		moveCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "move", Absolute: cmd.Absolute,
		}
		moveResp := s.sendMouseEvent(moveCmd)
		if !moveResp.Success {
			return moveResp
		}
		time.Sleep(20 * time.Millisecond)

		downCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "down", Absolute: cmd.Absolute,
		}
		downResp := s.sendMouseEvent(downCmd)
		if !downResp.Success {
			return downResp
		}
		time.Sleep(50 * time.Millisecond)
		upCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "up", Absolute: cmd.Absolute,
		}
		return s.sendMouseEvent(upCmd)
	}

	// Try the direct VM input path first (sendPointerNSEvent:pointingDeviceIndex:).
	if s.vm.ID != 0 {
		resp := s.sendMouseEventVMDirect(cmd)
		if resp != nil {
			return resp
		}
	}

	// Fall back to the CGEvent path.
	return s.sendMouseEventCGEvent(cmd)
}

// sendMouseEventVMDirect creates an NSEvent and sends it directly to the
// VZVirtualMachine via the private sendPointerNSEvent:pointingDeviceIndex:.
func (s *ControlServer) sendMouseEventVMDirect(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	var resp *controlpb.ControlResponse
	done := make(chan struct{})
	DispatchAsync(GetMainDispatchQueue(), func() {
		defer close(done)

		bounds := vmViewAsNSView(s.vmView).Bounds()

		// Calculate view-local coordinates.
		// Callers use top-left origin (x=0,y=0 is top-left, normalized 0-1).
		// NSEvent locationInWindow uses bottom-left origin, so we flip Y.
		//
		// The NSView bounds height (800) includes the title bar area, but
		// callers send normalized coordinates relative to the VM content area
		// (768px, matching the cropped screenshot). Map Y against the content
		// height so OCR coordinates translate correctly to click targets.
		contentH := float64(s.viewContentHeight)
		if contentH <= 0 {
			contentH = bounds.Size.Height // fallback if not set
		}
		var viewX, viewY float64
		if cmd.Absolute {
			viewX = cmd.X
			viewY = contentH - cmd.Y
		} else {
			viewX = cmd.X * bounds.Size.Width
			viewY = (1.0 - cmd.Y) * contentH
		}

		if verbose {
			fmt.Printf("[mouse-direct] bounds=%.0fx%.0f contentH=%.0f input=(%.3f,%.3f) view=(%.1f,%.1f) action=%s\n",
				bounds.Size.Width, bounds.Size.Height, contentH, cmd.X, cmd.Y, viewX, viewY, cmd.Action)
		}

		location := corefoundation.CGPoint{X: viewX, Y: viewY}

		var eventType appkit.NSEventType
		switch cmd.Action {
		case "down":
			if cmd.Button == 1 {
				eventType = appkit.NSEventTypeRightMouseDown
			} else {
				eventType = appkit.NSEventTypeLeftMouseDown
			}
		case "up":
			if cmd.Button == 1 {
				eventType = appkit.NSEventTypeRightMouseUp
			} else {
				eventType = appkit.NSEventTypeLeftMouseUp
			}
		case "move":
			eventType = appkit.NSEventTypeMouseMoved
		default:
			resp = &controlpb.ControlResponse{Error: fmt.Sprintf("unknown mouse action: %s", cmd.Action)}
			return
		}

		windowNumber := s.window.WindowNumber()

		// Create NSEvent for mouse
		iEvent := appkit.GetNSEventClass().MouseEventWithTypeLocationModifierFlagsTimestampWindowNumberContextEventNumberClickCountPressure(
			eventType,
			location,
			0, // modifiers
			0, // timestamp (0 = now)
			int(windowNumber),
			nil,          // context
			0,            // eventNumber
			1,            // clickCount
			float32(1.0), // pressure
		)
		event := appkit.NSEventFromID(iEvent.GetID())
		if event.ID == 0 {
			resp = &controlpb.ControlResponse{Error: "failed to create mouse NSEvent"}
			return
		}

		// Route through VZVirtualMachineView's own event methods.
		// The view's mouseDown:/mouseUp:/mouseMoved: implementations handle
		// coordinate conversion and call the VM's internal HID path correctly.
		switch cmd.Action {
		case "down":
			if cmd.Button == 1 {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("rightMouseDown:"), event.ID)
			} else {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("mouseDown:"), event.ID)
			}
		case "up":
			if cmd.Button == 1 {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("rightMouseUp:"), event.ID)
			} else {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("mouseUp:"), event.ID)
			}
		case "move":
			objc.Send[struct{}](s.vmView.ID, objc.Sel("mouseMoved:"), event.ID)
		}
		resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	})
	<-done
	return resp
}

// sendMouseEventCGEvent sends a mouse event using CGEvent (legacy path).
func (s *ControlServer) sendMouseEventCGEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	// Read window/view geometry on the main thread.
	var windowFrame corefoundation.CGRect
	var bounds corefoundation.CGRect
	var screenHeight float64
	done := make(chan struct{})
	DispatchAsync(GetMainDispatchQueue(), func() {
		defer close(done)
		s.window.MakeKeyAndOrderFront(nil)
		windowFrame = s.window.Frame()
		bounds = vmViewAsNSView(s.vmView).Bounds()
		mainScreen := appkit.GetNSScreenClass().MainScreen()
		screenHeight = mainScreen.Frame().Size.Height
	})
	<-done

	var viewX, viewY float64
	if cmd.Absolute {
		viewX = cmd.X
		viewY = cmd.Y
	} else {
		viewX = cmd.X * bounds.Size.Width
		viewY = cmd.Y * bounds.Size.Height
	}

	screenX := windowFrame.Origin.X + viewX
	windowTop := screenHeight - (windowFrame.Origin.Y + windowFrame.Size.Height)
	screenY := windowTop + 22 + viewY

	position := corefoundation.CGPoint{X: screenX, Y: screenY}

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
	event, err := CGEventCreateMouseEvent(0, eventType, position, mouseButton)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	// Post through the system HID event tap so events travel the same
	// path as real mouse input (window server → focused app → key window).
	if err := ensureCGInit(); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}
	cgEventPost(kCGHIDEventTap, event)

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// typeText types text into the VM character by character. Each character is
// mapped to its macOS virtual keycode and sent via sendKeyEventNSEvent which
// creates CGEvents (for correct keyCodes) and delivers them as NSEvents
// directly to VZVirtualMachineView's keyDown:/keyUp: handlers.
func (s *ControlServer) typeText(cmd *controlpb.TextCommand) *controlpb.ControlResponse {
	if s.vmView.ID == 0 || s.window.ID == 0 {
		return &controlpb.ControlResponse{Error: "text input requires GUI mode (run with -gui)"}
	}

	fmt.Printf("[typeText] typing %d chars: %q\n", len([]rune(cmd.Text)), cmd.Text)

	for _, ch := range cmd.Text {
		info, ok := charToKeyCode[ch]
		if !ok {
			fmt.Printf("[typeText] no keycode for %q, skipping\n", ch)
			continue
		}

		var mods uint32
		if info.shift {
			mods = uint32(ModifierShift)
		}

		downCmd := &controlpb.KeyCommand{
			KeyCode:   uint32(info.keyCode),
			KeyDown:   true,
			Modifiers: mods,
			Character: string(ch),
		}
		s.sendKeyEventNSEvent(downCmd)
		time.Sleep(30 * time.Millisecond)

		upCmd := &controlpb.KeyCommand{
			KeyCode:   uint32(info.keyCode),
			KeyDown:   false,
			Modifiers: mods,
			Character: string(ch),
		}
		s.sendKeyEventNSEvent(upCmd)
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Printf("[typeText] done typing %q\n", cmd.Text)
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// charKeyCodeInfo holds keycode and shift state for a character.
type charKeyCodeInfo struct {
	keyCode uint16
	shift   bool
}

// charToKeyCode maps ASCII characters to macOS virtual keycodes.
var charToKeyCode = map[rune]charKeyCodeInfo{
	'a': {0, false}, 'b': {11, false}, 'c': {8, false}, 'd': {2, false},
	'e': {14, false}, 'f': {3, false}, 'g': {5, false}, 'h': {4, false},
	'i': {34, false}, 'j': {38, false}, 'k': {40, false}, 'l': {37, false},
	'm': {46, false}, 'n': {45, false}, 'o': {31, false}, 'p': {35, false},
	'q': {12, false}, 'r': {15, false}, 's': {1, false}, 't': {17, false},
	'u': {32, false}, 'v': {9, false}, 'w': {13, false}, 'x': {7, false},
	'y': {16, false}, 'z': {6, false},
	'A': {0, true}, 'B': {11, true}, 'C': {8, true}, 'D': {2, true},
	'E': {14, true}, 'F': {3, true}, 'G': {5, true}, 'H': {4, true},
	'I': {34, true}, 'J': {38, true}, 'K': {40, true}, 'L': {37, true},
	'M': {46, true}, 'N': {45, true}, 'O': {31, true}, 'P': {35, true},
	'Q': {12, true}, 'R': {15, true}, 'S': {1, true}, 'T': {17, true},
	'U': {32, true}, 'V': {9, true}, 'W': {13, true}, 'X': {7, true},
	'Y': {16, true}, 'Z': {6, true},
	'0': {29, false}, '1': {18, false}, '2': {19, false}, '3': {20, false},
	'4': {21, false}, '5': {23, false}, '6': {22, false}, '7': {26, false},
	'8': {28, false}, '9': {25, false},
	' ': {49, false}, '-': {27, false}, '=': {24, false}, '[': {33, false},
	']': {30, false}, '\\': {42, false}, ';': {41, false}, '\'': {39, false},
	',': {43, false}, '.': {47, false}, '/': {44, false}, '`': {50, false},
	'!': {18, true}, '@': {19, true}, '#': {20, true}, '$': {21, true},
	'%': {23, true}, '^': {22, true}, '&': {26, true}, '*': {28, true},
	'(': {25, true}, ')': {29, true}, '_': {27, true}, '+': {24, true},
}

// GetControlSocketPath returns the default socket path
func GetControlSocketPath() string {
	return GetControlSocketPathForVM(vmDir)
}

// GetControlSocketPathForVM returns the control socket path for a specific VM dir.
func GetControlSocketPathForVM(vmDirectory string) string {
	return filepath.Join(vmDirectory, "control.sock")
}

// GetControlTokenPath returns the default control token file path.
func GetControlTokenPath() string {
	return GetControlTokenPathForVM(vmDir)
}

// GetControlTokenPathForVM returns the control token file path for a specific VM dir.
func GetControlTokenPathForVM(vmDirectory string) string {
	return filepath.Join(vmDirectory, controlTokenFileName)
}

// LoadControlTokenForVM reads the control token for a specific VM directory.
func LoadControlTokenForVM(vmDirectory string) (string, error) {
	return LoadControlTokenFromPath(GetControlTokenPathForVM(vmDirectory))
}

// LoadControlTokenFromPath reads a control token file and trims whitespace.
func LoadControlTokenFromPath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("empty token in %s", path)
	}
	return token, nil
}

// EnsureControlTokenForVM returns a VM control token, creating one if needed.
// VZ_MACOS_CTL_TOKEN overrides file loading/generation.
func EnsureControlTokenForVM(vmDirectory string) (string, error) {
	if token := strings.TrimSpace(os.Getenv(controlTokenEnvVar)); token != "" {
		tokenPath := GetControlTokenPathForVM(vmDirectory)
		if err := os.MkdirAll(vmDirectory, 0755); err != nil {
			return "", fmt.Errorf("create vm dir %s: %w", vmDirectory, err)
		}
		if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
			return "", fmt.Errorf("write %s: %w", tokenPath, err)
		}
		return token, nil
	}

	tokenPath := GetControlTokenPathForVM(vmDirectory)
	token, err := LoadControlTokenFromPath(tokenPath)
	if err == nil {
		return token, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	token, err = generateControlToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(vmDirectory, 0755); err != nil {
		return "", fmt.Errorf("create vm dir %s: %w", vmDirectory, err)
	}
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		return "", fmt.Errorf("write %s: %w", tokenPath, err)
	}
	return token, nil
}

func generateControlToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
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
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_Status{Status: &controlpb.StatusResponse{
			State:          state.String(),
			CanPause:       canPause,
			CanResume:      canResume,
			CanStop:        canStop,
			CanRequestStop: canRequestStop,
		}},
	}
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
		return &controlpb.ControlResponse{Success: true, Data: "paused", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "paused"}}}
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
		return &controlpb.ControlResponse{Success: true, Data: "resumed", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "resumed"}}}
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
		return &controlpb.ControlResponse{Success: true, Data: "stopped", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "stopped"}}}
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

	return &controlpb.ControlResponse{Success: true, Data: "stop requested (ACPI power button sent)", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "stop requested (ACPI power button sent)"}}}
}

// handleNetworkInfo returns the VM's network configuration including MAC address
// and optionally the guest IP address (if the agent is available).
func (s *ControlServer) handleNetworkInfo() *controlpb.ControlResponse {
	info := &controlpb.NetworkInfoResponse{
		Mode: networkMode,
	}

	// Read MAC address from saved file
	macPath := filepath.Join(s.effectiveVMDir(), "mac.address")
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
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result:  &controlpb.ControlResponse_NetworkInfo{NetworkInfo: info},
	}
}

func (s *ControlServer) getCapabilities() *controlpb.ControlResponse {
	payload := map[string]any{
		"protocolVersion": "vz.control.v1",
		"encoding":        "protojson",
		"auth": map[string]any{
			"required":    s.authToken != "",
			"field":       "auth_token",
			"legacyField": "token",
			"tokenPath":   GetControlTokenPathForVM(s.effectiveVMDir()),
		},
		"legacyJsonCompat": map[string]bool{
			"screenshotFlatFields": true,
			"snapshotDataField":    true,
			"memoryDataField":      true,
			"agentExecFlatFields":  true,
			"tokenField":           true,
		},
		"features": map[string]bool{
			"agentExecStream": true,
			"screenshotDiff":  true,
			"snapshots":       true,
			"memoryBalloon":   true,
		},
		"commands": []string{
			"ping", "status", "capabilities", "screenshot", "key", "mouse", "text",
			"pause", "resume", "stop", "request-stop", "snapshot", "memory", "network-info",
			"shared-folders-apply",
			"agent-connect", "agent-ping", "agent-info", "agent-exec", "agent-exec-stream",
			"agent-read", "agent-write", "agent-cp", "agent-shutdown", "agent-reboot",
			"agent-sshd", "agent-mount-volumes",
		},
	}
	commands := []string{
		"ping", "status", "capabilities", "screenshot", "key", "mouse", "text",
		"pause", "resume", "stop", "request-stop", "snapshot", "memory", "network-info",
		"shared-folders-apply",
		"agent-connect", "agent-ping", "agent-info", "agent-exec", "agent-exec-stream",
		"agent-read", "agent-write", "agent-cp", "agent-shutdown", "agent-reboot",
		"agent-sshd", "agent-mount-volumes",
	}
	features := map[string]bool{
		"agentExecStream": true,
		"screenshotDiff":  true,
		"snapshots":       true,
		"memoryBalloon":   true,
	}

	data, _ := json.Marshal(payload)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_Capabilities{Capabilities: &controlpb.CapabilitiesResponse{
			ProtocolVersion: "vz.control.v1",
			Encoding:        "protojson",
			Commands:        commands,
			Features:        features,
			AuthRequired:    s.authToken != "",
		}},
	}
}

// Global control server instance
var controlServer *ControlServer
