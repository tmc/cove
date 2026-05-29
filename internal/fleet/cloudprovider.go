// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CloudProvider implements Provider against the hosted REST /v1/sandboxes API.
// It is the "provider=cloud" half of the unified surface: same Create/Get/Start/
// Stop/Delete shape as LocalProvider, but the host is hidden and an API key
// authenticates every call. The zero value is not usable; build one with
// NewCloudProvider.
type CloudProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewCloudProvider returns a provider that talks to a controller's hosted API at
// baseURL (e.g. "https://cove.example.com") authenticating with apiKey. A nil
// client gets a default with a sane timeout.
func NewCloudProvider(baseURL, apiKey string, client *http.Client) (*CloudProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("cloud provider: base url required")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &CloudProvider{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client}, nil
}

// do performs one JSON request. method/path build the URL; body (when non-nil)
// is JSON-encoded; out (when non-nil) is JSON-decoded from the response.
// wantStatus is the success code. It returns the response status for callers
// that branch on it (e.g. delete's 204).
func (p *CloudProvider) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		enc, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hosted api %s %s: status %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Create implements Provider.
func (p *CloudProvider) Create(ctx context.Context, req CreateRequest) (Sandbox, error) {
	if req.BaseRef == "" {
		return Sandbox{}, fmt.Errorf("create: base ref required")
	}
	var sb Sandbox
	err := p.do(ctx, http.MethodPost, PathSandboxes, CreateSandboxRequest{
		BaseRef:  req.BaseRef,
		Name:     req.Name,
		RAMBytes: req.RAMBytes,
		JobID:    req.JobID,
	}, &sb)
	if err != nil {
		return Sandbox{}, fmt.Errorf("create: %w", err)
	}
	return sb, nil
}

// Get implements Provider.
func (p *CloudProvider) Get(ctx context.Context, id string) (Sandbox, error) {
	var sb Sandbox
	if err := p.do(ctx, http.MethodGet, pathSandbox+id, nil, &sb); err != nil {
		return Sandbox{}, fmt.Errorf("get: %w", err)
	}
	return sb, nil
}

// Start implements Provider.
func (p *CloudProvider) Start(ctx context.Context, id string) (Sandbox, error) {
	var sb Sandbox
	if err := p.do(ctx, http.MethodPost, pathSandbox+id+"/start", nil, &sb); err != nil {
		return Sandbox{}, fmt.Errorf("start: %w", err)
	}
	return sb, nil
}

// Stop implements Provider.
func (p *CloudProvider) Stop(ctx context.Context, id string) (Sandbox, error) {
	var sb Sandbox
	if err := p.do(ctx, http.MethodPost, pathSandbox+id+"/stop", nil, &sb); err != nil {
		return Sandbox{}, fmt.Errorf("stop: %w", err)
	}
	return sb, nil
}

// Wait blocks until the sandbox is running or the timeout elapses, returning the
// running handle. A zero timeout is a single non-blocking check.
func (p *CloudProvider) Wait(ctx context.Context, id string, timeout time.Duration) (Sandbox, error) {
	var sb Sandbox
	err := p.do(ctx, http.MethodPost, pathSandbox+id+"/wait", WaitRequest{TimeoutMS: int(timeout.Milliseconds())}, &sb)
	if err != nil {
		return Sandbox{}, fmt.Errorf("wait: %w", err)
	}
	return sb, nil
}

// Delete implements Provider.
func (p *CloudProvider) Delete(ctx context.Context, id string) error {
	if err := p.do(ctx, http.MethodDelete, pathSandbox+id, nil, nil); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

var _ Provider = (*CloudProvider)(nil)
