package fleetproto

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Call POSTs req as JSON to baseURL+path with a Bearer credential and decodes
// the JSON response into a value of type Resp. It is the single transport
// helper the worker-mode client uses for register, heartbeat, and report-status.
//
// Call lives in the header-free portion of the package because the MIT
// worker-mode client (internal/coved) is its only intended caller.
func Call[Req any, Resp any](ctx context.Context, client *http.Client, baseURL, path, bearer string, req Req) (Resp, error) {
	var resp Resp
	body, err := json.Marshal(req)
	if err != nil {
		return resp, fmt.Errorf("encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return resp, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		httpReq.Header.Set(AuthHeader, "Bearer "+bearer)
	}
	if client == nil {
		client = http.DefaultClient
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return resp, fmt.Errorf("send request: %w", err)
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return resp, fmt.Errorf("read response: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return resp, fmt.Errorf("controller %s: status %d: %s", path, httpResp.StatusCode, bytes.TrimSpace(data))
	}
	if len(data) == 0 {
		return resp, nil
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}
