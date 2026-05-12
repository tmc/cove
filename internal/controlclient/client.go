// control_client.go - Programmatic client for VM control socket
package controlclient

import (
	"bufio"
	"bytes"
	"context"
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

	"google.golang.org/protobuf/proto"

	control "github.com/tmc/vz-macos/internal/control"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// Client provides programmatic access to the VM control socket.
type Client struct {
	socketPath string
	timeout    time.Duration
	authToken  string
}

// New creates a new control client.
func New(socketPath string) *Client {
	authToken := strings.TrimSpace(os.Getenv(TokenEnvVar))
	if authToken == "" {
		tokenPath := filepath.Join(filepath.Dir(socketPath), TokenFileName)
		if token, err := LoadTokenFromPath(tokenPath); err == nil {
			authToken = token
		}
	}
	return &Client{
		socketPath: socketPath,
		timeout:    10 * time.Second,
		authToken:  authToken,
	}
}

// SetTimeout sets the command timeout.
func (c *Client) SetTimeout(d time.Duration) {
	c.timeout = d
}

// SetGUIInputBackend switches the runtime automation input backend.
func (c *Client) SetGUIInputBackend(mode string) error {
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
func (c *Client) SetGUICaptureBackend(mode string) error {
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
func (c *Client) SetAuthToken(token string) {
	c.authToken = strings.TrimSpace(token)
}

// SendRequest sends a proto request and returns the proto response.
func (c *Client) SendRequest(req *controlpb.ControlRequest) (*controlpb.ControlResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	return c.SendRequestCtx(ctx, req)
}

// SendRequestCtx sends a proto request and returns the proto response.
func (c *Client) SendRequestCtx(ctx context.Context, req *controlpb.ControlRequest) (*controlpb.ControlResponse, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, FormatDialError(c.socketPath, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	// Marshal and send request
	reqToSend := req
	if req.AuthToken == "" && c.authToken != "" {
		reqToSend = proto.Clone(req).(*controlpb.ControlRequest)
		reqToSend.AuthToken = c.authToken
	}
	reqBytes, err := control.ProtoJSONMarshaler.Marshal(reqToSend)
	if err != nil {
		return nil, fmt.Errorf("control %q: marshal: %w", req.Type, err)
	}
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("control %q: write: %w", req.Type, err)
	}

	// Read response
	reader := bufio.NewReaderSize(conn, 256*1024)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if timeoutErr, ok := err.(net.Error); ok && timeoutErr.Timeout() {
			if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				return nil, context.DeadlineExceeded
			}
		}
		return nil, fmt.Errorf("control %q: read: %w", req.Type, err)
	}

	var resp controlpb.ControlResponse
	if err := control.ProtoJSONUnmarshaler.Unmarshal([]byte(respLine), &resp); err != nil {
		return nil, fmt.Errorf("control %q: parse: %w", req.Type, err)
	}

	return &resp, nil
}

func (c *Client) Timeout() time.Duration {
	return c.timeout
}

// sendRequest is kept unexported inside the package for method bodies.
func (c *Client) sendRequest(req *controlpb.ControlRequest) (*controlpb.ControlResponse, error) {
	return c.SendRequest(req)
}

// Ping tests the connection.
func (c *Client) Ping() error {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "ping"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("ping failed: %s", resp.Error)
	}
	return nil
}

// Screenshot captures the VM screen.
func (c *Client) Screenshot() (image.Image, error) {
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
func (c *Client) ScreenshotScaled(scale float64) (image.Image, error) {
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
func (c *Client) KeyDown(keyCode uint16) error {
	return c.sendKeyEvent(keyCode, true, 0, false)
}

// KeyUp sends a key up event
func (c *Client) KeyUp(keyCode uint16) error {
	return c.sendKeyEvent(keyCode, false, 0, false)
}

// KeyPress sends a key down followed by key up
func (c *Client) KeyPress(keyCode uint16) error {
	if err := c.KeyDown(keyCode); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.KeyUp(keyCode)
}

// KeyPressWithModifiers sends a key press with modifier keys
func (c *Client) KeyPressWithModifiers(keyCode uint16, modifiers uint) error {
	// Use CGEvent for app-level shortcuts (menu commands, Cmd+Q, etc).
	if err := c.sendKeyEvent(keyCode, true, modifiers, true); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.sendKeyEvent(keyCode, false, modifiers, true)
}

// SendKeyEvent sends a keyboard event.
func (c *Client) SendKeyEvent(keyCode uint16, keyDown bool, modifiers uint, useCGEvent bool) error {
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

func (c *Client) sendKeyEvent(keyCode uint16, keyDown bool, modifiers uint, useCGEvent bool) error {
	return c.SendKeyEvent(keyCode, keyDown, modifiers, useCGEvent)
}

// TypeText types a string of text
func (c *Client) TypeText(text string) error {
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
func (c *Client) MouseClick(x, y float64) error {
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
func (c *Client) MouseClickAbsolute(x, y float64) error {
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
func (c *Client) sendMouseEvent(x, y float64, action string, button int, absolute bool) error {
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
func (c *Client) WaitForConnection(timeout time.Duration) error {
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

// SendKey sends a key press event (convenience wrapper for KeyPress)
func (c *Client) SendKey(keyCode uint16) error {
	return c.KeyPress(keyCode)
}

// SendMouseClick sends a mouse click at the specified coordinates
func (c *Client) SendMouseClick(x, y float64) error {
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
func (c *Client) Status() (*controlpb.StatusResponse, error) {
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
func (c *Client) Capabilities() (*controlpb.CapabilitiesResponse, error) {
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
func (c *Client) ScreenshotData() (imageData []byte, format string, err error) {
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
func (c *Client) NetworkInfo() (*controlpb.NetworkInfoResponse, error) {
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
func (c *Client) AgentExecTyped(args []string, env map[string]string, workDir string) (*controlpb.AgentExecResponse, error) {
	return c.AgentExecTypedTimeout(args, env, workDir, 10*time.Minute)
}

// AgentExecTypedTimeout runs a command in the guest and returns typed result.
func (c *Client) AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	return c.agentExecTypedTimeout("agent-exec-auto", args, env, workDir, timeout)
}

func (c *Client) AgentDaemonExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	return c.agentExecTypedTimeout("agent-exec", args, env, workDir, timeout)
}

func (c *Client) AgentUserExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	return c.agentExecTypedTimeout("agent-user-exec", args, env, workDir, timeout)
}

func (c *Client) agentExecTypedTimeout(reqType string, args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
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
func (c *Client) AgentReadFile(path string) ([]byte, error) {
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
func (c *Client) AgentPingTyped() (version string, err error) {
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
func (c *Client) AgentInfo() (*controlpb.AgentInfoResponse, error) {
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
func (c *Client) SnapshotList() (*controlpb.SnapshotListResponse, error) {
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
func (c *Client) SnapshotSave(name string) (string, error) {
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
func (c *Client) SnapshotRestore(name string) (string, error) {
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
func (c *Client) SnapshotDelete(name string) (string, error) {
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
func (c *Client) MemoryInfo() (*controlpb.MemoryInfoResponse, error) {
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
func (c *Client) MemorySet(sizeGB float64) (string, error) {
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
func (c *Client) OCRAllText() (string, error) {
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
func (c *Client) OCRClickText(text string, timeout time.Duration) error {
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
func (c *Client) SharedFoldersApply() (string, error) {
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

func (c *Client) SharedFoldersRuntimeStatus() (SharedFoldersRuntimeStatus, error) {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "shared-folders-runtime-status"})
	if err != nil {
		return SharedFoldersRuntimeStatus{}, err
	}
	if !resp.Success {
		return SharedFoldersRuntimeStatus{}, fmt.Errorf("shared-folders-runtime-status: %s", resp.Error)
	}
	var status SharedFoldersRuntimeStatus
	if resp.Data != "" {
		if err := json.Unmarshal([]byte(resp.Data), &status); err != nil {
			return SharedFoldersRuntimeStatus{}, fmt.Errorf("parse shared-folders-runtime-status: %w", err)
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
func (c *Client) Pause() error {
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
func (c *Client) Resume() error {
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
func (c *Client) Stop() error {
	resp, err := c.sendRequest(&controlpb.ControlRequest{Type: "stop"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("stop: %s", resp.Error)
	}
	return nil
}
