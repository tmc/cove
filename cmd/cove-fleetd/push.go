// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tmc/cove/internal/fleet"
	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// runPush is the "cove-fleetd push" operator subcommand. It enqueues a
// fleet-wide policy or image-gc assignment against a running controller and
// prints the aggregated per-host outcome as JSON.
func runPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	controller := fs.String("controller", controllerURLDefault(), "controller base URL")
	token := fs.String("token", os.Getenv("COVE_FLEET_TOKEN"), "operator token (controller register token)")
	hostsCSV := fs.String("hosts", "", "comma-separated host ids to target (empty targets all)")
	kind := fs.String("kind", "policy", "push kind: policy or image-gc")
	idle := fs.Duration("idle-timeout", 0, "policy idle timeout (policy kind)")
	maxAge := fs.Duration("max-age", 0, "policy max age (policy kind)")
	runBudget := fs.Int("run-budget", 0, "policy run budget (policy kind)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	hosts := splitHosts(*hostsCSV)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var pushed fleet.PushResult
	switch *kind {
	case "policy":
		req := fleet.PushPolicyRequest{
			IdleTimeout: durString(*idle),
			MaxAge:      durString(*maxAge),
			RunBudget:   *runBudget,
			Hosts:       hosts,
		}
		if err := postJSON(ctx, *controller+fleet.PathPushPolicy, *token, req, &pushed); err != nil {
			return fmt.Errorf("push policy: %w", err)
		}
	case "image-gc":
		req := fleet.PushImageGCRequest{Hosts: hosts}
		if err := postJSON(ctx, *controller+fleet.PathPushImageGC, *token, req, &pushed); err != nil {
			return fmt.Errorf("push image-gc: %w", err)
		}
	default:
		return fmt.Errorf("unknown push kind %q (want policy or image-gc)", *kind)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pushed); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "enqueued %d host(s), %d failed; workers report results on their next heartbeat\n", pushed.Enqueued, pushed.Failed)
	return nil
}

func splitHosts(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	var out []string
	for _, h := range strings.Split(csv, ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func durString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func controllerURLDefault() string {
	if v := os.Getenv("COVE_FLEET_CONTROLLER_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:9878"
}

// postJSON POSTs req as JSON with a bearer token and decodes the response into
// out. It reuses no third-party deps: plain net/http + encoding/json.
func postJSON(ctx context.Context, url, token string, req, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set(fleetproto.AuthHeader, "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("controller status %d: %s", resp.StatusCode, bytes.TrimSpace(data))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
