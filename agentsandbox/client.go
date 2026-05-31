package agentsandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/controlclient"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

const (
	ProviderLocal = "local"
	ProviderCloud = "cloud"
)

type ClientOptions struct {
	Provider  string
	VM        string
	Socket    string
	CoveBin   string
	FleetURL  string
	APIKey    string
	Namespace string
	SandboxID string
	ImageRef  string
	VMName    string
	Timeout   time.Duration
	HTTP      *http.Client
}

type Client struct {
	provider  string
	vm        string
	coveBin   string
	local     *controlclient.Client
	fleetURL  string
	apiKey    string
	namespace string
	sandboxID string
	vmName    string
	timeout   time.Duration
	http      *http.Client
}

type SandboxStatus struct {
	Namespace string `json:"namespace,omitempty"`
	ID        string `json:"id"`
	VMName    string `json:"vm_name,omitempty"`
	Status    string `json:"status,omitempty"`
	WorkerID  string `json:"worker_id,omitempty"`
}

type ExecRequest struct {
	Command []string
	Env     map[string]string
	WorkDir string
	Timeout time.Duration
}

type ExecResult struct {
	ExitCode        int
	Stdout          string
	Stderr          string
	DurationSeconds float64
}

func (r ExecResult) Check() error {
	if r.ExitCode != 0 {
		return fmt.Errorf("guest command exited %d: %s", r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	return nil
}

type ScreenshotOptions struct {
	Scale   float64
	Format  string
	Quality int
	Timeout time.Duration
}

type KeyEvent struct {
	KeyCode    int
	Down       *bool
	Modifiers  uint
	UseCGEvent bool
}

type MouseEvent struct {
	X        float64
	Y        float64
	Button   int
	Action   string
	Absolute bool
}

func NewClient(opts ClientOptions) (*Client, error) {
	provider := normalizeProvider(opts)
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	coveBin := strings.TrimSpace(opts.CoveBin)
	if coveBin == "" {
		coveBin = "cove"
	}
	switch provider {
	case ProviderLocal:
		socket := strings.TrimSpace(opts.Socket)
		vm := strings.TrimSpace(opts.VM)
		if socket == "" {
			if vm == "" {
				return nil, errors.New("agentsandbox: vm or socket required")
			}
			var err error
			socket, err = vmSocketPath(vm)
			if err != nil {
				return nil, err
			}
		}
		local := controlclient.New(socket)
		local.SetTimeout(timeout)
		return &Client{
			provider: provider,
			vm:       vm,
			coveBin:  coveBin,
			local:    local,
			timeout:  timeout,
			http:     httpClient(opts.HTTP),
		}, nil
	case ProviderCloud:
		fleetURL := strings.TrimRight(strings.TrimSpace(firstNonEmpty(opts.FleetURL, os.Getenv("COVE_FLEET_URL"))), "/")
		if fleetURL == "" {
			return nil, errors.New("agentsandbox: fleet url required")
		}
		parsed, err := url.Parse(fleetURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("agentsandbox: invalid fleet url %q", fleetURL)
		}
		sandboxID := strings.TrimSpace(opts.SandboxID)
		if sandboxID == "" {
			return nil, errors.New("agentsandbox: sandbox id required")
		}
		return &Client{
			provider:  provider,
			coveBin:   coveBin,
			fleetURL:  fleetURL,
			apiKey:    strings.TrimSpace(firstNonEmpty(opts.APIKey, os.Getenv("COVE_API_KEY"), os.Getenv("COVE_FLEET_TOKEN"))),
			namespace: strings.TrimSpace(opts.Namespace),
			sandboxID: sandboxID,
			vmName:    strings.TrimSpace(opts.VMName),
			timeout:   timeout,
			http:      httpClient(opts.HTTP),
		}, nil
	default:
		return nil, fmt.Errorf("agentsandbox: unsupported provider %q", provider)
	}
}

func Create(ctx context.Context, opts ClientOptions) (*Client, error) {
	provider := normalizeProvider(opts)
	if provider != ProviderCloud {
		return nil, errors.New("agentsandbox: create is only supported for cloud sandboxes")
	}
	imageRef := strings.TrimSpace(opts.ImageRef)
	if imageRef == "" {
		return nil, errors.New("agentsandbox: image ref required")
	}
	seed := opts
	seed.SandboxID = "pending"
	c, err := NewClient(seed)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"image_ref": imageRef}
	if id := strings.TrimSpace(opts.SandboxID); id != "" {
		body["id"] = id
	}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if vmName := strings.TrimSpace(opts.VMName); vmName != "" {
		body["vm_name"] = vmName
	}
	var status SandboxStatus
	if err := c.request(ctx, http.MethodPost, "/v1/sandboxes", body, &status, c.timeout); err != nil {
		return nil, err
	}
	if strings.TrimSpace(status.ID) == "" {
		return nil, errors.New("agentsandbox: sandbox create returned no id")
	}
	c.sandboxID = status.ID
	c.namespace = status.Namespace
	c.vmName = status.VMName
	return c, nil
}

func (c *Client) Provider() string {
	if c == nil {
		return ""
	}
	return c.provider
}

func (c *Client) ID() string {
	if c == nil {
		return ""
	}
	return c.sandboxID
}

func (c *Client) VMName() string {
	if c == nil {
		return ""
	}
	if c.vmName != "" {
		return c.vmName
	}
	return c.vm
}

func (c *Client) Status(ctx context.Context) (SandboxStatus, error) {
	if err := c.ready(); err != nil {
		return SandboxStatus{}, err
	}
	if c.provider == ProviderCloud {
		var status SandboxStatus
		if err := c.request(ctx, http.MethodGet, c.sandboxPath(""), nil, &status, c.timeout); err != nil {
			return SandboxStatus{}, err
		}
		c.vmName = status.VMName
		c.namespace = status.Namespace
		return status, nil
	}
	req := &controlpb.ControlRequest{Type: "agent-ping"}
	if _, err := c.localResponse(ctx, req, c.timeout); err != nil {
		return SandboxStatus{}, err
	}
	return SandboxStatus{ID: c.vm, VMName: c.vm, Status: "ready"}, nil
}

func (c *Client) WaitReady(ctx context.Context, timeout time.Duration) error {
	if err := c.ready(); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status, err := c.Status(ctx)
		if err == nil && status.Status == "ready" {
			return nil
		}
		if err == nil && terminalSandboxStatus(status.Status) {
			return fmt.Errorf("agentsandbox: sandbox %s is %s", status.ID, status.Status)
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("agentsandbox: wait ready: %w", err)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) Start(ctx context.Context) error {
	return c.sandboxAction(ctx, "start")
}

func (c *Client) Stop(ctx context.Context) error {
	return c.sandboxAction(ctx, "stop")
}

func (c *Client) Restart(ctx context.Context) error {
	return c.sandboxAction(ctx, "restart")
}

func (c *Client) Delete(ctx context.Context) error {
	if err := c.ready(); err != nil {
		return err
	}
	if c.provider == ProviderCloud {
		return c.request(ctx, http.MethodDelete, c.sandboxPath(""), nil, nil, maxDuration(c.timeout, 2*time.Minute))
	}
	if c.vm == "" {
		return errors.New("agentsandbox: local delete requires vm name")
	}
	return runCommand(ctx, c.coveBin, "vm", "delete", c.vm)
}

func (c *Client) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if err := c.ready(); err != nil {
		return ExecResult{}, err
	}
	command := cloneStrings(req.Command)
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return ExecResult{}, errors.New("agentsandbox: exec command required")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if c.provider == ProviderLocal {
		controlReq := &controlpb.ControlRequest{
			Type: "agent-exec",
			Command: &controlpb.ControlRequest_AgentExec{
				AgentExec: &controlpb.AgentExecCommand{
					Args:       command,
					Env:        cloneStringMap(req.Env),
					WorkingDir: req.WorkDir,
				},
			},
		}
		resp, err := c.localResponse(ctx, controlReq, timeout)
		if err != nil {
			return ExecResult{}, err
		}
		result := resp.GetAgentExecResult()
		if result == nil && resp.Data != "" {
			result = new(controlpb.AgentExecResponse)
			if err := json.Unmarshal([]byte(resp.Data), result); err != nil {
				return ExecResult{}, fmt.Errorf("agentsandbox: parse exec: %w", err)
			}
		}
		if result == nil {
			return ExecResult{}, errors.New("agentsandbox: exec returned no result")
		}
		return ExecResult{
			ExitCode:        int(result.GetExitCode()),
			Stdout:          result.GetStdout(),
			Stderr:          result.GetStderr(),
			DurationSeconds: result.GetDurationSeconds(),
		}, nil
	}
	if req.WorkDir != "" {
		command = shellInDir(req.WorkDir, command)
	}
	var result struct {
		Done     bool   `json:"done"`
		ExitCode int    `json:"exit_code,omitempty"`
		Stdout   string `json:"stdout,omitempty"`
		Stderr   string `json:"stderr,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	body := map[string]any{
		"command": command,
		"env":     cloneStringMap(req.Env),
		"timeout": formatSeconds(timeout),
	}
	if err := c.request(ctx, http.MethodPost, c.sandboxPath("exec"), body, &result, timeout+minDuration(timeout, 30*time.Second)); err != nil {
		return ExecResult{}, err
	}
	if !result.Done {
		return ExecResult{}, fmt.Errorf("agentsandbox: sandbox exec timed out after %s", formatSeconds(timeout))
	}
	if result.Error != "" {
		return ExecResult{}, errors.New(result.Error)
	}
	return ExecResult{ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr}, nil
}

func (c *Client) Shell(ctx context.Context, command string) (ExecResult, error) {
	return c.Exec(ctx, ExecRequest{Command: []string{"/bin/zsh", "-lc", command}})
}

func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("agentsandbox: read path required")
	}
	if c.provider == ProviderLocal {
		req := &controlpb.ControlRequest{
			Type: "agent-read",
			Command: &controlpb.ControlRequest_AgentRead{
				AgentRead: &controlpb.AgentFileReadCommand{Path: path},
			},
		}
		resp, err := c.localResponse(ctx, req, c.timeout)
		if err != nil {
			return nil, err
		}
		if file := resp.GetAgentFile(); file != nil {
			return file.GetData(), nil
		}
		data, err := base64.StdEncoding.DecodeString(resp.Data)
		if err != nil {
			return nil, fmt.Errorf("agentsandbox: decode read: %w", err)
		}
		return data, nil
	}
	result, err := c.Exec(ctx, ExecRequest{Command: []string{"/bin/sh", "-c", "/usr/bin/base64 < " + shellQuote(path)}})
	if err != nil {
		return nil, err
	}
	if err := result.Check(); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("agentsandbox: decode read: %w", err)
	}
	return data, nil
}

func (c *Client) WriteFile(ctx context.Context, path string, data []byte, mode int) error {
	if err := c.ready(); err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("agentsandbox: write path required")
	}
	if mode == 0 {
		mode = 0644
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	if c.provider == ProviderLocal {
		req := &controlpb.ControlRequest{
			Type: "agent-write",
			Command: &controlpb.ControlRequest_AgentWrite{
				AgentWrite: &controlpb.AgentFileWriteCommand{Path: path, Data: encoded, Mode: uint32(mode)},
			},
		}
		_, err := c.localResponse(ctx, req, c.timeout)
		return err
	}
	script := "if /usr/bin/base64 -D </dev/null >/dev/null 2>&1; then cove_base64_decode='/usr/bin/base64 -D'; else cove_base64_decode='/usr/bin/base64 -d'; fi\n" +
		"$cove_base64_decode > " + shellQuote(path) + " <<'COVE_EOF'\n" +
		encoded + "\nCOVE_EOF\nchmod " + fmt.Sprintf("%o", mode) + " " + shellQuote(path) + "\n"
	result, err := c.Exec(ctx, ExecRequest{Command: []string{"/bin/sh", "-c", script}})
	if err != nil {
		return err
	}
	return result.Check()
}

func (c *Client) Screenshot(ctx context.Context, opts ScreenshotOptions) ([]byte, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if opts.Scale == 0 {
		opts.Scale = 1
	}
	if opts.Format == "" {
		opts.Format = "png"
	}
	if opts.Quality == 0 {
		opts.Quality = 90
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = maxDuration(c.timeout, 30*time.Second)
	}
	if c.provider == ProviderLocal {
		req := &controlpb.ControlRequest{
			Type: "screenshot",
			Command: &controlpb.ControlRequest_Screenshot{
				Screenshot: &controlpb.ScreenshotCommand{
					Scale:   opts.Scale,
					Quality: int32(opts.Quality),
					Format:  opts.Format,
				},
			},
		}
		resp, err := c.localResponse(ctx, req, timeout)
		if err != nil {
			return nil, err
		}
		if screenshot := resp.GetScreenshotResult(); screenshot != nil {
			return screenshot.GetImageData(), nil
		}
		data, err := base64.StdEncoding.DecodeString(resp.Data)
		if err != nil {
			return nil, fmt.Errorf("agentsandbox: decode screenshot: %w", err)
		}
		return data, nil
	}
	result, err := c.control(ctx, map[string]any{
		"type": "screenshot",
		"screenshot": map[string]any{
			"scale":   opts.Scale,
			"quality": opts.Quality,
			"format":  opts.Format,
		},
	}, timeout)
	if err != nil {
		return nil, err
	}
	data := result.Data
	if data == "" {
		if screenshot, ok := result.Response["screenshot_result"].(map[string]any); ok {
			if imageData, ok := screenshot["image_data"].(string); ok {
				data = imageData
			}
		}
	}
	if data == "" {
		return nil, errors.New("agentsandbox: screenshot returned no image data")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("agentsandbox: decode screenshot: %w", err)
	}
	return decoded, nil
}

func (c *Client) Key(ctx context.Context, event KeyEvent) error {
	if err := c.ready(); err != nil {
		return err
	}
	if event.KeyCode < 0 {
		return errors.New("agentsandbox: key code must be non-negative")
	}
	useCGEvent := event.UseCGEvent || event.Modifiers != 0
	if event.Down == nil {
		down := true
		if err := c.Key(ctx, KeyEvent{KeyCode: event.KeyCode, Down: &down, Modifiers: event.Modifiers, UseCGEvent: useCGEvent}); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		down = false
		return c.Key(ctx, KeyEvent{KeyCode: event.KeyCode, Down: &down, Modifiers: event.Modifiers, UseCGEvent: useCGEvent})
	}
	if c.provider == ProviderLocal {
		req := &controlpb.ControlRequest{
			Type: "key",
			Command: &controlpb.ControlRequest_Key{
				Key: &controlpb.KeyCommand{
					KeyCode:    uint32(event.KeyCode),
					KeyDown:    *event.Down,
					Modifiers:  uint32(event.Modifiers),
					UseCgEvent: useCGEvent,
				},
			},
		}
		_, err := c.localResponse(ctx, req, c.timeout)
		return err
	}
	_, err := c.control(ctx, map[string]any{
		"type": "key",
		"key": map[string]any{
			"key_code":     event.KeyCode,
			"key_down":     *event.Down,
			"modifiers":    event.Modifiers,
			"use_cg_event": useCGEvent,
		},
	}, c.timeout)
	return err
}

func (c *Client) Text(ctx context.Context, text string) error {
	if err := c.ready(); err != nil {
		return err
	}
	timeout := maxDuration(c.timeout, time.Duration(10+len(text)/10)*time.Second)
	if c.provider == ProviderLocal {
		req := &controlpb.ControlRequest{
			Type: "text",
			Command: &controlpb.ControlRequest_Text{
				Text: &controlpb.TextCommand{Text: text},
			},
		}
		_, err := c.localResponse(ctx, req, timeout)
		return err
	}
	_, err := c.control(ctx, map[string]any{"type": "text", "text": map[string]any{"text": text}}, timeout)
	return err
}

func (c *Client) Mouse(ctx context.Context, event MouseEvent) error {
	if err := c.ready(); err != nil {
		return err
	}
	action := strings.TrimSpace(event.Action)
	if action == "" {
		action = "click"
	}
	if c.provider == ProviderLocal {
		req := &controlpb.ControlRequest{
			Type: "mouse",
			Command: &controlpb.ControlRequest_Mouse{
				Mouse: &controlpb.MouseCommand{
					X:        event.X,
					Y:        event.Y,
					Button:   int32(event.Button),
					Action:   action,
					Absolute: event.Absolute,
				},
			},
		}
		_, err := c.localResponse(ctx, req, c.timeout)
		return err
	}
	_, err := c.control(ctx, map[string]any{
		"type": "mouse",
		"mouse": map[string]any{
			"x":        event.X,
			"y":        event.Y,
			"button":   event.Button,
			"action":   action,
			"absolute": event.Absolute,
		},
	}, c.timeout)
	return err
}

func (c *Client) Click(ctx context.Context, x, y float64) error {
	return c.Mouse(ctx, MouseEvent{X: x, Y: y, Action: "click", Absolute: true})
}

func (c *Client) sandboxAction(ctx context.Context, action string) error {
	if err := c.ready(); err != nil {
		return err
	}
	if c.provider != ProviderCloud {
		return fmt.Errorf("agentsandbox: %s is only supported for cloud sandboxes", action)
	}
	var status SandboxStatus
	return c.request(ctx, http.MethodPost, c.sandboxPath(action), map[string]any{}, &status, c.timeout)
}

type controlResult struct {
	Done     bool           `json:"done"`
	Data     string         `json:"data,omitempty"`
	Response map[string]any `json:"response,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func (c *Client) control(ctx context.Context, payload map[string]any, timeout time.Duration) (controlResult, error) {
	if timeout <= 0 {
		timeout = c.timeout
	}
	body := cloneAnyMap(payload)
	body["timeout"] = formatSeconds(timeout)
	var result controlResult
	if err := c.request(ctx, http.MethodPost, c.sandboxPath("control"), body, &result, timeout+minDuration(timeout, 30*time.Second)); err != nil {
		return controlResult{}, err
	}
	if !result.Done {
		return controlResult{}, fmt.Errorf("agentsandbox: sandbox control timed out after %s", formatSeconds(timeout))
	}
	if result.Error != "" {
		return controlResult{}, errors.New(result.Error)
	}
	if result.Response != nil {
		if success, ok := result.Response["success"].(bool); ok && !success {
			if msg, ok := result.Response["error"].(string); ok && msg != "" {
				return controlResult{}, errors.New(msg)
			}
			return controlResult{}, errors.New("agentsandbox: control request failed")
		}
	}
	return result, nil
}

func (c *Client) localResponse(ctx context.Context, req *controlpb.ControlRequest, timeout time.Duration) (*controlpb.ControlResponse, error) {
	if c.local == nil {
		return nil, errors.New("agentsandbox: local client not configured")
	}
	ctx, cancel := contextWithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.local.SendRequestCtx(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("agentsandbox: control %s: %s", req.Type, resp.Error)
	}
	return resp, nil
}

func (c *Client) request(ctx context.Context, method, path string, in, out any, timeout time.Duration) error {
	if c.provider != ProviderCloud {
		return errors.New("agentsandbox: cloud client not configured")
	}
	ctx, cancel := contextWithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("agentsandbox: encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.fleetURL+path, body)
	if err != nil {
		return fmt.Errorf("agentsandbox: create request: %w", err)
	}
	if in != nil {
		req.Header.Set("content-type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agentsandbox: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(method, path, resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("agentsandbox: decode response: %w", err)
	}
	return nil
}

func (c *Client) sandboxPath(action string) string {
	path := "/v1/sandboxes/" + url.PathEscape(c.sandboxID)
	if action != "" {
		path += "/" + url.PathEscape(action)
	}
	return path
}

func (c *Client) ready() error {
	if c == nil {
		return errors.New("agentsandbox: nil client")
	}
	if c.provider == "" {
		return errors.New("agentsandbox: provider required")
	}
	return nil
}

func normalizeProvider(opts ClientOptions) string {
	provider := strings.ToLower(strings.TrimSpace(opts.Provider))
	if provider != "" {
		return provider
	}
	if strings.TrimSpace(opts.FleetURL) != "" || strings.TrimSpace(os.Getenv("COVE_FLEET_URL")) != "" {
		return ProviderCloud
	}
	return ProviderLocal
}

func contextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func terminalSandboxStatus(status string) bool {
	switch status {
	case "canceled", "complete", "failed", "stopped":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func formatSeconds(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return d.String()
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func shellInDir(dir string, command []string) []string {
	parts := make([]string, 0, len(command))
	for _, part := range command {
		parts = append(parts, shellQuote(part))
	}
	return []string{"/bin/zsh", "-lc", "cd " + shellQuote(dir) + " && exec " + strings.Join(parts, " ")}
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func responseError(method, path string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(data))
	var errBody struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &errBody) == nil && strings.TrimSpace(errBody.Error) != "" {
		msg = strings.TrimSpace(errBody.Error)
	}
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("agentsandbox: %s %s: %s", method, path, msg)
}

func vmSocketPath(vm string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agentsandbox: user home: %w", err)
	}
	return filepath.Join(home, ".vz", "vms", vm, "control.sock"), nil
}

func runCommand(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("agentsandbox: %s %s: %w", bin, strings.Join(args, " "), err)
	}
	return nil
}
