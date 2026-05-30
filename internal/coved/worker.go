package coved

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// HostFacts is the snapshot of this worker's live state reported in every
// heartbeat. The controller materializes fleet-wide VM truth from these facts;
// the worker stays stateless about the rest of the fleet.
type HostFacts struct {
	FreeRAMBytes int64
	VMCount      int
	Images       []string
	RunningVMs   []string
}

// FactsFunc collects this host's live facts. It is injectable so tests can
// supply deterministic facts without touching the filesystem.
type FactsFunc func() HostFacts

// AssignmentHandler performs a single bounded assignment. Implementations must
// only carry out VM-lifecycle and image-sync actions; the worker refuses any
// assignment kind outside fleetproto's known set before dispatching here, so a
// handler never sees an arbitrary host-shell request.
type AssignmentHandler interface {
	Handle(ctx context.Context, a fleetproto.Assignment) (state, detail string, err error)
}

// WorkerConfig configures a Worker. Only ControllerURL, Token, and Handler are
// required; the rest have safe defaults.
type WorkerConfig struct {
	ControllerURL string
	Token         string
	HostID        string
	Hostname      string
	Arch          string
	MacOSVersion  string

	// Interval is the heartbeat period; defaults to DefaultHeartbeatInterval.
	Interval time.Duration
	// Facts collects host facts; defaults to scanning ~/.vz.
	Facts FactsFunc
	// Handler performs assignments; required.
	Handler AssignmentHandler
	// Client is the HTTP client; defaults to a client with a sane timeout.
	Client *http.Client

	// TLSClientCA is an optional path to a PEM CA bundle used to trust the
	// controller's server certificate. Empty uses the system root pool. It is
	// only consulted when Client is nil.
	TLSClientCA string
	// TLSClientCertFile and TLSClientKeyFile are an optional client certificate
	// pair presented to the controller when it requires mTLS. Both must be set
	// together. They are only consulted when Client is nil.
	TLSClientCertFile string
	TLSClientKeyFile  string
}

// DefaultHeartbeatInterval is the default heartbeat period.
const DefaultHeartbeatInterval = 10 * time.Second

// Worker is coved's dial-out fleet client. It registers with a controller,
// heartbeats this host's facts, long-polls for assignments, and reports their
// status. It is MIT (single-host worker mode), distinct from the paid
// controller. The zero value is not usable; build one with NewWorker.
type Worker struct {
	cfg     WorkerConfig
	client  *http.Client
	leaseID string
}

// NewWorker validates cfg and returns a Worker.
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.ControllerURL == "" {
		return nil, fmt.Errorf("worker: controller url required")
	}
	if cfg.Handler == nil {
		return nil, fmt.Errorf("worker: assignment handler required")
	}
	if cfg.HostID == "" {
		cfg.HostID = defaultHostID()
	}
	if cfg.Hostname == "" {
		cfg.Hostname, _ = os.Hostname()
	}
	if cfg.Arch == "" {
		cfg.Arch = runtime.GOARCH
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultHeartbeatInterval
	}
	if cfg.Facts == nil {
		cfg.Facts = ScanHostFacts
	}
	client := cfg.Client
	if client == nil {
		c, err := newWorkerClient(cfg)
		if err != nil {
			return nil, err
		}
		client = c
	}
	return &Worker{cfg: cfg, client: client}, nil
}

// newWorkerClient builds the default HTTP client. With no TLS fields set it
// returns a plain client with a sane timeout, which already dials both http://
// and https:// controllers using the system root pool. When any TLS field is
// set it carries a tls.Config trusting cfg.TLSClientCA (in addition to, or in
// place of, the system roots) and presenting the client certificate pair when
// the controller requires mTLS. It never disables verification.
func newWorkerClient(cfg WorkerConfig) (*http.Client, error) {
	tlsCfg, err := workerTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	if tlsCfg == nil {
		return &http.Client{Timeout: 30 * time.Second}, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}, nil
}

// workerTLSConfig assembles the worker's outbound tls.Config from cfg, or
// returns nil when no TLS fields are set (use the default transport). It never
// sets InsecureSkipVerify.
func workerTLSConfig(cfg WorkerConfig) (*tls.Config, error) {
	if cfg.TLSClientCA == "" && cfg.TLSClientCertFile == "" && cfg.TLSClientKeyFile == "" {
		return nil, nil
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSClientCA != "" {
		pem, err := os.ReadFile(cfg.TLSClientCA)
		if err != nil {
			return nil, fmt.Errorf("worker: read tls client ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("worker: parse tls client ca %q: no certificates found", cfg.TLSClientCA)
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.TLSClientCertFile != "" || cfg.TLSClientKeyFile != "" {
		if cfg.TLSClientCertFile == "" || cfg.TLSClientKeyFile == "" {
			return nil, fmt.Errorf("worker: tls client cert and key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(cfg.TLSClientCertFile, cfg.TLSClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("worker: load tls client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}

// LeaseID returns the lease issued by the controller after Register, or the
// empty string before registration.
func (w *Worker) LeaseID() string { return w.leaseID }

// Register performs the one-time registration handshake and stores the lease.
func (w *Worker) Register(ctx context.Context) error {
	resp, err := fleetproto.Call[fleetproto.Register, fleetproto.RegisterResp](
		ctx, w.client, w.cfg.ControllerURL, fleetproto.PathRegister, w.cfg.Token,
		fleetproto.Register{
			HostID:       w.cfg.HostID,
			Hostname:     w.cfg.Hostname,
			Arch:         w.cfg.Arch,
			MacOSVersion: w.cfg.MacOSVersion,
			Token:        w.cfg.Token,
		})
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if !resp.OK || resp.LeaseID == "" {
		return fmt.Errorf("register: controller rejected registration")
	}
	w.leaseID = resp.LeaseID
	return nil
}

// Heartbeat sends one heartbeat with current facts and dispatches any returned
// assignments, reporting each one's status. It returns the number of
// assignments handled.
func (w *Worker) Heartbeat(ctx context.Context) (int, error) {
	if w.leaseID == "" {
		return 0, fmt.Errorf("heartbeat: not registered")
	}
	facts := w.cfg.Facts()
	resp, err := fleetproto.Call[fleetproto.Heartbeat, fleetproto.HeartbeatResp](
		ctx, w.client, w.cfg.ControllerURL, fleetproto.PathHeartbeat, w.leaseID,
		fleetproto.Heartbeat{
			HostID:       w.cfg.HostID,
			LeaseID:      w.leaseID,
			FreeRAMBytes: facts.FreeRAMBytes,
			VMCount:      facts.VMCount,
			Images:       facts.Images,
			RunningVMs:   facts.RunningVMs,
		})
	if err != nil {
		return 0, fmt.Errorf("heartbeat: %w", err)
	}
	for _, a := range resp.Assignments {
		w.dispatch(ctx, a)
	}
	return len(resp.Assignments), nil
}

// dispatch enforces the security invariant — refuse any kind outside the known
// set — then runs the bounded handler and reports the result.
func (w *Worker) dispatch(ctx context.Context, a fleetproto.Assignment) {
	if !fleetproto.KnownKind(a.Kind) {
		w.report(ctx, a.ID, fleetproto.StateRefused, fmt.Sprintf("refused unknown assignment kind %q", a.Kind))
		return
	}
	state, detail, err := w.cfg.Handler.Handle(ctx, a)
	if err != nil {
		if state == "" {
			state = fleetproto.StateFailed
		}
		if detail == "" {
			detail = err.Error()
		}
	}
	if state == "" {
		state = fleetproto.StateDone
	}
	w.report(ctx, a.ID, state, detail)
}

func (w *Worker) report(ctx context.Context, assignmentID, state, detail string) {
	_, _ = fleetproto.Call[fleetproto.ReportStatus, fleetproto.StatusAck](
		ctx, w.client, w.cfg.ControllerURL, fleetproto.PathStatus, w.leaseID,
		fleetproto.ReportStatus{
			HostID:       w.cfg.HostID,
			LeaseID:      w.leaseID,
			AssignmentID: assignmentID,
			State:        state,
			Detail:       detail,
		})
}

// Run registers once, then heartbeats on the configured interval until ctx is
// cancelled. A transient heartbeat error is logged via the returned error of
// the final call; intermediate errors do not abort the loop.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.Register(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	// Heartbeat immediately so the controller sees us without waiting a tick.
	_, _ = w.Heartbeat(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.Heartbeat(ctx); err != nil {
				// A heartbeat that failed because the context was cancelled is
				// shutdown, not lease loss: return ctx.Err() rather than masking
				// it with a re-register attempt that will fail the same way.
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Re-register on lease loss; otherwise keep polling.
				if registerErr := w.Register(ctx); registerErr != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return fmt.Errorf("worker loop: %w", registerErr)
				}
			}
		}
	}
}

// ScanHostFacts collects host facts from ~/.vz: VM directory count, available
// image refs, and running VMs (those whose control socket exists).
func ScanHostFacts() HostFacts {
	home, _ := os.UserHomeDir()
	vmRoot := filepath.Join(home, ".vz", "vms")
	imageRoot := filepath.Join(home, ".vz", "images")
	return HostFacts{
		VMCount:    countDirs(vmRoot),
		RunningVMs: scanRunningVMs(vmRoot),
		Images:     scanImageRefs(imageRoot),
	}
}

func countDirs(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	var n int
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

func scanRunningVMs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var running []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "control.sock")); err == nil {
			running = append(running, e.Name())
		}
	}
	sort.Strings(running)
	return running
}

// scanImageRefs walks the image store one level past each name directory to
// list name:tag refs, matching the ~/.vz/images/<name>/<tag> layout.
func scanImageRefs(root string) []string {
	names, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var refs []string
	for _, n := range names {
		if !n.IsDir() {
			continue
		}
		tags, err := os.ReadDir(filepath.Join(root, n.Name()))
		if err != nil {
			continue
		}
		for _, t := range tags {
			if t.IsDir() {
				refs = append(refs, n.Name()+":"+t.Name())
			}
		}
	}
	sort.Strings(refs)
	return refs
}

func defaultHostID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return fmt.Sprintf("host-%d", os.Getpid())
}

// BoundedHandler is the default AssignmentHandler. It accepts only the
// VM-lifecycle, image-sync, policy, and image-gc kinds and treats every other
// kind as a hard refusal, enforcing the security invariant that worker mode
// never runs arbitrary host-shell.
//
// Policy and image-gc are handled in-process by reusing the daemon's existing
// machinery (internal/vmpolicy + LifecycleEnforcer for policy, ImageGCScheduler
// for image-gc); the fork/stop/image-sync kinds still delegate to injectable
// hooks.
type BoundedHandler struct {
	// ForkRun, StopVM, and ImageSync perform the corresponding action when set.
	// A nil hook ack's the assignment as done without side effects so the
	// controller can observe acceptance before the integration lands.
	ForkRun   func(ctx context.Context, payload []byte) (detail string, err error)
	StopVM    func(ctx context.Context, payload []byte) (detail string, err error)
	ImageSync func(ctx context.Context, payload []byte) (detail string, err error)

	// VMRoot is the directory of local VM dirs policy is written to; empty
	// defaults to ~/.vz/vms. HomeDir is the image-gc root; empty defaults to the
	// user home. Logger is optional.
	VMRoot  string
	HomeDir string
	Logger  *slog.Logger

	// Lifecycle and ImageGC override the default enforcer/scheduler so callers
	// (and tests) can share an instance with the running daemon. Nil builds a
	// default over VMRoot/HomeDir per assignment.
	Lifecycle *LifecycleEnforcer
	ImageGC   *ImageGCScheduler
}

// Handle dispatches an assignment to the bounded action for its kind. It never
// executes arbitrary host commands: only the known kinds are reachable. Policy
// applies the pushed lifecycle thresholds to local VMs and runs one enforcement
// pass; image-gc runs the scheduler one-shot. Both report structured counts.
func (h *BoundedHandler) Handle(ctx context.Context, a fleetproto.Assignment) (state, detail string, err error) {
	switch a.Kind {
	case fleetproto.KindForkRun:
		return h.run(ctx, h.ForkRun, a.Payload)
	case fleetproto.KindStopVM:
		return h.run(ctx, h.StopVM, a.Payload)
	case fleetproto.KindImageSync:
		return h.run(ctx, h.ImageSync, a.Payload)
	case fleetproto.KindPolicy:
		detail, err = h.applyPolicy(ctx, a.Payload)
		if err != nil {
			return fleetproto.StateFailed, detail, err
		}
		return fleetproto.StateDone, detail, nil
	case fleetproto.KindImageGC:
		detail, err = h.runImageGC(ctx)
		if err != nil {
			return fleetproto.StateFailed, detail, err
		}
		return fleetproto.StateDone, detail, nil
	default:
		return fleetproto.StateRefused, fmt.Sprintf("kind %q not permitted in worker mode", a.Kind), fmt.Errorf("refused kind %q", a.Kind)
	}
}

func (h *BoundedHandler) run(ctx context.Context, hook func(context.Context, []byte) (string, error), payload []byte) (state, detail string, err error) {
	if hook == nil {
		return fleetproto.StateDone, "", nil
	}
	detail, err = hook(ctx, payload)
	if err != nil {
		return fleetproto.StateFailed, detail, err
	}
	return fleetproto.StateDone, detail, nil
}
