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

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	ocrx "github.com/tmc/apple/x/vzkit/ocr"

	controlx "github.com/tmc/vz-macos/internal/control"
	"github.com/tmc/vz-macos/internal/control/operations"
	"github.com/tmc/vz-macos/internal/controlserver"
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
	running           atomic.Bool
	capture           controlserver.Capture // diff cache + lazy OCR service, self-guarded
	bridge            agentBridge   // agent clients + health state (owns its own mutexes)
	network           networkBridge // iterm2 proxy, port forwards, HTTP listeners, VNC/debug status
	input             inputBridge   // mouse/keyboard delivery, back-references this ControlServer
	windowNum         int           // cached window number for thread-safe screenshot
	viewContentHeight int           // cached view content height in pixels (excludes title bar)
	windowTitleMu     sync.RWMutex
	windowTitleBase   string
	windowTitleState  string
	windowTitleLabel  string
	life              controlserver.Lifecycle // cancellable lifecycle ctx + policy counters (owns its own mutexes)
	gui               VMGUIController
	captureMode       atomic.Int32
	inputMode         atomic.Int32
	activeConnections atomic.Int32

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
	s := &ControlServer{
		socketPath: socketPath,
		vmDir:      vmDirectory,
	}
	s.life.Start()
	s.bridge.cs = s
	s.network.cs = s
	s.input.cs = s
	capture, input := resolveAutomationBackends()
	s.setCaptureBackend(capture)
	s.setInputBackend(input)
	return s
}

func (s *ControlServer) lifecycleContext() context.Context {
	return s.life.Context()
}

func (s *ControlServer) timeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return s.life.TimeoutContext(timeout)
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

// inputs returns the input bridge with its back-reference wired. Test
// constructors that build &ControlServer{} skip NewControlServerWithVMDir,
// so the bridge can be reached with a nil cs. The forwarders go through
// inputs() to ensure b.cs is always live.
func (s *ControlServer) inputs() *inputBridge {
	if s.input.cs != s {
		s.input.cs = s
	}
	return &s.input
}

func (s *ControlServer) rememberCaptureBounds(img image.Image) {
	s.capture.RememberBounds(img)
}

func (s *ControlServer) lastCaptureBounds() (width, height int) {
	return s.capture.LastBounds()
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
	s.life.SetPolicyStartTime(now)
}

func (s *ControlServer) policySnapshot() (time.Time, int64, bool) {
	return s.life.PolicySnapshot()
}

func (s *ControlServer) notePolicyExec() {
	s.life.NotePolicyExec()
}

func (s *ControlServer) markPolicyStopIssued() bool {
	return s.life.MarkPolicyStopIssued()
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
	lifecycleCtx := s.life.Start()
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
	proxyCtx, cancel := s.timeoutContext(5 * time.Second)
	s.network.stopITerm2Proxy(proxyCtx)
	cancel()
	s.life.Shutdown()
	s.network.stopPortForwards()
	s.network.closeHTTPListeners()
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
	return s.network.clearPortForwardManager()
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
	return s.capture.Service(verbose)
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
	return s.inputs().sendKey(cmd)
}

func (s *ControlServer) sendKeyEventPrimitive(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	return s.inputs().sendKeyPrimitive(cmd)
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

func (s *ControlServer) sendKeyEventPrivate(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	return s.inputs().sendKeyPrivate(cmd)
}

func (s *ControlServer) sendKeyEventCGEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	return s.inputs().sendKeyCGEvent(cmd)
}

func (s *ControlServer) sendKeyEventNSEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	return s.inputs().sendKeyNSEvent(cmd)
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

// sendMouseEvent forwards a mouse event to the input bridge.
func (s *ControlServer) sendMouseEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	return s.inputs().sendMouse(cmd)
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

// typeText forwards a text-typing command to the input bridge.
func (s *ControlServer) typeText(cmd *controlpb.TextCommand) *controlpb.ControlResponse {
	return s.inputs().typeText(cmd)
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
	s.bridge.healthMu.RLock()
	lastPing := s.bridge.health.lastPing
	s.bridge.healthMu.RUnlock()
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
