// control_socket.go - Socket-based control for keyboard, mouse, and screenshots
package main

import (
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
	"sync/atomic"
	"time"

	"github.com/tmc/apple/objc"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	ocrx "github.com/tmc/apple/x/vzkit/ocr"
	"github.com/tmc/apple/x/vzkit/vm"
	"github.com/tmc/apple/x/vzkit/vminput"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	controlx "github.com/tmc/vz-macos/internal/control"
	"github.com/tmc/vz-macos/internal/control/operations"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const (
	controlTokenFileName = "control.token"
	controlTokenEnvVar   = "VZ_MACOS_CTL_TOKEN"
)

var (
	protojsonMarshaler   = controlx.ProtoJSONMarshaler
	protojsonUnmarshaler = controlx.ProtoJSONUnmarshaler
)

// ControlServer manages the Unix socket for VM control
type ControlServer struct {
	socketPath        string
	vmDir             string
	authToken         string
	controlServer     *controlx.Server
	listener          net.Listener
	vmView            vz.VZVirtualMachineView
	window            appkit.NSWindow
	vm                vz.VZVirtualMachine
	vmQueue           dispatch.Queue
	mu                sync.Mutex
	agentMu           sync.RWMutex // protects agent connection setup; RLock for concurrent RPCs
	screenshotMu      sync.Mutex   // protects lastScreenshot for diff mode
	running           atomic.Bool
	lastScreenshot    image.Image                 // For diff mode
	lastCaptureWidth  int                         // last screenshot width used for OCR/click mapping
	lastCaptureHeight int                         // last screenshot height used for OCR/click mapping
	agent             *agentstate.AgentClient     // GRPC client to guest agent daemon (nil until connected)
	userAgent         *agentstate.UserAgentClient // GRPC client to guest user agent (nil until connected)
	ocr               *ocrx.Service               // lazily created OCR service for server-side OCR commands
	iterm2Proxy       *ITerm2Proxy                // WebSocket-to-vsock relay for iTerm2 API (nil until started)
	portForwards      *PortForwardManager         // host TCP -> guest vsock port forwards (nil until first use)
	vncStatus         VNCStatus
	debugStubStatus   DebugStubStatus
	windowNum         int              // cached window number for thread-safe screenshot
	viewContentHeight int              // cached view content height in pixels (excludes title bar)
	healthMu          sync.RWMutex     // protects agentHealth
	agentHealth       agentHealthState // proactive health monitoring state
	windowTitleMu     sync.RWMutex
	windowTitleBase   string
	windowTitleState  string
	windowTitleLabel  string
	policyMu          sync.Mutex
	policyStartedAt   time.Time
	policyExecCount   int64
	policyStopIssued  bool
	gui               VMGUIController
	captureMode       atomic.Int32
	inputMode         atomic.Int32
	activeConnections atomic.Int32
	httpListeners     *httpListeners // TCP listeners started by StartHTTP
	lifecycleMu       sync.RWMutex
	lifecycleCtx      context.Context
	lifecycleCancel   context.CancelFunc

	opsMu  sync.Mutex                    // guards opsReg lazy init
	opsReg *operations.OperationRegistry // file-backed at <vmDir>/operations/, lazy
}

// agentHealthState tracks proactive agent health monitoring.
type agentHealthState struct {
	daemonStatus     string // "connected", "disconnected", "reconnecting"
	userStatus       string // "connected", "disconnected", "unknown"
	guiSession       guiSession
	guiSessionActive bool
	lastPing         time.Time // last successful daemon ping
	disconnectAt     time.Time // first ping failure since the last successful ping; zero when connected
	lastErr          string    // last ping error (empty if healthy)
	version          string    // agent version from last successful ping
	versionChecked   bool      // true after first version comparison
	upgradeAttempted bool      // true after auto-upgrade attempt
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
	ctx, cancel := context.WithCancel(context.Background())
	s := &ControlServer{
		socketPath:      socketPath,
		vmDir:           vmDirectory,
		lifecycleCtx:    ctx,
		lifecycleCancel: cancel,
	}
	capture, input := resolveAutomationBackends()
	s.setCaptureBackend(capture)
	s.setInputBackend(input)
	return s
}

func (s *ControlServer) lifecycleContext() context.Context {
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	if s.lifecycleCtx == nil {
		return context.Background()
	}
	return s.lifecycleCtx
}

func (s *ControlServer) timeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.lifecycleContext(), timeout)
}

func (s *ControlServer) captureBackend() automationBackendMode {
	return automationBackendMode(s.captureMode.Load())
}

func (s *ControlServer) setCaptureBackend(mode automationBackendMode) {
	s.captureMode.Store(int32(mode))
}

func (s *ControlServer) inputBackend() automationBackendMode {
	return automationBackendMode(s.inputMode.Load())
}

func (s *ControlServer) setInputBackend(mode automationBackendMode) {
	s.inputMode.Store(int32(mode))
}

func (s *ControlServer) rememberCaptureBounds(img image.Image) {
	if img == nil {
		return
	}
	b := img.Bounds()
	s.screenshotMu.Lock()
	s.lastCaptureWidth = b.Dx()
	s.lastCaptureHeight = b.Dy()
	s.screenshotMu.Unlock()
}

func (s *ControlServer) lastCaptureBounds() (width, height int) {
	s.screenshotMu.Lock()
	defer s.screenshotMu.Unlock()
	return s.lastCaptureWidth, s.lastCaptureHeight
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
	if window.ID != 0 {
		s.windowNum = int(window.WindowNumber())
	} else {
		s.windowNum = 0
	}
	if view.ID != 0 {
		s.viewContentHeight = int(vmViewAsNSView(view).Bounds().Size.Height)
	} else {
		s.viewContentHeight = 0
	}
	if verbose {
		fmt.Printf("[control] SetVMViewWithWindow: vmView=%x window=%x windowNum=%d viewH=%d\n",
			view.ID, window.ID, s.windowNum, s.viewContentHeight)
	}
}

// SetVM sets the VM and dispatch queue for lifecycle operations (pause/resume/stop)
func (s *ControlServer) SetVM(vm vz.VZVirtualMachine, queue dispatch.Queue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vm = vm
	s.vmQueue = queue
}

func (s *ControlServer) setPolicyStartTime(now time.Time) {
	s.policyMu.Lock()
	if s.policyStartedAt.IsZero() {
		s.policyStartedAt = now
	}
	s.policyMu.Unlock()
}

func (s *ControlServer) policySnapshot() (time.Time, int64, bool) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	return s.policyStartedAt, s.policyExecCount, s.policyStopIssued
}

func (s *ControlServer) notePolicyExec() {
	s.policyMu.Lock()
	s.policyExecCount++
	s.policyMu.Unlock()
}

func (s *ControlServer) markPolicyStopIssued() bool {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if s.policyStopIssued {
		return false
	}
	s.policyStopIssued = true
	return true
}

func (s *ControlServer) SetWindowTitleBase(base string) {
	s.windowTitleMu.Lock()
	defer s.windowTitleMu.Unlock()
	s.windowTitleBase = strings.TrimSpace(base)
}

func (s *ControlServer) SetWindowTitleState(state string) {
	s.windowTitleMu.Lock()
	defer s.windowTitleMu.Unlock()
	s.windowTitleState = strings.TrimSpace(state)
}

func (s *ControlServer) SetWindowTitleLabel(label string) {
	s.windowTitleMu.Lock()
	defer s.windowTitleMu.Unlock()
	s.windowTitleLabel = strings.TrimSpace(label)
}

func (s *ControlServer) WindowTitle() string {
	s.windowTitleMu.RLock()
	defer s.windowTitleMu.RUnlock()
	var parts []string
	if s.windowTitleBase != "" {
		parts = append(parts, s.windowTitleBase)
	}
	if s.windowTitleState != "" {
		parts = append(parts, s.windowTitleState)
	}
	if s.windowTitleLabel != "" {
		parts = append(parts, s.windowTitleLabel)
	}
	return strings.Join(parts, " — ")
}

// Start begins listening on the Unix socket
func (s *ControlServer) Start() error {
	s.lifecycleMu.Lock()
	if s.lifecycleCtx == nil || s.lifecycleCancel == nil {
		s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(context.Background())
	}
	lifecycleCtx := s.lifecycleCtx
	s.lifecycleMu.Unlock()
	if s.authToken == "" {
		token, err := EnsureControlTokenForVM(s.effectiveVMDir())
		if err != nil {
			return fmt.Errorf("control token: %w", err)
		}
		s.authToken = token
	}
	s.setPolicyStartTime(vmLifecycleClock.Now())

	s.controlServer = &controlx.Server{
		SocketPath:    s.socketPath,
		Verbose:       verbose,
		AuthTokenPath: GetControlTokenPathForVM(s.effectiveVMDir()),
		Handler:       s,
		HealthMonitor: s.agentHealthMonitor,
		AcceptError: func(err error) {
			fmt.Printf("Accept error: %v\n", err)
		},
		Started: func() {
			if verbose {
				fmt.Printf("Control socket listening at: %s\n", s.socketPath)
				fmt.Printf("Control auth token: %s\n", GetControlTokenPathForVM(s.effectiveVMDir()))
			}
		},
	}
	s.running.Store(true)
	return s.controlServer.Start(lifecycleCtx)
}

// Stop closes the control server
func (s *ControlServer) Stop() {
	s.running.Store(false)
	if s.iterm2Proxy != nil {
		ctx, cancel := s.timeoutContext(5 * time.Second)
		s.iterm2Proxy.Stop(ctx)
		cancel()
		s.iterm2Proxy = nil
	}
	s.lifecycleMu.Lock()
	lifecycleCancel := s.lifecycleCancel
	s.lifecycleCancel = nil
	s.lifecycleCtx = nil
	s.lifecycleMu.Unlock()
	if lifecycleCancel != nil {
		lifecycleCancel()
	}
	if portForwards := s.clearPortForwardManager(); portForwards != nil {
		portForwards.StopAll()
	}
	if s.httpListeners != nil {
		s.httpListeners.closeAll()
	}
	if s.listener != nil {
		s.listener.Close()
	}
	if s.controlServer != nil {
		s.controlServer.Stop()
		s.controlServer = nil
	} else {
		os.Remove(s.socketPath)
	}
}

func (s *ControlServer) clearPortForwardManager() *PortForwardManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	portForwards := s.portForwards
	s.portForwards = nil
	return portForwards
}

func (s *ControlServer) handleConnection(conn net.Conn) {
	controlx.ServeConnection(conn, s)
}

func writeResponse(conn net.Conn, resp *controlpb.ControlResponse) error {
	return controlx.WriteResponse(conn, resp)
}

func populateLegacyRequestPayloads(line string, req *controlpb.ControlRequest) {
	controlx.PopulateLegacyRequestPayloads(line, req)
}

func (s *ControlServer) Authorize(token string) bool {
	return s.authorizeRequest(token)
}

func (s *ControlServer) HandleStream(conn net.Conn, req *controlpb.ControlRequest, raw []byte) (bool, bool) {
	if req.Type == "agent-exec-stream" || req.Type == "agent-user-exec-stream" {
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return true, true
		}
		s.handleAgentExecStreamConnection(conn, req)
		return true, false
	}

	if req.Type == "agent-exec-attach" {
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return true, true
		}
		s.handleAgentExecAttachConnection(conn, raw)
		return true, true
	}
	return false, false
}

func (s *ControlServer) HandleRaw(req *controlpb.ControlRequest, raw []byte) (*controlpb.ControlResponse, bool) {
	if req.Type == "ocr" {
		if ocrCmd := req.GetOcr(); ocrCmd != nil {
			mapped := &controlpb.ControlRequest{Type: "ocr-" + ocrCmd.Action}
			fakeJSON, _ := json.Marshal(map[string]any{
				"type": "ocr-" + ocrCmd.Action,
				"data": map[string]string{"text": ocrCmd.Text, "timeout": ocrCmd.Timeout},
			})
			if resp, ok := s.handleOCRSocketCommand(mapped, fakeJSON); ok {
				return resp, true
			}
		}
		return &controlpb.ControlResponse{Error: "missing ocr command payload"}, true
	}

	if resp, ok := s.handleOCRSocketCommand(req, raw); ok {
		return resp, true
	}

	if req.Type == "iterm2-proxy-start" {
		port := parseITerm2ProxyPort(raw)
		if port > 0 {
			return s.handleITerm2ProxyStartWithPort(port), true
		}
		return s.handleITerm2ProxyStart(), true
	}

	switch req.Type {
	case "disk":
		return s.handleDiskJSONRequest(raw), true
	case "pit":
		return s.handlePITJSONRequest(raw), true
	case "usb":
		return s.handleRuntimeUSBJSONRequest(raw), true
	case "agent-exec-resize":
		return s.handleAgentExecResizeJSON(raw), true
	case "agent-exec-signal":
		return s.handleAgentExecSignalJSON(raw), true
	}
	return nil, false
}

func (s *ControlServer) Handle(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	return s.handleRequest(req)
}

func (s *ControlServer) Event(reqType string, resp *controlpb.ControlResponse) {
	teeControlEvent(reqType, resp)
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
	case "vnc-status":
		return s.handleVNCStatus()
	case "debug-stub-status":
		return s.handleDebugStubStatus()
	case "ping":
		return &controlpb.ControlResponse{Success: true, Data: "pong", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "pong"}}}
	case "status":
		return s.getVMStatus()
	case "capabilities":
		return s.getCapabilities()
	case "window-label":
		label := ""
		if cmd := req.GetText(); cmd != nil {
			label = cmd.Text
		}
		s.SetWindowTitleLabel(label)
		return &controlpb.ControlResponse{Success: true, Data: label, Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: label}}}
	case "reboot-to-recovery", "boot-recovery":
		return s.rebootToRecovery()
	case "shared-folders-apply":
		return s.handleSharedFoldersApply()
	case "shared-folders-runtime-status":
		return s.handleSharedFoldersRuntimeStatus()
	case "iterm2-proxy-stop":
		return s.handleITerm2ProxyStop()
	case "iterm2-proxy-status":
		return s.handleITerm2ProxyStatus()
	case "gui-open", "gui-close", "gui-status",
		"gui-backend-auto", "gui-backend-framebuffer", "gui-backend-window",
		"gui-capture-backend-auto", "gui-capture-backend-framebuffer", "gui-capture-backend-window",
		"gui-input-backend-auto", "gui-input-backend-direct", "gui-input-backend-window":
		return s.handleGUIRequest(req.Type)
	case "port-forward":
		cmd := req.GetPortForward()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing port-forward command payload"}
		}
		return s.handlePortForward(cmd)
	case "operations":
		cmd := req.GetOperations()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing operations command payload"}
		}
		return s.handleOperationsCommand(cmd)
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
		// typeText acquires s.mu per key event to match ctl-key cadence;
		// the outer lock is released for the duration of the call.
		s.mu.Unlock()
		resp := s.typeText(cmd)
		s.mu.Lock()
		return resp
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
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown command type: %s\nValid commands include ping, status, capabilities, screenshot, key, mouse, text, pause, resume, stop, snapshot, memory, and network-info.", req.Type)}
	}
}

// getOCR returns the lazily-initialized OCR service.
func (s *ControlServer) getOCR() *ocrx.Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ocr == nil {
		s.ocr = ocrx.NewService(verbose)
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
		opts, err := ocrx.ParseSearchOptions(p.Region)
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
		opts, err := ocrx.ParseSearchOptions(p.Region)
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
		opts, err := ocrx.ParseSearchOptions(p.Region)
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
			img, errMsg := s.captureDisplayImage()
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
		img, errMsg := s.captureDisplayImage()
		if errMsg != "" {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("capture: %s", errMsg)}, true
		}
		state := DetectScreenStateOCR(img, ocr)
		return &controlpb.ControlResponse{Success: true, Data: state.String(), Result: &controlpb.ControlResponse_ScreenDetection{ScreenDetection: &controlpb.ScreenDetectionResponse{State: state.String()}}}, true
	}
	return nil, false
}

func (s *ControlServer) authorizeRequest(token string) bool {
	if s.authToken == "" {
		return true
	}
	provided := strings.TrimSpace(token)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.authToken)) == 1
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

	// For non-modifier keys, choose the configured automation backend unless
	// the caller explicitly asks for CGEvent-style fallback behavior.
	return s.sendKeyEventPrimitive(cmd)
}

func (s *ControlServer) sendKeyEventPrimitive(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	if s.inputBackend() == automationBackendWindow {
		return s.sendKeyEventCGEvent(cmd)
	}
	if s.inputBackend() == automationBackendFramebuffer {
		if !allowHIDKeyboard() {
			return &controlpb.ControlResponse{Error: "framebuffer keyboard input disabled by VZ_MACOS_DISABLE_HID_KEYBOARD"}
		}
		return s.sendKeyEventPrivate(cmd)
	}
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
	}
	if allowHIDKeyboard() {
		paths = append(paths, keyInjector{name: "private", fn: s.sendKeyEventPrivate})
	}
	if cmd.UseCgEvent {
		// For shortcut-style input, prefer lower-level injectors first.
		paths = []keyInjector{
			{name: "cgevent", fn: s.sendKeyEventCGEvent},
			{name: "nsevent", fn: s.sendKeyEventNSEvent},
		}
		if allowHIDKeyboard() {
			paths = append([]keyInjector{{name: "private", fn: s.sendKeyEventPrivate}}, paths...)
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

var hidKeyboardDeprecatedEnvLogOnce sync.Once
var sendKeyEventPrivateHook func(*ControlServer, *controlpb.KeyCommand) *controlpb.ControlResponse

func allowHIDKeyboard() bool {
	return !disableHIDKeyboardOptOut()
}

func disableHIDKeyboardOptOut() bool {
	legacy, ok := envBool("VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD")
	if ok {
		hidKeyboardDeprecatedEnvLogOnce.Do(func() {
			fmt.Println("VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD is deprecated; HID keyboard input is enabled by default; use VZ_MACOS_DISABLE_HID_KEYBOARD=1 to disable it")
		})
	}
	if envBoolTrue("VZ_MACOS_DISABLE_HID_KEYBOARD") {
		return true
	}
	if ok && !legacy {
		return true
	}
	return false
}

func envBoolTrue(name string) bool {
	v, ok := envBool(name)
	return ok && v
}

func envBool(name string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// sendKeyEventPrivate sends a keyboard event through the typed private
// _VZKeyboard.sendKeyEvents: path using _VZKeyEvent objects.
//
// The framebuffer backend uses this VM-local path by default. It still depends
// on private Virtualization selectors, so VZ_MACOS_DISABLE_HID_KEYBOARD=1 can
// temporarily disable it if a host/framework regression appears.
func (s *ControlServer) sendKeyEventPrivate(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	if sendKeyEventPrivateHook != nil {
		return sendKeyEventPrivateHook(s, cmd)
	}
	event, err := s.newKeyboardNSEvent(cmd)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	sender := vminput.NewSender(vm.WrapQueue(s.vmQueue), s.vm)
	if err := sender.SendKeyboardNSEvent(event, 0); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("private keyboard inject: %v", err)}
	}
	if verbose {
		fmt.Printf("[key-private] sent keyCode=%d down=%v mods=%d chars=%q\n",
			cmd.KeyCode, cmd.KeyDown, cmd.Modifiers, keyboardEventUnicodeString(cmd))
	}
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// sendKeyEventCGEvent uses Quartz CGEvent for keyboard injection.
// Events are posted through the system HID event tap so they travel
// through the window server to VZVirtualMachineView (the same path
// as real keyboard input). The VM window must be key and frontmost.
func (s *ControlServer) sendKeyEventCGEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	// Activate and focus the VM window on the main thread first.
	runOnUIThreadSync(func() {
		appkit.GetNSApplicationClass().SharedApplication().Activate()
		s.window.MakeKeyAndOrderFront(nil)
		s.window.MakeFirstResponder(vmViewAsNSView(s.vmView).NSResponder)
	})

	event, err := createKeyboardEvent(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	chars := keyboardEventUnicodeString(cmd)
	if chars != "" {
		setEventUnicodeString(event, chars)
	}
	if cmd.Modifiers != 0 {
		setEventFlags(event, uint64(cmd.Modifiers))
	}

	// Try both delivery methods: first activate the app, then post through
	// the HID event tap (window server path). Also post to PID as fallback.
	if err := postEvent(cgHIDEventTap, event); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}
	if verbose {
		fmt.Printf("[key-cgevent] posted keyCode=%d down=%v chars=%q via HID event tap\n", cmd.KeyCode, cmd.KeyDown, chars)
	}

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// sendKeyEventNSEvent uses AppKit NSEvent for view-level keyboard injection.
// All AppKit calls are dispatched to the main thread.
func (s *ControlServer) sendKeyEventNSEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	event, err := s.newKeyboardNSEvent(cmd)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	// Convert CGEvent to NSEvent and deliver to VZVirtualMachineView on the main thread.
	var resp *controlpb.ControlResponse
	runOnUIThreadSync(func() {
		// Make sure the VM view is first responder.
		s.window.MakeKeyAndOrderFront(nil)
		s.window.MakeFirstResponder(vmViewAsNSView(s.vmView).NSResponder)

		actualKeyCode := event.KeyCode()
		chars := keyboardEventUnicodeString(cmd)
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
	return resp
}

func (s *ControlServer) newKeyboardNSEvent(cmd *controlpb.KeyCommand) (appkit.NSEvent, error) {
	cgEvent, err := createKeyboardEvent(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return appkit.NSEvent{}, fmt.Errorf("create CGEvent: %v", err)
	}
	if cgEvent == 0 {
		return appkit.NSEvent{}, fmt.Errorf("create CGEvent returned nil")
	}

	chars := keyboardEventUnicodeString(cmd)
	if chars != "" {
		setEventUnicodeString(cgEvent, chars)
	}
	if cmd.Modifiers != 0 {
		setEventFlags(cgEvent, uint64(cmd.Modifiers))
	}

	eventID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSEvent")),
		objc.Sel("eventWithCGEvent:"),
		uintptr(cgEvent),
	)
	if eventID == 0 {
		return appkit.NSEvent{}, fmt.Errorf("NSEvent eventWithCGEvent: returned nil")
	}
	return appkit.NSEventFromID(eventID), nil
}

func keyboardEventUnicodeString(cmd *controlpb.KeyCommand) string {
	if cmd == nil {
		return ""
	}
	if cmd.Character != "" {
		return cmd.Character
	}
	switch cmd.KeyCode {
	case 36:
		return "\r"
	case 48:
		return "\t"
	case 51:
		return "\x7f"
	case 53:
		return "\x1b"
	case 49:
		return " "
	default:
		return ""
	}
}

// sendMouseEvent sends a mouse event to the VM.
// Uses either the direct VM input path or the host window-server CGEvent path,
// depending on the configured automation backend.
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

	switch s.inputBackend() {
	case automationBackendWindow:
		if cmd.Absolute && s.captureBackend() == automationBackendWindow {
			return s.sendMouseEventVMDirect(cmd)
		}
		return s.sendMouseEventCGEvent(cmd)
	case automationBackendFramebuffer:
		return s.sendMouseEventVMDirect(cmd)
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

func mapWindowCapturePointToViewPoint(x, y float64, captureW, captureH int, boundsW, contentH float64) (viewX, viewY float64) {
	if captureW <= 0 || captureH <= 0 || boundsW <= 0 || contentH <= 0 {
		return x, contentH - y
	}

	viewX = x * (boundsW / float64(captureW))

	topInset := float64(captureH) - contentH
	if topInset < 0 {
		topInset = 0
	}
	contentY := y - topInset
	if contentY < 0 {
		contentY = 0
	}
	if contentY > contentH {
		contentY = contentH
	}
	viewY = contentH - contentY
	return viewX, viewY
}

func mapNormalizedWindowCapturePointToViewPoint(x, y float64, captureW, captureH int, boundsW, contentH float64) (viewX, viewY float64) {
	if captureW <= 0 || captureH <= 0 {
		return x * boundsW, (1.0 - y) * contentH
	}
	return mapWindowCapturePointToViewPoint(
		x*float64(captureW),
		y*float64(captureH),
		captureW,
		captureH,
		boundsW,
		contentH,
	)
}

func needsWindowCapturePointMapping(mode automationBackendMode, captureW, captureH int, boundsW, contentH float64) bool {
	if captureW <= 0 || captureH <= 0 {
		return false
	}
	if mode == automationBackendWindow {
		return true
	}
	if mode != automationBackendAuto {
		return false
	}
	return float64(captureW) != boundsW || float64(captureH) != contentH
}

// sendMouseEventVMDirect creates an NSEvent and sends it directly to the
// VZVirtualMachine via the private sendPointerNSEvent:pointingDeviceIndex:.
// If that selector is unavailable, it falls back to routing through
// VZVirtualMachineView's mouse event handlers.
func (s *ControlServer) sendMouseEventVMDirect(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	var resp *controlpb.ControlResponse
	runOnUIThreadSync(func() {
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
		captureW, captureH := s.lastCaptureBounds()
		useWindowMapping := needsWindowCapturePointMapping(s.captureBackend(), captureW, captureH, bounds.Size.Width, contentH)

		if cmd.Absolute {
			if useWindowMapping {
				viewX, viewY = mapWindowCapturePointToViewPoint(cmd.X, cmd.Y, captureW, captureH, bounds.Size.Width, contentH)
			} else {
				viewX = cmd.X
				viewY = contentH - cmd.Y
			}
		} else {
			if useWindowMapping {
				viewX, viewY = mapNormalizedWindowCapturePointToViewPoint(cmd.X, cmd.Y, captureW, captureH, bounds.Size.Width, contentH)
			} else {
				viewX = cmd.X * bounds.Size.Width
				viewY = (1.0 - cmd.Y) * contentH
			}
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

		if s.vm.ID != 0 && objc.Send[bool](s.vm.ID, objc.Sel("respondsToSelector:"), objc.Sel("sendPointerNSEvent:pointingDeviceIndex:")) {
			if verbose {
				fmt.Printf("[mouse-hid] action=%s point=(%.1f,%.1f)\n", cmd.Action, viewX, viewY)
			}
			objc.Send[struct{}](s.vm.ID, objc.Sel("sendPointerNSEvent:pointingDeviceIndex:"), event.ID, uint32(0))
			resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
			return
		}

		// Fall back to routing through VZVirtualMachineView's own event methods.
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
	return resp
}

// sendMouseEventCGEvent sends a mouse event using CGEvent (legacy path).
func (s *ControlServer) sendMouseEventCGEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	// Read window/view geometry on the main thread.
	var windowFrame corefoundation.CGRect
	var bounds corefoundation.CGRect
	var screenHeight float64
	runOnUIThreadSync(func() {
		s.window.MakeKeyAndOrderFront(nil)
		windowFrame = s.window.Frame()
		bounds = vmViewAsNSView(s.vmView).Bounds()
		mainScreen := appkit.GetNSScreenClass().MainScreen()
		screenHeight = mainScreen.Frame().Size.Height
	})

	var viewX, viewY float64
	if cmd.Absolute {
		viewX = cmd.X
		viewY = cmd.Y
		if s.captureBackend() == automationBackendWindow {
			captureW, captureH := s.lastCaptureBounds()
			if captureW > 0 && captureH > 0 {
				viewX = cmd.X * (windowFrame.Size.Width / float64(captureW))
				viewY = cmd.Y * (windowFrame.Size.Height / float64(captureH))
			}
		}
	} else if s.captureBackend() == automationBackendWindow {
		captureW, captureH := s.lastCaptureBounds()
		if captureW > 0 && captureH > 0 {
			viewX = cmd.X * float64(captureW)
			viewY = cmd.Y * float64(captureH)
		} else {
			viewX = cmd.X * windowFrame.Size.Width
			viewY = cmd.Y * windowFrame.Size.Height
		}
	} else {
		viewX = cmd.X * bounds.Size.Width
		viewY = cmd.Y * bounds.Size.Height
	}

	screenX := windowFrame.Origin.X + viewX
	windowTop := screenHeight - (windowFrame.Origin.Y + windowFrame.Size.Height)
	screenY := windowTop + viewY
	if !cmd.Absolute && s.captureBackend() != automationBackendWindow {
		screenY += 22
	}

	if verbose {
		captureW, captureH := s.lastCaptureBounds()
		fmt.Printf("[mouse-cgevent] absolute=%v action=%s input=(%.1f,%.1f) view=(%.1f,%.1f) window=(x=%.1f y=%.1f w=%.1f h=%.1f) capture=%dx%d screen=(%.1f,%.1f)\n",
			cmd.Absolute, cmd.Action, cmd.X, cmd.Y, viewX, viewY,
			windowFrame.Origin.X, windowFrame.Origin.Y, windowFrame.Size.Width, windowFrame.Size.Height,
			captureW, captureH, screenX, screenY)
	}

	position := corefoundation.CGPoint{X: screenX, Y: screenY}

	// Map action to CGEvent mouse type
	var eventType uint32
	var mouseButton uint32 = uint32(cmd.Button)
	switch cmd.Action {
	case "move":
		eventType = cgEventMouseMoved
	case "down":
		switch cmd.Button {
		case 0:
			eventType = cgEventLeftMouseDown
		case 1:
			eventType = cgEventRightMouseDown
		default:
			return &controlpb.ControlResponse{Error: "only left (0) and right (1) buttons supported"}
		}
	case "up":
		switch cmd.Button {
		case 0:
			eventType = cgEventLeftMouseUp
		case 1:
			eventType = cgEventRightMouseUp
		default:
			return &controlpb.ControlResponse{Error: "only left (0) and right (1) buttons supported"}
		}
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown mouse action: %s", cmd.Action)}
	}

	// Create and post CGEvent
	event, err := createMouseEvent(0, eventType, position, mouseButton)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	// Post through the system HID event tap so events travel the same
	// path as real mouse input (window server → focused app → key window).
	if err := postEvent(cgHIDEventTap, event); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// typeText types text into the VM character by character. Each character is
// mapped to its macOS virtual keycode and delivered through the configured
// automation backend. The caller must not hold s.mu; typeText acquires it
// per key event so each send observes the same state as a fresh key RPC.
func (s *ControlServer) typeText(cmd *controlpb.TextCommand) *controlpb.ControlResponse {
	if s.vmView.ID == 0 || s.window.ID == 0 {
		return &controlpb.ControlResponse{Error: "text input requires GUI mode (run with -gui)"}
	}

	if verbose {
		fmt.Printf("[typeText] typing %d chars: %q\n", len([]rune(cmd.Text)), cmd.Text)
	}

	send := func(kc *controlpb.KeyCommand) *controlpb.ControlResponse {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.sendKeyEvent(kc)
	}

	for _, ch := range cmd.Text {
		info, ok := charToKeyCode[ch]
		if !ok {
			if verbose {
				fmt.Printf("[typeText] no keycode for %q, skipping\n", ch)
			}
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
		if resp := send(downCmd); resp.Error != "" {
			return resp
		}
		time.Sleep(30 * time.Millisecond)

		upCmd := &controlpb.KeyCommand{
			KeyCode:   uint32(info.keyCode),
			KeyDown:   false,
			Modifiers: mods,
			Character: string(ch),
		}
		if resp := send(upCmd); resp.Error != "" {
			return resp
		}
		time.Sleep(50 * time.Millisecond)
	}

	if verbose {
		fmt.Printf("[typeText] done typing %q\n", cmd.Text)
	}
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
	'{': {33, true}, '}': {30, true}, '|': {42, true}, '~': {50, true},
	':': {41, true}, '"': {39, true}, '<': {43, true}, '>': {47, true},
	'?':  {44, true},
	'\n': {36, false}, '\t': {48, false},
}

// GetControlSocketPath returns the default socket path
func GetControlSocketPath() string {
	return GetControlSocketPathForVM(vmDir)
}

// GetControlSocketPathForVM returns the control socket path for a specific VM dir.
func GetControlSocketPathForVM(vmDirectory string) string {
	return filepath.Join(vmDirectory, "control.sock")
}

// GetControlTokenPathForVM returns the control token file path for a specific VM dir.
func GetControlTokenPathForVM(vmDirectory string) string {
	return filepath.Join(vmDirectory, controlTokenFileName)
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
		"state":          vmStateLabel(state),
		"canPause":       canPause,
		"canResume":      canResume,
		"canStop":        canStop,
		"canRequestStop": canRequestStop,
	}
	startedAt, execCount, stopIssued := s.policySnapshot()
	status["policyStartedAt"] = startedAt.Format(time.RFC3339)
	status["policyExecCount"] = execCount
	status["policyStopIssued"] = stopIssued
	s.healthMu.RLock()
	lastPing := s.agentHealth.lastPing
	s.healthMu.RUnlock()
	status["lastPing"] = lastPing.Format(time.RFC3339)

	data, _ := json.Marshal(status)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_Status{Status: &controlpb.StatusResponse{
			State:          vmStateLabel(state),
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

func (s *ControlServer) rebootToRecovery() *controlpb.ControlResponse {
	s.mu.Lock()
	vm := s.vm
	queue := s.vmQueue
	s.mu.Unlock()
	if vm.ID == 0 || queue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	done := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		startRecovery := func() {
			if hasSuspendState() {
				moveAsideSuspendState("recovery-mode")
			}
			setActiveBootSessionMode(bootSessionModeRecovery)
			opts := vz.NewVZMacOSVirtualMachineStartOptions()
			opts.SetStartUpFromMacOSRecovery(true)
			vm.StartWithOptionsCompletionHandler(&opts.VZVirtualMachineStartOptions, func(err error) {
				done <- snapshotNSError(err)
			})
		}

		state := vz.VZVirtualMachineState(vm.State())
		switch state {
		case vz.VZVirtualMachineStateStopped:
			startRecovery()
		case vz.VZVirtualMachineStateRunning, vz.VZVirtualMachineStatePaused:
			if !vm.CanStop() {
				done <- fmt.Errorf("cannot stop VM in state: %s", state.String())
				return
			}
			vm.StopWithCompletionHandler(func(err error) {
				if err := snapshotNSError(err); err != nil {
					done <- fmt.Errorf("stop before recovery: %w", err)
					return
				}
				startRecovery()
			})
		default:
			done <- fmt.Errorf("cannot boot recovery from state: %s", state.String())
		}
	})

	select {
	case err := <-done:
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("reboot to recovery failed: %v", err)}
		}
		msg := "booted to recovery mode"
		return &controlpb.ControlResponse{
			Success: true,
			Data:    msg,
			Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}},
		}
	case <-time.After(2 * time.Minute):
		return &controlpb.ControlResponse{Error: "reboot to recovery timed out"}
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

	if a, err := s.getAgent(); err == nil {
		ctx, cancel := s.timeoutContext(5 * time.Second)
		defer cancel()
		if info.MacAddress == "" && linuxMode {
			if result, err := a.Exec(ctx, linuxGuestMACProbeArgs(), nil, ""); err == nil && result.ExitCode == 0 {
				info.MacAddress = parseGuestMAC(responseText(result.Stdout))
				if info.MacAddress != "" {
					_ = os.WriteFile(macPath, []byte(info.MacAddress+"\n"), 0644)
				}
			}
		}
		result, err := a.Exec(ctx, guestIPProbeArgs(linuxMode), nil, "")
		if err == nil && result.ExitCode == 0 {
			info.GuestIp = parseGuestIP(responseText(result.Stdout))
		}
	}

	data, _ := protojsonMarshaler.Marshal(info)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result:  &controlpb.ControlResponse_NetworkInfo{NetworkInfo: info},
	}
}

func guestIPProbeArgs(linuxGuest bool) []string {
	if linuxGuest {
		return []string{"sh", "-lc", linuxGuestIPProbeScript}
	}
	return []string{"ipconfig", "getifaddr", "en0"}
}

func linuxGuestMACProbeArgs() []string {
	return []string{"sh", "-lc", linuxGuestMACProbeScript}
}

const linuxGuestIPProbeScript = `ip=$(ip -4 -o addr show eth0 2>/dev/null | awk '{print $4}' | head -1)
if [ -z "$ip" ]; then
	ip=$(hostname -I 2>/dev/null | awk '{print $1}')
fi
printf '%s\n' "$ip"`

const linuxGuestMACProbeScript = `iface=$(ip route show default 2>/dev/null | awk '{print $5; exit}')
if [ -z "$iface" ]; then
	iface=$(ls /sys/class/net 2>/dev/null | awk '$1 != "lo" {print $1; exit}')
fi
if [ -n "$iface" ] && [ -r "/sys/class/net/$iface/address" ]; then
	cat "/sys/class/net/$iface/address"
fi`

func parseGuestIP(out string) string {
	ip := strings.TrimSpace(out)
	if i := strings.IndexByte(ip, '/'); i >= 0 {
		ip = ip[:i]
	}
	return strings.TrimSpace(ip)
}

func parseGuestMAC(out string) string {
	return strings.ToLower(strings.TrimSpace(out))
}

func (s *ControlServer) getCapabilities() *controlpb.ControlResponse {
	s.mu.Lock()
	guiAvailable := s.gui != nil
	s.mu.Unlock()
	commands := controlCapabilityCommands(linuxMode, windowsMode)
	features := controlCapabilityFeatures(guiAvailable, linuxMode, windowsMode)
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
		"features": features,
		"commands": commands,
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

func controlCapabilityCommands(linuxGuest, windowsGuest bool) []string {
	commands := []string{
		"ping", "status", "capabilities", "screenshot", "key", "mouse", "text",
		"pause", "resume", "stop", "request-stop", "snapshot", "memory", "network-info",
		"shared-folders-apply", "shared-folders-runtime-status", "gui-open", "gui-close", "gui-status", "port-forward",
		"vnc-status", "debug-stub-status", "disk", "pit", "usb",
		"agent-connect", "agent-ping", "agent-info", "agent-exec", "agent-exec-stream",
		"agent-exec-attach", "agent-exec-resize", "agent-exec-signal",
		"agent-read", "agent-write", "agent-cp", "agent-shutdown", "agent-reboot",
		"agent-sshd", "agent-mount-volumes", "agent-status",
	}
	if !linuxGuest && !windowsGuest {
		commands = append(commands, "reboot-to-recovery")
	}
	return commands
}

func controlCapabilityFeatures(guiAvailable, linuxGuest, windowsGuest bool) map[string]bool {
	return map[string]bool{
		"agentExecStream":    true,
		"screenshotDiff":     true,
		"snapshots":          true,
		"pitSnapshots":       true,
		"memoryBalloon":      true,
		"guiAttach":          guiAvailable,
		"vncStatus":          true,
		"debugStubStatus":    true,
		"runtimeDiskControl": true,
		"runtimeUSBControl":  true,
	}
}
