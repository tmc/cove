// Package coved contains host-side daemon services.
package coved

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	controlx "github.com/tmc/cove/internal/control"
	"github.com/tmc/cove/internal/vmpolicy"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

const DefaultLifecycleInterval = 30 * time.Second

type LifecycleConfig struct {
	VMRoot   string
	Interval time.Duration
	Now      func() time.Time
	Log      *slog.Logger
	Bus      *EventBus
}

type LifecycleStats struct {
	Enforced    uint64
	StopErrors  uint64
	LastRunUnix int64
}

type LifecycleEnforcer struct {
	cfg         LifecycleConfig
	enforced    atomic.Uint64
	stopErrors  atomic.Uint64
	lastRunUnix atomic.Int64
}

func NewLifecycleEnforcer(cfg LifecycleConfig) *LifecycleEnforcer {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultLifecycleInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &LifecycleEnforcer{cfg: cfg}
}

func (e *LifecycleEnforcer) Run(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()

	e.EnforceOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.EnforceOnce(ctx)
		}
	}
}

func (e *LifecycleEnforcer) Stats() LifecycleStats {
	return LifecycleStats{
		Enforced:    e.enforced.Load(),
		StopErrors:  e.stopErrors.Load(),
		LastRunUnix: e.lastRunUnix.Load(),
	}
}

func (e *LifecycleEnforcer) EnforceOnce(ctx context.Context) {
	now := e.cfg.Now().UTC()
	e.lastRunUnix.Store(now.Unix())

	entries, err := os.ReadDir(e.cfg.VMRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			e.cfg.Log.Debug("lifecycle read vm root", slog.Any("err", err))
		}
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.IsDir() {
			continue
		}
		vmDir := filepath.Join(e.cfg.VMRoot, entry.Name())
		if err := e.enforceVM(ctx, entry.Name(), vmDir, now); err != nil {
			e.cfg.Log.Debug("lifecycle vm skipped", slog.String("vm", entry.Name()), slog.Any("err", err))
		}
	}
}

func (e *LifecycleEnforcer) enforceVM(ctx context.Context, name, vmDir string, now time.Time) error {
	policy, err := vmpolicy.Load(vmDir)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	if policy.Empty() {
		return nil
	}

	client := controlClient{socket: filepath.Join(vmDir, "control.sock")}
	status, err := client.status(ctx)
	if err != nil {
		return err
	}
	if status.State != "running" && status.State != "paused" {
		return nil
	}
	if status.PolicyStopIssued {
		return nil
	}

	reason := stopReason(policy, status, now)
	if reason == "" {
		return nil
	}
	if err := client.requestStop(ctx); err != nil {
		e.stopErrors.Add(1)
		e.publishStopError(ctx, name, reason, err, now)
		return fmt.Errorf("request stop: %w", err)
	}
	e.enforced.Add(1)
	e.publishStop(ctx, name, reason, now)
	e.cfg.Log.Info("lifecycle policy stop", slog.String("vm", name), slog.String("reason", reason))
	return nil
}

func (e *LifecycleEnforcer) publishStopError(ctx context.Context, name, reason string, err error, now time.Time) {
	if e.cfg.Bus == nil {
		return
	}
	e.cfg.Bus.Publish(ctx, Event{
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		EventType: "lifecycle.policy.stop_error",
		VMName:    name,
		Status:    "error",
		Extra: map[string]any{
			"reason":     reason,
			"stop_error": err.Error(),
		},
	})
}

func (e *LifecycleEnforcer) publishStop(ctx context.Context, name, reason string, now time.Time) {
	if e.cfg.Bus == nil {
		return
	}
	e.cfg.Bus.Publish(ctx, Event{
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		EventType: "lifecycle.policy.stop",
		VMName:    name,
		Status:    "stop_requested",
		Extra: map[string]any{
			"reason": reason,
		},
	})
}

func stopReason(policy vmpolicy.Policy, status vmStatus, now time.Time) string {
	switch {
	case policy.RunBudget > 0 && status.PolicyExecCount >= int64(policy.RunBudget):
		return "run_budget"
	case policy.MaxAge > 0 && !status.PolicyStartedAt.IsZero() && now.Sub(status.PolicyStartedAt) >= policy.MaxAge:
		return "max_age"
	case policy.IdleTimeout > 0 && status.State == "running" && !status.LastPing.IsZero() && now.Sub(status.LastPing) >= policy.IdleTimeout:
		return "idle"
	default:
		return ""
	}
}

type controlClient struct {
	socket string
}

type vmStatus struct {
	State            string
	LastPing         time.Time
	PolicyStartedAt  time.Time
	PolicyExecCount  int64
	PolicyStopIssued bool
}

func (c controlClient) status(ctx context.Context) (vmStatus, error) {
	var status vmStatus
	resp, err := c.roundTrip(ctx, "status")
	if err != nil {
		return status, err
	}
	if resp.Error != "" {
		return status, fmt.Errorf("status: %s", resp.Error)
	}
	if resp.GetStatus() != nil {
		status.State = resp.GetStatus().State
	}
	var data struct {
		State            string `json:"state"`
		LastPing         string `json:"lastPing"`
		PolicyStartedAt  string `json:"policyStartedAt"`
		PolicyExecCount  int64  `json:"policyExecCount"`
		PolicyStopIssued bool   `json:"policyStopIssued"`
	}
	if resp.Data != "" {
		if err := json.Unmarshal([]byte(resp.Data), &data); err != nil {
			return status, fmt.Errorf("decode status data: %w", err)
		}
	}
	if status.State == "" {
		status.State = data.State
	}
	status.LastPing = parseTime(data.LastPing)
	status.PolicyStartedAt = parseTime(data.PolicyStartedAt)
	status.PolicyExecCount = data.PolicyExecCount
	status.PolicyStopIssued = data.PolicyStopIssued
	return status, nil
}

func (c controlClient) requestStop(ctx context.Context) error {
	resp, err := c.roundTrip(ctx, "request-stop")
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func (c controlClient) roundTrip(ctx context.Context, typ string) (*controlpb.ControlResponse, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req := &controlpb.ControlRequest{Type: typ}
	if token, err := os.ReadFile(filepath.Join(filepath.Dir(c.socket), "control.token")); err == nil {
		req.AuthToken = string(bytesTrimSpace(token))
	}
	data, err := controlx.ProtoJSONMarshaler.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp controlpb.ControlResponse
	if err := controlx.ProtoJSONUnmarshaler.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\r' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t
	}
	t, err = time.Parse(time.RFC3339Nano, s)
	if err == nil {
		return t
	}
	return time.Time{}
}
