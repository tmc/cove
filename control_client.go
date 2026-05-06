// control_client.go - Programmatic client for VM control socket
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// ControlClient provides programmatic access to the VM control socket
type ControlClient struct {
	socketPath string
	timeout    time.Duration
	authToken  string
}

// NewControlClient creates a new control client
func NewControlClient(socketPath string) *ControlClient {
	authToken := strings.TrimSpace(os.Getenv(controlTokenEnvVar))
	if authToken == "" {
		tokenPath := filepath.Join(filepath.Dir(socketPath), controlTokenFileName)
		if token, err := LoadControlTokenFromPath(tokenPath); err == nil {
			authToken = token
		}
	}
	return &ControlClient{
		socketPath: socketPath,
		timeout:    10 * time.Second,
		authToken:  authToken,
	}
}

// SetTimeout sets the command timeout
func (c *ControlClient) SetTimeout(d time.Duration) {
	c.timeout = d
}

// SetGUIInputBackend switches the runtime automation input backend.
func (c *ControlClient) SetGUIInputBackend(mode string) error {
	reqType := "gui-input-backend-" + mode
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: reqType})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// SetGUICaptureBackend switches the runtime automation screenshot backend.
func (c *ControlClient) SetGUICaptureBackend(mode string) error {
	reqType := "gui-capture-backend-" + mode
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: reqType})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// SetAuthToken overrides the token used for control socket requests.
func (c *ControlClient) SetAuthToken(token string) {
	c.authToken = strings.TrimSpace(token)
}

// sendRequest sends a proto request and returns the proto response
func (c *ControlClient) sendRequest(req *controlpb.ControlRequest) (*controlpb.ControlResponse, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return nil, formatControlSocketDialError(c.socketPath, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(c.timeout))

	// Marshal and send request
	reqToSend := req
	if req.AuthToken == "" && c.authToken != "" {
		reqToSend = proto.Clone(req).(*controlpb.ControlRequest)
		reqToSend.AuthToken = c.authToken
	}
	reqBytes, err := protojsonMarshaler.Marshal(reqToSend)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read response
	reader := bufio.NewReaderSize(conn, 256*1024)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var resp controlpb.ControlResponse
	if err := protojson.Unmarshal([]byte(respLine), &resp); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	return &resp, nil
}

// Ping tests the connection
func (c *ControlClient) Ping() error {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "ping"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("ping failed: %s", resp.Error)
	}
	return nil
}

// Screenshot captures the VM screen
func (c *ControlClient) Screenshot() (image.Image, error) {
	req := &controlpb.ControlRequest{
		Type: "screenshot",
		Command: &controlpb.ControlRequest_Screenshot{
			Screenshot: &controlpb.ScreenshotCommand{
				Scale:   1.0,
				Quality: 90,
				Format:  "png",
			},
		},
	}

	// Use longer timeout for screenshots
	oldTimeout := c.timeout
	c.timeout = 30 * time.Second
	defer func() { c.timeout = oldTimeout }()

	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("screenshot failed: %s", resp.Error)
	}

	// Decode base64
	imgData, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}

	// Decode image
	img, pngErr := png.Decode(bytes.NewReader(imgData))
	if pngErr != nil {
		// Try JPEG as fallback
		img, err = jpeg.Decode(bytes.NewReader(imgData))
		if err != nil {
			return nil, fmt.Errorf("decode image: %w", err)
		}
	}

	return img, nil
}

// ScreenshotScaled captures the VM screen with scaling
func (c *ControlClient) ScreenshotScaled(scale float64) (image.Image, error) {
	req := &controlpb.ControlRequest{
		Type: "screenshot",
		Command: &controlpb.ControlRequest_Screenshot{
			Screenshot: &controlpb.ScreenshotCommand{
				Scale:   scale,
				Quality: 80,
				Format:  "jpeg",
			},
		},
	}

	oldTimeout := c.timeout
	c.timeout = 30 * time.Second
	defer func() { c.timeout = oldTimeout }()

	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("screenshot failed: %s", resp.Error)
	}

	imgData, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}

	img, err := jpeg.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	return img, nil
}

// KeyDown sends a key down event
func (c *ControlClient) KeyDown(keyCode uint16) error {
	return c.sendKeyEvent(keyCode, true, 0, false)
}

// KeyUp sends a key up event
func (c *ControlClient) KeyUp(keyCode uint16) error {
	return c.sendKeyEvent(keyCode, false, 0, false)
}

// KeyPress sends a key down followed by key up
func (c *ControlClient) KeyPress(keyCode uint16) error {
	if err := c.KeyDown(keyCode); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.KeyUp(keyCode)
}

// KeyPressWithModifiers sends a key press with modifier keys
func (c *ControlClient) KeyPressWithModifiers(keyCode uint16, modifiers uint) error {
	// Use CGEvent for app-level shortcuts (menu commands, Cmd+Q, etc).
	if err := c.sendKeyEvent(keyCode, true, modifiers, true); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.sendKeyEvent(keyCode, false, modifiers, true)
}

// sendKeyEvent sends a keyboard event
func (c *ControlClient) sendKeyEvent(keyCode uint16, keyDown bool, modifiers uint, useCGEvent bool) error {
	req := &controlpb.ControlRequest{
		Type: "key",
		Command: &controlpb.ControlRequest_Key{
			Key: &controlpb.KeyCommand{
				KeyCode:    uint32(keyCode),
				KeyDown:    keyDown,
				Modifiers:  uint32(modifiers),
				UseCgEvent: useCGEvent,
			},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("key event failed: %s", resp.Error)
	}
	return nil
}

// TypeText types a string of text
func (c *ControlClient) TypeText(text string) error {
	req := &controlpb.ControlRequest{
		Type: "text",
		Command: &controlpb.ControlRequest_Text{
			Text: &controlpb.TextCommand{Text: text},
		},
	}

	// Typing can take a while for long strings
	oldTimeout := c.timeout
	c.timeout = time.Duration(len(text)/10+10) * time.Second
	defer func() { c.timeout = oldTimeout }()

	resp, err := c.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("type text failed: %s", resp.Error)
	}
	return nil
}

// MouseClick clicks at normalized coordinates
func (c *ControlClient) MouseClick(x, y float64) error {
	if err := c.sendMouseEvent(x, y, "move", 0, false); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	if err := c.sendMouseEvent(x, y, "down", 0, false); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.sendMouseEvent(x, y, "up", 0, false)
}

// MouseClickAbsolute clicks at absolute window pixel coordinates.
func (c *ControlClient) MouseClickAbsolute(x, y float64) error {
	if err := c.sendMouseEvent(x, y, "move", 0, true); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	if err := c.sendMouseEvent(x, y, "down", 0, true); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.sendMouseEvent(x, y, "up", 0, true)
}

// sendMouseEvent sends a mouse event
func (c *ControlClient) sendMouseEvent(x, y float64, action string, button int, absolute bool) error {
	req := &controlpb.ControlRequest{
		Type: "mouse",
		Command: &controlpb.ControlRequest_Mouse{
			Mouse: &controlpb.MouseCommand{
				X:        x,
				Y:        y,
				Action:   action,
				Button:   int32(button),
				Absolute: absolute,
			},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("mouse event failed: %s", resp.Error)
	}
	return nil
}

// WaitForConnection waits until the control socket is available
func (c *ControlClient) WaitForConnection(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := c.Ping(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for control socket: %w", lastErr)
}

// DetectScreen takes a screenshot and analyzes it to detect the current screen state
func (c *ControlClient) DetectScreen() (image.Image, ScreenState, error) {
	img, err := c.Screenshot()
	if err != nil {
		return nil, ScreenStateUnknown, fmt.Errorf("screenshot failed: %w", err)
	}
	state := DetectScreenState(img)
	return img, state, nil
}

// SendKey sends a key press event (convenience wrapper for KeyPress)
func (c *ControlClient) SendKey(keyCode uint16) error {
	return c.KeyPress(keyCode)
}

// SendMouseClick sends a mouse click at the specified coordinates
func (c *ControlClient) SendMouseClick(x, y float64) error {
	return c.MouseClick(x, y)
}

// Additional key codes not defined in control_socket_commands.go
const (
	KeyCodeS      uint16 = 1
	KeyCodeD      uint16 = 2
	KeyCodeF      uint16 = 3
	KeyCodeH      uint16 = 4
	KeyCodeG      uint16 = 5
	KeyCodeZ      uint16 = 6
	KeyCodeX      uint16 = 7
	KeyCodeC      uint16 = 8
	KeyCodeV      uint16 = 9
	KeyCodeB      uint16 = 11
	KeyCodeW      uint16 = 13
	KeyCodeE      uint16 = 14
	KeyCodeR      uint16 = 15
	KeyCodeY      uint16 = 16
	KeyCodeT      uint16 = 17
	KeyCodeN      uint16 = 45
	KeyCodeM      uint16 = 46
	KeyCodeO      uint16 = 31
	KeyCodeU      uint16 = 32
	KeyCodeI      uint16 = 34
	KeyCodeP      uint16 = 35
	KeyCodeL      uint16 = 37
	KeyCodeJ      uint16 = 38
	KeyCodeK      uint16 = 40
	KeyCodePeriod uint16 = 47 // Period/dot key
)

// Status returns typed VM status info.
func (c *ControlClient) Status() (*controlpb.StatusResponse, error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "status"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("status: %s", resp.Error)
	}
	if s := resp.GetStatus(); s != nil {
		return s, nil
	}
	var s controlpb.StatusResponse
	if err := json.Unmarshal([]byte(resp.Data), &s); err != nil {
		return nil, fmt.Errorf("parse status: %w", err)
	}
	return &s, nil
}

// Capabilities returns typed protocol capabilities.
func (c *ControlClient) Capabilities() (*controlpb.CapabilitiesResponse, error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "capabilities"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("capabilities: %s", resp.Error)
	}
	if s := resp.GetCapabilities(); s != nil {
		return s, nil
	}
	var s controlpb.CapabilitiesResponse
	if err := json.Unmarshal([]byte(resp.Data), &s); err != nil {
		return nil, fmt.Errorf("parse capabilities: %w", err)
	}
	return &s, nil
}

// ScreenshotData returns raw image bytes and format from typed response.
func (c *ControlClient) ScreenshotData() (imageData []byte, format string, err error) {
	req := &controlpb.ControlRequest{
		Type: "screenshot",
		Command: &controlpb.ControlRequest_Screenshot{
			Screenshot: &controlpb.ScreenshotCommand{
				Scale:   1.0,
				Quality: 90,
				Format:  "png",
			},
		},
	}

	oldTimeout := c.timeout
	c.timeout = 30 * time.Second
	defer func() { c.timeout = oldTimeout }()

	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, "", err
	}
	if !resp.Success {
		return nil, "", fmt.Errorf("screenshot: %s", resp.Error)
	}
	if s := resp.GetScreenshotResult(); s != nil {
		return s.ImageData, s.Format, nil
	}
	// Fallback: decode base64 from Data field.
	data, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, "", fmt.Errorf("decode screenshot: %w", err)
	}
	return data, "png", nil
}

// NetworkInfo returns typed network configuration.
func (c *ControlClient) NetworkInfo() (*controlpb.NetworkInfoResponse, error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "network"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("network: %s", resp.Error)
	}
	if s := resp.GetNetworkInfo(); s != nil {
		return s, nil
	}
	var s controlpb.NetworkInfoResponse
	if err := json.Unmarshal([]byte(resp.Data), &s); err != nil {
		return nil, fmt.Errorf("parse network: %w", err)
	}
	return &s, nil
}

// AgentExecTyped runs a command in the guest and returns typed result.
func (c *ControlClient) AgentExecTyped(args []string, env map[string]string, workDir string) (*controlpb.AgentExecResponse, error) {
	return c.AgentExecTypedTimeout(args, env, workDir, 10*time.Minute)
}

// AgentExecTypedTimeout runs a command in the guest and returns typed result.
func (c *ControlClient) AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	return c.agentExecTypedTimeout("agent-exec-auto", args, env, workDir, timeout)
}

func (c *ControlClient) AgentDaemonExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	return c.agentExecTypedTimeout("agent-exec", args, env, workDir, timeout)
}

func (c *ControlClient) AgentUserExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	return c.agentExecTypedTimeout("agent-user-exec", args, env, workDir, timeout)
}

func (c *ControlClient) agentExecTypedTimeout(reqType string, args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	req := &controlpb.ControlRequest{
		Type: reqType,
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args:       args,
				Env:        env,
				WorkingDir: workDir,
			},
		},
	}

	oldTimeout := c.timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	c.timeout = timeout
	defer func() { c.timeout = oldTimeout }()

	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("agent-exec: %s", resp.Error)
	}
	if s := resp.GetAgentExecResult(); s != nil {
		return s, nil
	}
	var s controlpb.AgentExecResponse
	if err := json.Unmarshal([]byte(resp.Data), &s); err != nil {
		return nil, fmt.Errorf("parse agent-exec: %w", err)
	}
	return &s, nil
}

// AgentReadFile reads a file from the guest and returns raw bytes.
func (c *ControlClient) AgentReadFile(path string) ([]byte, error) {
	req := &controlpb.ControlRequest{
		Type: "agent-read",
		Command: &controlpb.ControlRequest_AgentRead{
			AgentRead: &controlpb.AgentFileReadCommand{
				Path: path,
			},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("agent-read: %s", resp.Error)
	}
	if s := resp.GetAgentFile(); s != nil {
		return s.Data, nil
	}
	// Fallback: decode base64 from Data field.
	data, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("decode agent-read: %w", err)
	}
	return data, nil
}

// AgentPingTyped checks if the agent is alive and returns version.
func (c *ControlClient) AgentPingTyped() (version string, err error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "agent-ping"})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("agent-ping: %s", resp.Error)
	}
	if s := resp.GetAgentPing(); s != nil {
		return s.Version, nil
	}
	return resp.Data, nil
}

// AgentInfo returns typed guest system information.
func (c *ControlClient) AgentInfo() (*controlpb.AgentInfoResponse, error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "agent-info"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("agent-info: %s", resp.Error)
	}
	if s := resp.GetAgentInfo(); s != nil {
		return s, nil
	}
	var s controlpb.AgentInfoResponse
	if err := json.Unmarshal([]byte(resp.Data), &s); err != nil {
		return nil, fmt.Errorf("parse agent-info: %w", err)
	}
	return &s, nil
}

// SnapshotList returns typed snapshot list.
func (c *ControlClient) SnapshotList() (*controlpb.SnapshotListResponse, error) {
	req := &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{
			Snapshot: &controlpb.SnapshotCommand{Action: "list"},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("snapshot list: %s", resp.Error)
	}
	if s := resp.GetSnapshotList(); s != nil {
		return s, nil
	}
	var s controlpb.SnapshotListResponse
	if err := json.Unmarshal([]byte(resp.Data), &s); err != nil {
		return nil, fmt.Errorf("parse snapshot list: %w", err)
	}
	return &s, nil
}

// SnapshotSave saves a snapshot and returns result message.
func (c *ControlClient) SnapshotSave(name string) (string, error) {
	req := &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{
			Snapshot: &controlpb.SnapshotCommand{Action: "save", Name: name},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("snapshot save: %s", resp.Error)
	}
	if s := resp.GetSnapshotAction(); s != nil {
		return s.Message, nil
	}
	return resp.Data, nil
}

// SnapshotRestore restores a snapshot and returns result message.
func (c *ControlClient) SnapshotRestore(name string) (string, error) {
	req := &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{
			Snapshot: &controlpb.SnapshotCommand{Action: "restore", Name: name},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("snapshot restore: %s", resp.Error)
	}
	if s := resp.GetSnapshotAction(); s != nil {
		return s.Message, nil
	}
	return resp.Data, nil
}

// SnapshotDelete deletes a snapshot and returns result message.
func (c *ControlClient) SnapshotDelete(name string) (string, error) {
	req := &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{
			Snapshot: &controlpb.SnapshotCommand{Action: "delete", Name: name},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("snapshot delete: %s", resp.Error)
	}
	if s := resp.GetSnapshotAction(); s != nil {
		return s.Message, nil
	}
	return resp.Data, nil
}

// MemoryInfo returns typed memory balloon info.
func (c *ControlClient) MemoryInfo() (*controlpb.MemoryInfoResponse, error) {
	req := &controlpb.ControlRequest{
		Type: "memory",
		Command: &controlpb.ControlRequest_Memory{
			Memory: &controlpb.MemoryCommand{Action: "info"},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("memory info: %s", resp.Error)
	}
	if s := resp.GetMemoryInfo(); s != nil {
		return s, nil
	}
	var s controlpb.MemoryInfoResponse
	if err := json.Unmarshal([]byte(resp.Data), &s); err != nil {
		return nil, fmt.Errorf("parse memory info: %w", err)
	}
	return &s, nil
}

// MemorySet sets memory target and returns result message.
func (c *ControlClient) MemorySet(sizeGB float64) (string, error) {
	req := &controlpb.ControlRequest{
		Type: "memory",
		Command: &controlpb.ControlRequest_Memory{
			Memory: &controlpb.MemoryCommand{Action: "set", SizeGb: sizeGB},
		},
	}

	resp, err := c.sendRequest(req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("memory set: %s", resp.Error)
	}
	if s := resp.GetMessage(); s != nil {
		return s.Message, nil
	}
	return resp.Data, nil
}

// OCRAllText returns all recognized text from the VM display.
func (c *ControlClient) OCRAllText() (string, error) {
	req := &controlpb.ControlRequest{
		Type: "ocr",
		Command: &controlpb.ControlRequest_Ocr{
			Ocr: &controlpb.OCRCommand{Action: "all-text"},
		},
	}

	oldTimeout := c.timeout
	c.timeout = 30 * time.Second
	defer func() { c.timeout = oldTimeout }()

	resp, err := c.sendRequest(req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("ocr all-text: %s", resp.Error)
	}
	if s := resp.GetOcrText(); s != nil {
		return s.Text, nil
	}
	return resp.Data, nil
}

// OCRClickText clicks on text found via OCR.
func (c *ControlClient) OCRClickText(text string, timeout time.Duration) error {
	req := &controlpb.ControlRequest{
		Type: "ocr",
		Command: &controlpb.ControlRequest_Ocr{
			Ocr: &controlpb.OCRCommand{
				Action:  "click",
				Text:    text,
				Timeout: timeout.String(),
			},
		},
	}

	oldTimeout := c.timeout
	c.timeout = timeout + 10*time.Second
	defer func() { c.timeout = oldTimeout }()

	resp, err := c.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("ocr click-text: %s", resp.Error)
	}
	return nil
}

// SharedFoldersApply reloads shared_folders.json and live-applies it to a running VM.
func (c *ControlClient) SharedFoldersApply() (string, error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "shared-folders-apply"})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("shared-folders-apply: %s", resp.Error)
	}
	if s := resp.GetMessage(); s != nil {
		return s.Message, nil
	}
	return resp.Data, nil
}

func (c *ControlClient) SharedFoldersRuntimeStatus() (sharedFoldersRuntimeStatus, error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "shared-folders-runtime-status"})
	if err != nil {
		return sharedFoldersRuntimeStatus{}, err
	}
	if !resp.Success {
		return sharedFoldersRuntimeStatus{}, fmt.Errorf("shared-folders-runtime-status: %s", resp.Error)
	}
	var status sharedFoldersRuntimeStatus
	if resp.Data != "" {
		if err := json.Unmarshal([]byte(resp.Data), &status); err != nil {
			return sharedFoldersRuntimeStatus{}, fmt.Errorf("parse shared-folders-runtime-status: %w", err)
		}
	}
	if status.Message == "" {
		if msg := resp.GetMessage(); msg != nil {
			status.Message = msg.Message
		}
	}
	return status, nil
}

// Pause pauses the VM.
func (c *ControlClient) Pause() error {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "pause"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("pause: %s", resp.Error)
	}
	return nil
}

// Resume resumes the VM.
func (c *ControlClient) Resume() error {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "resume"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("resume: %s", resp.Error)
	}
	return nil
}

// Stop stops the VM.
func (c *ControlClient) Stop() error {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "stop"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("stop: %s", resp.Error)
	}
	return nil
}
