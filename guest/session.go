// Package guest provides read-only accessibility sessions for running guests.
package guest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tmc/cove/internal/controlclient"
	pb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

// Session uses the authenticated control socket of an already running VM.
// The zero value is not usable. Close cancels client requests, never the VM.
type Session struct {
	client *controlclient.Client
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
}

// NewSession opens a logical user session without starting or changing a VM.
func NewSession(socketPath string) (*Session, error) {
	if socketPath == "" {
		return nil, errors.New("control socket path is empty")
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := controlclient.New(socketPath)
	client.SetResponseLimit(4 << 20)
	return &Session{client: client, ctx: ctx, cancel: cancel}, nil
}

// Close releases client ownership and cancels outstanding client requests.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	return nil
}

// Status separates transport, user-session and Accessibility permission states.
type Status struct {
	ProtocolVersion uint32   `json:"protocol_version"`
	Backend         string   `json:"backend"`
	SessionID       string   `json:"session_id"`
	Generation      string   `json:"generation"`
	State           string   `json:"state"`
	PermissionState string   `json:"permission_state"`
	SessionState    string   `json:"session_state"`
	Operations      []string `json:"operations,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

// Query bounds a read to one guest application PID. Selectors match exactly.
type Query struct {
	PID                int32
	MaxDepth           uint32
	MaxNodes           uint32
	MaxBytes           uint32
	Timeout            time.Duration
	Role               string
	Identifier         string
	Label              string
	ExpectedGeneration string
}

// Bounds uses guest screen coordinates.
type Bounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Node is an observation, not a persistent element or actionable native pointer.
type Node struct {
	Handle       string  `json:"handle"`
	ParentHandle string  `json:"parent_handle,omitempty"`
	Role         string  `json:"role"`
	Subrole      string  `json:"subrole,omitempty"`
	Identifier   string  `json:"identifier,omitempty"`
	Label        string  `json:"label,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
	Focused      *bool   `json:"focused,omitempty"`
	Bounds       *Bounds `json:"bounds,omitempty"`
	ValueOmitted bool    `json:"value_omitted"`
}

// Observation includes explicit truncation and match ambiguity.
type Observation struct {
	Status           Status `json:"status"`
	ID               string `json:"observation_id"`
	ObservedAt       string `json:"observed_at"`
	Nodes            []Node `json:"nodes,omitempty"`
	Truncated        bool   `json:"truncated"`
	TruncationReason string `json:"truncation_reason,omitempty"`
	MatchState       string `json:"match_state,omitempty"`
}

// Ready reads readiness; permission denial is returned as data, without prompting.
func (s *Session) Ready(ctx context.Context) (Status, error) {
	observation, err := s.request(ctx, "agent-ui-status", Query{})
	return observation.Status, err
}

// Inspect reads a bounded application tree, omitting values and sensitive text.
func (s *Session) Inspect(ctx context.Context, query Query) (Observation, error) {
	return s.request(ctx, "agent-ui-inspect", query)
}

// Find returns all exact selector matches; it never chooses or acts on one.
func (s *Session) Find(ctx context.Context, query Query) (Observation, error) {
	return s.request(ctx, "agent-ui-find", query)
}

func (s *Session) request(ctx context.Context, kind string, query Query) (Observation, error) {
	if s == nil || s.client == nil {
		return Observation{}, errors.New("guest session is not initialized")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Observation{}, errors.New("guest session is closed")
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	timeout := query.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	if timeout < 100*time.Millisecond || timeout > 10*time.Second {
		return Observation{}, errors.New("ui timeout must be between 100ms and 10s")
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
		if timeout < 100*time.Millisecond {
			return Observation{}, context.DeadlineExceeded
		}
	}
	request := &pb.UIRequest{Pid: query.PID, MaxDepth: query.MaxDepth, MaxNodes: query.MaxNodes, MaxBytes: query.MaxBytes, TimeoutMs: uint32(timeout.Milliseconds()), Role: query.Role, Identifier: query.Identifier, Label: query.Label, ExpectedGeneration: query.ExpectedGeneration}
	response, err := s.client.SendRequestCtx(ctx, &controlpb.ControlRequest{Type: kind, Command: &controlpb.ControlRequest_AgentUi{AgentUi: request}})
	if err != nil {
		return Observation{}, err
	}
	if !response.Success {
		return Observation{}, fmt.Errorf("guest ui: %s", response.Error)
	}
	result := response.GetAgentUi()
	if result == nil || result.Status == nil {
		return Observation{}, errors.New("guest ui response missing status")
	}
	status := result.Status
	observation := Observation{Status: Status{ProtocolVersion: status.ProtocolVersion, Backend: status.Backend, SessionID: status.SessionId, Generation: status.Generation, State: status.State, PermissionState: status.PermissionState, SessionState: status.SessionState, Operations: append([]string(nil), status.Operations...), Reason: status.Reason}, ID: result.ObservationId, ObservedAt: result.ObservedAt, Truncated: result.Truncated, TruncationReason: result.TruncationReason, MatchState: result.MatchState}
	for _, node := range result.Nodes {
		mapped := Node{Handle: node.Handle, ParentHandle: node.ParentHandle, Role: node.Role, Subrole: node.Subrole, Identifier: node.Identifier, Label: node.Label, Enabled: node.Enabled, Focused: node.Focused, ValueOmitted: node.ValueOmitted}
		if node.Bounds != nil {
			mapped.Bounds = &Bounds{X: node.Bounds.X, Y: node.Bounds.Y, Width: node.Bounds.Width, Height: node.Bounds.Height}
		}
		observation.Nodes = append(observation.Nodes, mapped)
	}
	return observation, nil
}
