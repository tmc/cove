package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmc/cove/internal/rfb"

	controlx "github.com/tmc/cove/internal/control"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type windowsQEMUControlServer struct {
	server     *controlx.Server
	cancel     context.CancelFunc
	handler    *windowsQEMUControlHandler
	vmDir      string
	socketPath string
}

func startWindowsQEMUControlServer(ctx context.Context, vmDir string) (*windowsQEMUControlServer, error) {
	runCtx, cancel := context.WithCancel(ctx)
	token, err := EnsureControlTokenForVM(vmDir)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("control token: %w", err)
	}
	socketPath := GetControlSocketPathForVM(vmDir)
	if err := prepareWindowsQEMUControlSocket(vmDir, socketPath, token); err != nil {
		cancel()
		return nil, err
	}
	handler := &windowsQEMUControlHandler{vmDir: vmDir, authToken: token}
	server := &controlx.Server{
		SocketPath:    socketPath,
		Verbose:       verbose,
		AuthTokenPath: GetControlTokenPathForVM(vmDir),
		Handler:       handler,
		AcceptError: func(err error) {
			if verbose {
				fmt.Printf("QEMU control accept error: %v\n", err)
			}
		},
		Started: func() {
			if verbose {
				fmt.Printf("QEMU control socket listening at: %s\n", socketPath)
				fmt.Printf("QEMU control auth token: %s\n", GetControlTokenPathForVM(vmDir))
			}
		},
	}
	if err := server.Start(runCtx); err != nil {
		cancel()
		cleanupWindowsQEMUControlSocket(vmDir, socketPath)
		return nil, err
	}
	return &windowsQEMUControlServer{server: server, cancel: cancel, handler: handler, vmDir: vmDir, socketPath: socketPath}, nil
}

func prepareWindowsQEMUControlSocket(vmDir, socketPath, token string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return fmt.Errorf("create qemu control socket dir: %w", err)
	}
	if controlSocketUsesVMDir(vmDir, socketPath) {
		return nil
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(socketPath), controlTokenFileName), []byte(token+"\n"), 0600); err != nil {
		return fmt.Errorf("write qemu short control token: %w", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(socketPath), controlVMDirFileName), []byte(vmDir+"\n"), 0600); err != nil {
		return fmt.Errorf("write qemu short control VM dir: %w", err)
	}
	return nil
}

func (s *windowsQEMUControlServer) Stop() {
	if s == nil {
		return
	}
	if s.server != nil {
		s.server.Stop()
		s.server = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.handler != nil {
		s.handler.Close()
		s.handler = nil
	}
	cleanupWindowsQEMUControlSocket(s.vmDir, s.socketPath)
}

func (s *windowsQEMUControlServer) SocketPath() string {
	if s == nil {
		return ""
	}
	return s.socketPath
}

func cleanupWindowsQEMUControlSocket(vmDir, socketPath string) {
	if socketPath == "" || controlSocketUsesVMDir(vmDir, socketPath) {
		return
	}
	dir := filepath.Dir(socketPath)
	_ = os.Remove(filepath.Join(dir, controlTokenFileName))
	_ = os.Remove(filepath.Join(dir, controlVMDirFileName))
	_ = os.Remove(dir)
}

type windowsQEMUControlHandler struct {
	vmDir       string
	authToken   string
	rfbMu       sync.Mutex
	rfb         *rfb.Client
	rfbEndpoint string
}

func (h *windowsQEMUControlHandler) Authorize(token string) bool {
	if h.authToken == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(h.authToken)) == 1
}

func (h *windowsQEMUControlHandler) HandleStream(net.Conn, *controlpb.ControlRequest, []byte) (bool, bool) {
	return false, false
}

func (h *windowsQEMUControlHandler) HandleRaw(*controlpb.ControlRequest, []byte) (*controlpb.ControlResponse, bool) {
	return nil, false
}

func (h *windowsQEMUControlHandler) Handle(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	switch req.Type {
	case "ping":
		return qemuControlMessage("pong")
	case "status":
		return h.status()
	case "capabilities":
		return h.capabilities()
	case "screenshot":
		return h.screenshot(req.GetScreenshot())
	case "key":
		return h.key(req.GetKey())
	case "mouse":
		return h.mouse(req.GetMouse())
	case "text":
		return h.text(req.GetText())
	case "stop":
		return h.stop()
	case "request-stop":
		return h.requestStop()
	case "agent-connect":
		return h.agentPing()
	case "agent-ping":
		return h.agentPing()
	case "agent-info":
		return h.agentInfo()
	case "agent-status":
		return h.agentStatus()
	case "agent-exec", "agent-exec-auto", "agent-exec-stream":
		return h.agentExec(req.GetAgentExec(), false)
	case "agent-user-exec", "agent-user-exec-stream":
		return h.agentExec(req.GetAgentExec(), true)
	case "agent-read":
		return h.agentRead(req.GetAgentRead())
	case "agent-write":
		return h.agentWrite(req.GetAgentWrite())
	case "agent-cp":
		return h.agentCopy(req.GetAgentCp())
	case "agent-shutdown":
		return h.agentShutdown(req.GetAgentShutdown(), false)
	case "agent-reboot":
		return h.agentShutdown(nil, true)
	case "gui-status", "vnc-status":
		return h.status()
	case "gui-open":
		return h.guiOpen()
	case "pause", "resume", "snapshot", "memory", "network-info",
		"shared-folders-apply", "shared-folders-runtime-status",
		"port-forward", "server-info", "disk", "pit", "usb",
		"agent-sshd", "agent-mount-volumes",
		"agent-exec-attach", "agent-exec-resize", "agent-exec-signal":
		return qemuControlUnsupported(req.Type)
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown qemu control command type: %s", req.Type)}
	}
}

func (h *windowsQEMUControlHandler) Event(string, *controlpb.ControlResponse) {}

func (h *windowsQEMUControlHandler) Close() {
	h.rfbMu.Lock()
	defer h.rfbMu.Unlock()
	h.closeRFBLocked()
}

func (h *windowsQEMUControlHandler) closeRFBLocked() {
	if h.rfb != nil {
		_ = h.rfb.Close()
		h.rfb = nil
		h.rfbEndpoint = ""
	}
}

func (h *windowsQEMUControlHandler) status() *controlpb.ControlResponse {
	status := readWindowsQEMUCTLStatus(h.vmDir)
	data, err := json.Marshal(status)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("marshal qemu status: %v", err)}
	}
	running := status.State == "running"
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_Status{Status: &controlpb.StatusResponse{
			State:          status.State,
			CanStop:        running,
			CanRequestStop: running,
		}},
	}
}

func (h *windowsQEMUControlHandler) capabilities() *controlpb.ControlResponse {
	commands := []string{
		"ping", "status", "capabilities", "screenshot", "key", "mouse", "text",
		"stop", "request-stop", "gui-status", "gui-open", "vnc-status",
		"agent-connect", "agent-ping", "agent-info", "agent-status",
		"agent-exec", "agent-exec-auto", "agent-exec-stream",
		"agent-user-exec", "agent-user-exec-stream",
		"agent-read", "agent-write", "agent-shutdown", "agent-reboot",
		"pause", "resume", "snapshot", "memory", "network-info",
	}
	features := map[string]bool{
		"qemu":              true,
		"windows":           true,
		"authToken":         h.authToken != "",
		"screenshot":        true,
		"keyboard":          true,
		"mouse":             qemuMetadataForVMDir(h.vmDir).VNCEndpoint != "",
		"agentExec":         true,
		"agentExecStream":   false,
		"agentFile":         true,
		"snapshots":         false,
		"pause":             false,
		"memoryBalloon":     false,
		"networkInfo":       false,
		"sharedFolders":     false,
		"portForward":       false,
		"legacyJsonCompat":  true,
		"typedProtoResults": true,
	}
	payload := map[string]any{
		"protocolVersion": "vz.control.v1",
		"encoding":        "protojson",
		"auth": map[string]any{
			"required":    h.authToken != "",
			"field":       "auth_token",
			"legacyField": "token",
			"tokenPath":   GetControlTokenPathForVM(h.vmDir),
		},
		"commands": commands,
		"features": features,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("marshal qemu capabilities: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_Capabilities{Capabilities: &controlpb.CapabilitiesResponse{
			ProtocolVersion: "vz.control.v1",
			Encoding:        "protojson",
			Commands:        commands,
			Features:        features,
			AuthRequired:    h.authToken != "",
		}},
	}
}

func (h *windowsQEMUControlHandler) screenshot(cmd *controlpb.ScreenshotCommand) *controlpb.ControlResponse {
	format := "jpeg"
	if cmd != nil && strings.TrimSpace(cmd.Format) != "" {
		format = cmd.Format
	}
	img, err := captureWindowsQEMUImage(h.vmDir)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	data, err := encodeWindowsQEMUImage(img, format)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	bounds := img.Bounds()
	return &controlpb.ControlResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(data),
		Result: &controlpb.ControlResponse_ScreenshotResult{ScreenshotResult: &controlpb.ScreenshotResponse{
			ImageData: data,
			Format:    strings.ToLower(format),
			Width:     int32(bounds.Dx()),
			Height:    int32(bounds.Dy()),
		}},
	}
}

func (h *windowsQEMUControlHandler) key(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	if cmd == nil {
		return &controlpb.ControlResponse{Error: "missing key command payload"}
	}
	if !cmd.KeyDown {
		return qemuControlEmpty()
	}
	spec, err := qemuKeySpecFromControl(cmd)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	q := newWindowsQEMUAutomation(h.vmDir)
	if err := q.monitorCommand("sendkey " + spec); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return qemuControlEmpty()
}

func qemuKeySpecFromControl(cmd *controlpb.KeyCommand) (string, error) {
	if cmd.Character != "" {
		runes := []rune(cmd.Character)
		if len(runes) != 1 {
			return "", fmt.Errorf("qemu key character must be one rune")
		}
		key, err := qemuKeyForRune(runes[0])
		if err != nil {
			return "", err
		}
		return qemuKeyWithModifiers(key, cmd.Modifiers)
	}
	key, ok := qemuKeyNameForMacKeyCode(cmd.KeyCode)
	if !ok {
		return "", fmt.Errorf("unsupported qemu key code %d", cmd.KeyCode)
	}
	spec, err := qemuKeySpec(key)
	if err != nil {
		return "", err
	}
	return qemuKeyWithModifiers(spec, cmd.Modifiers)
}

func qemuKeyWithModifiers(key string, modifiers uint32) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("empty qemu key")
	}
	var parts []string
	if modifiers&(1<<18) != 0 {
		parts = append(parts, "ctrl")
	}
	if modifiers&(1<<19) != 0 {
		parts = append(parts, "alt")
	}
	if modifiers&(1<<17) != 0 {
		parts = append(parts, "shift")
	}
	if modifiers&(1<<20) != 0 {
		parts = append(parts, "meta_l")
	}
	parts = append(parts, key)
	return strings.Join(parts, "-"), nil
}

func qemuKeyNameForMacKeyCode(code uint32) (string, bool) {
	switch code {
	case 0:
		return "a", true
	case 1:
		return "s", true
	case 2:
		return "d", true
	case 3:
		return "f", true
	case 4:
		return "h", true
	case 5:
		return "g", true
	case 6:
		return "z", true
	case 7:
		return "x", true
	case 8:
		return "c", true
	case 9:
		return "v", true
	case 11:
		return "b", true
	case 12:
		return "q", true
	case 13:
		return "w", true
	case 14:
		return "e", true
	case 15:
		return "r", true
	case 16:
		return "y", true
	case 17:
		return "t", true
	case 18:
		return "1", true
	case 19:
		return "2", true
	case 20:
		return "3", true
	case 21:
		return "4", true
	case 22:
		return "6", true
	case 23:
		return "5", true
	case 24:
		return "equals", true
	case 25:
		return "9", true
	case 26:
		return "7", true
	case 27:
		return "minus", true
	case 28:
		return "8", true
	case 29:
		return "0", true
	case 30:
		return "rightbracket", true
	case 31:
		return "o", true
	case 32:
		return "u", true
	case 33:
		return "leftbracket", true
	case 34:
		return "i", true
	case 35:
		return "p", true
	case 36:
		return "return", true
	case 37:
		return "l", true
	case 38:
		return "j", true
	case 39:
		return "quote", true
	case 40:
		return "k", true
	case 41:
		return "semicolon", true
	case 42:
		return "backslash", true
	case 43:
		return "comma", true
	case 44:
		return "slash", true
	case 45:
		return "n", true
	case 46:
		return "m", true
	case 47:
		return "period", true
	case 48:
		return "tab", true
	case 49:
		return "space", true
	case 50:
		return "grave", true
	case 51:
		return "delete", true
	case 53:
		return "escape", true
	case 96:
		return "f5", true
	case 97:
		return "f6", true
	case 98:
		return "f7", true
	case 99:
		return "f3", true
	case 100:
		return "f8", true
	case 101:
		return "f9", true
	case 103:
		return "f11", true
	case 109:
		return "f10", true
	case 111:
		return "f12", true
	case 118:
		return "f4", true
	case 120:
		return "f2", true
	case 122:
		return "f1", true
	case 123:
		return "left", true
	case 124:
		return "right", true
	case 125:
		return "down", true
	case 126:
		return "up", true
	default:
		return "", false
	}
}

func (h *windowsQEMUControlHandler) mouse(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	if cmd == nil {
		return &controlpb.ControlResponse{Error: "missing mouse command payload"}
	}
	action := strings.ToLower(strings.TrimSpace(cmd.Action))
	if action == "" {
		action = "move"
	}
	if err := h.sendMouse(cmd.X, cmd.Y, action); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return qemuControlEmpty()
}

func (h *windowsQEMUControlHandler) sendMouse(x, y float64, action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.withRFBClient(ctx, func(c *rfb.Client) error {
		p := qemuRFBMousePoint(c.Size(), x, y)
		var err error
		switch action {
		case "move":
			err = c.Pointer(p.X, p.Y, 0)
		case "down":
			err = c.Pointer(p.X, p.Y, 1)
		case "up":
			err = c.Pointer(p.X, p.Y, 0)
		case "click":
			if err = c.Pointer(p.X, p.Y, 0); err == nil {
				time.Sleep(20 * time.Millisecond)
				err = c.Pointer(p.X, p.Y, 1)
			}
			if err == nil {
				time.Sleep(50 * time.Millisecond)
				err = c.Pointer(p.X, p.Y, 0)
			}
		default:
			return fmt.Errorf("unknown mouse action: %s", action)
		}
		return err
	})
}

func (h *windowsQEMUControlHandler) withRFBClient(ctx context.Context, fn func(*rfb.Client) error) error {
	h.rfbMu.Lock()
	defer h.rfbMu.Unlock()

	endpoint := qemuMetadataForVMDir(h.vmDir).VNCEndpoint
	if endpoint == "" {
		return fmt.Errorf("qemu vnc endpoint is unavailable; restart with -vnc :5901 to use VNC input")
	}
	if h.rfb == nil || h.rfbEndpoint != endpoint {
		h.closeRFBLocked()
		client, err := rfb.Dial(ctx, endpoint)
		if err != nil {
			return fmt.Errorf("connect qemu vnc: %w", err)
		}
		h.rfb = client
		h.rfbEndpoint = endpoint
	}
	if err := fn(h.rfb); err != nil {
		h.closeRFBLocked()
		return err
	}
	return nil
}

func (h *windowsQEMUControlHandler) text(cmd *controlpb.TextCommand) *controlpb.ControlResponse {
	if cmd == nil {
		return &controlpb.ControlResponse{Error: "missing text command payload"}
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_TEXT_BACKEND"))) {
	case "", "auto":
		if err := h.typeRFBText(cmd.Text); err == nil {
			return qemuControlEmpty()
		}
		if err := typeWindowsQEMUMonitorText(h.vmDir, cmd.Text); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
	case "rfb", "vnc":
		if err := h.typeRFBText(cmd.Text); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
	case "monitor", "sendkey":
		if err := typeWindowsQEMUMonitorText(h.vmDir, cmd.Text); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("invalid COVE_QEMU_TEXT_BACKEND %q (must be auto, rfb, or monitor)", os.Getenv("COVE_QEMU_TEXT_BACKEND"))}
	}
	return qemuControlEmpty()
}

func (h *windowsQEMUControlHandler) typeRFBText(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return h.withRFBClient(ctx, func(c *rfb.Client) error {
		return c.TypeText(text)
	})
}

func (h *windowsQEMUControlHandler) stop() *controlpb.ControlResponse {
	if err := qemuMonitorCommand(qemuMonitorPathForVMDir(h.vmDir), "quit"); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("qemu monitor unavailable; vm is not running or has exited: %v", err)}
	}
	if err := waitWindowsQEMUCTLStopped(h.vmDir, 10*time.Second); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return qemuControlMessage("stopped")
}

func (h *windowsQEMUControlHandler) requestStop() *controlpb.ControlResponse {
	if err := qemuMonitorCommand(qemuMonitorPathForVMDir(h.vmDir), "system_powerdown"); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("qemu monitor unavailable; vm is not running or has exited: %v", err)}
	}
	return qemuControlMessage("stop requested")
}

func (h *windowsQEMUControlHandler) agentPing() *controlpb.ControlResponse {
	version, err := qemuAgentPing(qemuAgentAddressForVMDir(h.vmDir), 5*time.Second)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    "agent version: " + version,
		Result:  &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: version}},
	}
}

func (h *windowsQEMUControlHandler) agentInfo() *controlpb.ControlResponse {
	client, err := qemuAgentClient(qemuAgentAddressForVMDir(h.vmDir))
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := client.Info(ctx)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("info: %v", err)}
	}
	data, err := json.Marshal(info)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("marshal agent info: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentInfo{AgentInfo: &controlpb.AgentInfoResponse{
			Hostname: info.GetHostname(),
			Os:       info.GetOsVersion(),
			Arch:     info.GetArch(),
			Version:  info.GetAgentVersion(),
			RawJson:  string(data),
		}},
	}
}

func (h *windowsQEMUControlHandler) agentStatus() *controlpb.ControlResponse {
	status := readWindowsQEMUCTLStatus(h.vmDir)
	report := windowsQEMUAgentStatus{
		Backend:           "qemu-hvf",
		AgentEndpoint:     status.AgentEndpoint,
		AgentHealth:       status.AgentHealth,
		UserAgentEndpoint: status.UserAgentEndpoint,
		UserAgentHealth:   status.UserAgentHealth,
	}
	data, err := json.Marshal(report)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("marshal qemu agent status: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: string(data)}},
	}
}

func (h *windowsQEMUControlHandler) agentExec(cmd *controlpb.AgentExecCommand, user bool) *controlpb.ControlResponse {
	if cmd == nil {
		return &controlpb.ControlResponse{Error: "missing agent-exec command payload"}
	}
	if len(cmd.Args) == 0 {
		return &controlpb.ControlResponse{Error: "args required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	started := time.Now()
	var stdout, stderr []byte
	var exitCode int32
	if user {
		client, err := qemuUserAgentClient(qemuUserAgentAddressForVMDir(h.vmDir))
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		defer client.Close()
		result, err := client.UserExec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("user exec: %v", err)}
		}
		stdout, stderr, exitCode = result.Stdout, result.Stderr, result.ExitCode
	} else {
		client, err := qemuAgentClient(qemuAgentAddressForVMDir(h.vmDir))
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		defer client.Close()
		result, err := client.Exec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("exec: %v", err)}
		}
		stdout, stderr, exitCode = result.Stdout, result.Stderr, result.ExitCode
	}
	duration := time.Since(started).Seconds()
	data, err := json.Marshal(map[string]any{
		"exitCode": exitCode,
		"stdout":   responseText(stdout),
		"stderr":   responseText(stderr),
		"duration": duration,
	})
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("marshal agent exec: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
			ExitCode:        exitCode,
			Stdout:          responseText(stdout),
			Stderr:          responseText(stderr),
			DurationSeconds: duration,
		}},
	}
}

func (h *windowsQEMUControlHandler) agentRead(cmd *controlpb.AgentFileReadCommand) *controlpb.ControlResponse {
	if cmd == nil {
		return &controlpb.ControlResponse{Error: "missing agent-read command payload"}
	}
	if cmd.Path == "" {
		return &controlpb.ControlResponse{Error: "path required"}
	}
	data, err := qemuAgentReadFile(qemuAgentAddressForVMDir(h.vmDir), cmd.Path)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("read: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(data),
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Data: data}},
	}
}

func (h *windowsQEMUControlHandler) agentWrite(cmd *controlpb.AgentFileWriteCommand) *controlpb.ControlResponse {
	if cmd == nil {
		return &controlpb.ControlResponse{Error: "missing agent-write command payload"}
	}
	if cmd.Path == "" {
		return &controlpb.ControlResponse{Error: "path required"}
	}
	data, err := base64.StdEncoding.DecodeString(cmd.Data)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("decode data: %v", err)}
	}
	mode := cmd.Mode
	if mode == 0 {
		mode = 0644
	}
	if err := qemuAgentWriteFile(qemuAgentAddressForVMDir(h.vmDir), cmd.Path, data, mode); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("write: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    "ok",
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: "ok"}},
	}
}

func (h *windowsQEMUControlHandler) agentCopy(cmd *controlpb.AgentCopyCommand) *controlpb.ControlResponse {
	if cmd == nil {
		return &controlpb.ControlResponse{Error: "missing agent-cp command payload"}
	}
	if cmd.HostPath == "" {
		return &controlpb.ControlResponse{Error: "host path required"}
	}
	if cmd.GuestPath == "" {
		return &controlpb.ControlResponse{Error: "guest path required"}
	}
	guestPath, err := normalizeWindowsQEMUCopyPath(cmd.GuestPath)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	client, err := qemuAgentClient(qemuAgentAddressForVMDir(h.vmDir))
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if cmd.ToGuest {
		mode := os.FileMode(cmd.Mode)
		if mode == 0 {
			mode = 0644
		}
		err = client.CopyToGuest(ctx, cmd.HostPath, guestPath, mode)
	} else {
		err = client.CopyFromGuest(ctx, guestPath, cmd.HostPath)
	}
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    "ok",
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: "ok"}},
	}
}

func (h *windowsQEMUControlHandler) agentShutdown(cmd *controlpb.AgentShutdownCommand, reboot bool) *controlpb.ControlResponse {
	args := []string{"shutdown.exe", "/s", "/t", "0"}
	if reboot {
		args = []string{"shutdown.exe", "/r", "/t", "0"}
	}
	if cmd != nil && cmd.Force {
		args = append(args, "/f")
	}
	resp := h.agentExec(&controlpb.AgentExecCommand{Args: args}, false)
	if !resp.Success {
		return resp
	}
	if result := resp.GetAgentExecResult(); result != nil && result.ExitCode != 0 {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("shutdown command exited with code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))}
	}
	return qemuControlMessage("stop requested")
}

func (h *windowsQEMUControlHandler) guiOpen() *controlpb.ControlResponse {
	status := readWindowsQEMUCTLStatus(h.vmDir)
	if status.VNCURL == "" {
		return &controlpb.ControlResponse{Error: "qemu vnc is not enabled; restart with -vnc :5901"}
	}
	if err := windowsQEMUOpenURL(status.VNCURL); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("open %s: %v", status.VNCURL, err)}
	}
	return qemuControlMessage("opened " + status.VNCURL)
}

func qemuControlMessage(msg string) *controlpb.ControlResponse {
	return &controlpb.ControlResponse{
		Success: true,
		Data:    msg,
		Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}},
	}
}

func qemuControlEmpty() *controlpb.ControlResponse {
	return &controlpb.ControlResponse{
		Success: true,
		Result:  &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}},
	}
}

func qemuControlUnsupported(cmd string) *controlpb.ControlResponse {
	return &controlpb.ControlResponse{Error: fmt.Sprintf("%s is not supported for qemu windows VMs", cmd)}
}
