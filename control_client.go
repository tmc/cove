// control_client.go - Programmatic client for VM control socket
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
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

// SetAuthToken overrides the token used for control socket requests.
func (c *ControlClient) SetAuthToken(token string) {
	c.authToken = strings.TrimSpace(token)
}

// sendRequest sends a proto request and returns the proto response
func (c *ControlClient) sendRequest(req *controlpb.ControlRequest) (*controlpb.ControlResponse, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(c.timeout))

	// Marshal and send request
	reqToSend := req
	if req.AuthToken == "" && c.authToken != "" {
		reqCopy := *req
		reqCopy.AuthToken = c.authToken
		reqToSend = &reqCopy
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
	return c.sendKeyEvent(keyCode, true, 0)
}

// KeyUp sends a key up event
func (c *ControlClient) KeyUp(keyCode uint16) error {
	return c.sendKeyEvent(keyCode, false, 0)
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
	if err := c.sendKeyEvent(keyCode, true, modifiers); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.sendKeyEvent(keyCode, false, modifiers)
}

// sendKeyEvent sends a keyboard event
func (c *ControlClient) sendKeyEvent(keyCode uint16, keyDown bool, modifiers uint) error {
	req := &controlpb.ControlRequest{
		Type: "key",
		Command: &controlpb.ControlRequest_Key{
			Key: &controlpb.KeyCommand{
				KeyCode:   uint32(keyCode),
				KeyDown:   keyDown,
				Modifiers: uint32(modifiers),
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
	return c.sendMouseEvent(x, y, "click", 0, false)
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
