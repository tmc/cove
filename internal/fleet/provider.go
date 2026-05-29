package fleet

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// CreateRequest is the caller's request to create a sandbox. BaseRef is the
// image to fork from; Name is an optional friendly name; RAMBytes is the memory
// the sandbox needs (zero lets the provider pick a default); JobID groups
// replicas for the scheduler's anti-affinity term.
//
// CreateRequest is the local/cloud-neutral shape: both LocalProvider (dialing
// ~/.vz/cove.sock) and CloudProvider (hitting the hosted REST API) accept it
// unchanged, mirroring Cua's provider_type unification.
type CreateRequest struct {
	BaseRef  string `json:"base_ref"`
	Name     string `json:"name,omitempty"`
	RAMBytes int64  `json:"ram_bytes,omitempty"`
	JobID    string `json:"job_id,omitempty"`
}

// Sandbox is the opaque handle a provider returns. ID is provider-assigned and
// caller-opaque; the host is deliberately hidden in the cloud path so callers
// cannot pin to a Mac. State is one of the SandboxState constants.
type Sandbox struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Host  string `json:"host,omitempty"`
}

// Sandbox lifecycle states. They are a superset of the worker assignment states
// because a sandbox also has a pre-placement Pending state and a terminal
// Deleted state the worker protocol does not model.
const (
	SandboxPending = "pending"
	SandboxRunning = "running"
	SandboxStopped = "stopped"
	SandboxDeleted = "deleted"
	SandboxFailed  = "failed"
)

// Provider is the minimal sandbox-lifecycle surface shared by the local
// single-host path and the hosted multi-host path. The interface is the same
// shape in both deployments so an SDK can switch on a provider_type string
// without changing call sites. It is MIT: SDKs and the LocalProvider need it.
type Provider interface {
	// Create places a sandbox and returns its handle. The returned sandbox may be
	// Pending (placement queued) or Running.
	Create(ctx context.Context, req CreateRequest) (Sandbox, error)
	// Get returns the current handle for a sandbox id.
	Get(ctx context.Context, id string) (Sandbox, error)
	// Start resumes a stopped sandbox.
	Start(ctx context.Context, id string) (Sandbox, error)
	// Stop suspends a running sandbox without tearing it down.
	Stop(ctx context.Context, id string) (Sandbox, error)
	// Delete stops and tears down a sandbox.
	Delete(ctx context.Context, id string) error
}

// DefaultLocalSocket is the single-host daemon socket the LocalProvider dials.
func DefaultLocalSocket() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "cove.sock")
}

// LocalProvider implements Provider against the single-host coved daemon over
// its ~/.vz/cove.sock control socket. It is the MIT path: a caller on one Mac
// gets the same Provider surface as the hosted API without a controller.
//
// The daemon speaks newline-delimited JSON commands; LocalProvider issues one
// command per call. The zero value is usable and dials DefaultLocalSocket; set
// Socket to override and Timeout to bound each command.
type LocalProvider struct {
	// Socket is the daemon control socket path; empty dials DefaultLocalSocket.
	Socket string
	// Timeout bounds a single command round-trip; zero means 30s.
	Timeout time.Duration
}

// localCommand is the newline-JSON request envelope the daemon reads. Op selects
// the lifecycle verb; the remaining fields carry create arguments or the target
// id.
type localCommand struct {
	Op       string `json:"op"`
	ID       string `json:"id,omitempty"`
	BaseRef  string `json:"base_ref,omitempty"`
	Name     string `json:"name,omitempty"`
	RAMBytes int64  `json:"ram_bytes,omitempty"`
	JobID    string `json:"job_id,omitempty"`
}

// localResponse is the daemon's reply: either a sandbox handle or an error
// string.
type localResponse struct {
	Sandbox
	Error string `json:"error,omitempty"`
}

func (p *LocalProvider) socket() string {
	if p.Socket != "" {
		return p.Socket
	}
	return DefaultLocalSocket()
}

func (p *LocalProvider) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 30 * time.Second
}

// roundTrip sends one command and decodes the daemon's reply.
func (p *LocalProvider) roundTrip(ctx context.Context, cmd localCommand) (localResponse, error) {
	var resp localResponse
	d := net.Dialer{}
	dialCtx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	conn, err := d.DialContext(dialCtx, "unix", p.socket())
	if err != nil {
		return resp, fmt.Errorf("dial daemon: %w", err)
	}
	defer conn.Close()
	if dl, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	body, err := json.Marshal(cmd)
	if err != nil {
		return resp, fmt.Errorf("encode command: %w", err)
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return resp, fmt.Errorf("write command: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return resp, fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return resp, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("daemon: %s", resp.Error)
	}
	return resp, nil
}

// Create implements Provider.
func (p *LocalProvider) Create(ctx context.Context, req CreateRequest) (Sandbox, error) {
	if req.BaseRef == "" {
		return Sandbox{}, fmt.Errorf("create: base ref required")
	}
	resp, err := p.roundTrip(ctx, localCommand{
		Op:       "create",
		BaseRef:  req.BaseRef,
		Name:     req.Name,
		RAMBytes: req.RAMBytes,
		JobID:    req.JobID,
	})
	if err != nil {
		return Sandbox{}, fmt.Errorf("create: %w", err)
	}
	return resp.Sandbox, nil
}

// Get implements Provider.
func (p *LocalProvider) Get(ctx context.Context, id string) (Sandbox, error) {
	resp, err := p.roundTrip(ctx, localCommand{Op: "get", ID: id})
	if err != nil {
		return Sandbox{}, fmt.Errorf("get: %w", err)
	}
	return resp.Sandbox, nil
}

// Start implements Provider.
func (p *LocalProvider) Start(ctx context.Context, id string) (Sandbox, error) {
	resp, err := p.roundTrip(ctx, localCommand{Op: "start", ID: id})
	if err != nil {
		return Sandbox{}, fmt.Errorf("start: %w", err)
	}
	return resp.Sandbox, nil
}

// Stop implements Provider.
func (p *LocalProvider) Stop(ctx context.Context, id string) (Sandbox, error) {
	resp, err := p.roundTrip(ctx, localCommand{Op: "stop", ID: id})
	if err != nil {
		return Sandbox{}, fmt.Errorf("stop: %w", err)
	}
	return resp.Sandbox, nil
}

// Delete implements Provider.
func (p *LocalProvider) Delete(ctx context.Context, id string) error {
	if _, err := p.roundTrip(ctx, localCommand{Op: "delete", ID: id}); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// compile-time check that LocalProvider satisfies Provider.
var _ Provider = (*LocalProvider)(nil)
