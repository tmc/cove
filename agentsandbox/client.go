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
	"strconv"
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
	Provider             string
	VM                   string
	Socket               string
	CoveBin              string
	FleetURL             string
	APIKey               string
	Namespace            string
	SandboxID            string
	ImageRef             string
	ManifestBundle       string
	ImageManifestDigest  string
	ImageDigestRef       string
	ImagePlatform        string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	PlacementLimit       int
	VMName               string
	Timeout              time.Duration
	HTTP                 *http.Client
}

type Client struct {
	provider    string
	vm          string
	coveBin     string
	local       *controlclient.Client
	fleetURL    string
	apiKey      string
	namespace   string
	sandboxID   string
	vmName      string
	leaseHolder string
	timeout     time.Duration
	http        *http.Client
}

type SandboxStatus struct {
	Namespace            string    `json:"namespace,omitempty"`
	ID                   string    `json:"id"`
	VMName               string    `json:"vm_name,omitempty"`
	ImageRef             string    `json:"image_ref,omitempty"`
	ImageManifestDigest  string    `json:"image_manifest_digest,omitempty"`
	ImageDigestRef       string    `json:"image_digest_ref,omitempty"`
	ImagePlatform        string    `json:"image_platform,omitempty"`
	RequiredCapabilities []string  `json:"required_capabilities,omitempty"`
	Status               string    `json:"status,omitempty"`
	WorkerID             string    `json:"worker_id,omitempty"`
	Lease                *Lease    `json:"lease,omitempty"`
	Created              time.Time `json:"created,omitempty"`
	Updated              time.Time `json:"updated,omitempty"`
}

type SandboxListOptions struct {
	Namespace string
	Status    string
	WorkerID  string
	ImageRef  string
	Offset    int
	Limit     int
}

type SandboxListResult struct {
	Sandboxes  []SandboxStatus `json:"sandboxes"`
	Count      int             `json:"count,omitempty"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
}

type Capacity struct {
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	VMs         int    `json:"vms,omitempty"`
	MaxVMs      int    `json:"max_vms,omitempty"`
	Images      int    `json:"images,omitempty"`
}

type PlacementPlan struct {
	ID                   string               `json:"id,omitempty"`
	Created              time.Time            `json:"created,omitempty"`
	Namespace            string               `json:"namespace,omitempty"`
	Policy               string               `json:"policy"`
	ImageRef             string               `json:"image_ref,omitempty"`
	ImageManifestDigest  string               `json:"image_manifest_digest,omitempty"`
	ImageDigestRef       string               `json:"image_digest_ref,omitempty"`
	ImagePlatform        string               `json:"image_platform,omitempty"`
	RequiredLabels       map[string]string    `json:"required_labels,omitempty"`
	RequiredCapabilities []string             `json:"required_capabilities,omitempty"`
	Limit                int                  `json:"limit,omitempty"`
	Candidates           []PlacementCandidate `json:"candidates,omitempty"`
	Skipped              []PlacementSkip      `json:"skipped,omitempty"`
}

type PlacementCandidate struct {
	Rank             int    `json:"rank"`
	WorkerID         string `json:"worker_id"`
	Load             int    `json:"load"`
	MaxVMs           int    `json:"max_vms,omitempty"`
	RequestedVMs     int    `json:"requested_vms"`
	AntiAffinityLoad int    `json:"anti_affinity_load,omitempty"`
	HasImage         bool   `json:"has_image,omitempty"`
}

type PlacementSkip struct {
	WorkerID            string            `json:"worker_id"`
	Reason              string            `json:"reason"`
	Status              string            `json:"status,omitempty"`
	MissingLabels       map[string]string `json:"missing_labels,omitempty"`
	MissingCapabilities []string          `json:"missing_capabilities,omitempty"`
	Load                int               `json:"load,omitempty"`
	MaxVMs              int               `json:"max_vms,omitempty"`
	RequestedVMs        int               `json:"requested_vms,omitempty"`
	ImageRef            string            `json:"image_ref,omitempty"`
	ImageManifestDigest string            `json:"image_manifest_digest,omitempty"`
}

type Assignment struct {
	ID                   string            `json:"id"`
	Namespace            string            `json:"namespace,omitempty"`
	WorkerID             string            `json:"worker_id,omitempty"`
	WarmPool             string            `json:"warm_pool,omitempty"`
	WarmPoolSlot         string            `json:"warm_pool_slot,omitempty"`
	SandboxID            string            `json:"sandbox_id,omitempty"`
	SandboxRole          string            `json:"sandbox_role,omitempty"`
	SandboxLeaseHolder   string            `json:"sandbox_lease_holder,omitempty"`
	SandboxLeaseExpires  time.Time         `json:"sandbox_lease_expires,omitempty"`
	Policy               string            `json:"policy,omitempty"`
	ImageRef             string            `json:"image_ref,omitempty"`
	ManifestBundle       string            `json:"manifest_bundle,omitempty"`
	ImageManifestDigest  string            `json:"image_manifest_digest,omitempty"`
	ImageDigestRef       string            `json:"image_digest_ref,omitempty"`
	ImagePlatform        string            `json:"image_platform,omitempty"`
	RequiredLabels       map[string]string `json:"required_labels,omitempty"`
	RequiredCapabilities []string          `json:"required_capabilities,omitempty"`
	AntiAffinityKey      string            `json:"anti_affinity_key,omitempty"`
	Resources            Capacity          `json:"resources,omitempty"`
	Verb                 string            `json:"verb"`
	Args                 []string          `json:"args,omitempty"`
	Status               string            `json:"status,omitempty"`
	Created              time.Time         `json:"created,omitempty"`
	Updated              time.Time         `json:"updated,omitempty"`
	LeasedTo             string            `json:"leased_to,omitempty"`
	LeaseExpires         time.Time         `json:"lease_expires,omitempty"`
	LastReport           *WorkerReport     `json:"last_report,omitempty"`
}

type ImagePrepareOptions struct {
	FleetURL             string
	APIKey               string
	Namespace            string
	SourceRef            string
	ImageRef             string
	ManifestBundle       string
	ImageManifestDigest  string
	ImageDigestRef       string
	ImagePlatform        string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	Force                bool
	Timeout              time.Duration
	HTTP                 *http.Client
}

type ImagePrepareListOptions struct {
	FleetURL            string
	APIKey              string
	Namespace           string
	SourceRef           string
	ImageRef            string
	ImageManifestDigest string
	Offset              int
	Limit               int
	Timeout             time.Duration
	HTTP                *http.Client
}

type ImagePrepareGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type ImagePrepareResult struct {
	ID                   string             `json:"id,omitempty"`
	Created              time.Time          `json:"created,omitempty"`
	Namespace            string             `json:"namespace,omitempty"`
	SourceRef            string             `json:"source_ref"`
	ImageRef             string             `json:"image_ref"`
	ImageManifestDigest  string             `json:"image_manifest_digest,omitempty"`
	ImageDigestRef       string             `json:"image_digest_ref,omitempty"`
	ImagePlatform        string             `json:"image_platform,omitempty"`
	RequiredLabels       map[string]string  `json:"required_labels,omitempty"`
	RequiredCapabilities []string           `json:"required_capabilities,omitempty"`
	Assignments          []Assignment       `json:"assignments,omitempty"`
	Skipped              []ImagePrepareSkip `json:"skipped,omitempty"`
}

type ImagePrepareSkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type ImagePrepareListResult struct {
	Preparations []ImagePrepareResult `json:"preparations"`
	Count        int                  `json:"count"`
	Offset       int                  `json:"offset,omitempty"`
	Limit        int                  `json:"limit,omitempty"`
	NextOffset   int                  `json:"next_offset,omitempty"`
}

type WarmPoolOptions struct {
	FleetURL             string
	APIKey               string
	Namespace            string
	Name                 string
	ImageRef             string
	ManifestBundle       string
	ImageManifestDigest  string
	ImageDigestRef       string
	ImagePlatform        string
	Size                 int
	Policy               string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	Resources            Capacity
	Args                 []string
	Timeout              time.Duration
	HTTP                 *http.Client
}

type WarmPoolListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Timeout   time.Duration
	HTTP      *http.Client
}

type WarmPoolGetOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Name      string
	Timeout   time.Duration
	HTTP      *http.Client
}

type WarmPoolClaimOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Name      string
	Command   []string
	Env       map[string]string
	Timeout   time.Duration
	HTTP      *http.Client
}

type WarmPoolEventListOptions struct {
	FleetURL     string
	APIKey       string
	Namespace    string
	Name         string
	Actor        string
	Action       string
	WorkerID     string
	AssignmentID string
	Offset       int
	Limit        int
	Timeout      time.Duration
	HTTP         *http.Client
}

type WarmPool struct {
	Namespace            string            `json:"namespace,omitempty"`
	Name                 string            `json:"name"`
	ImageRef             string            `json:"image_ref"`
	ImageManifestDigest  string            `json:"image_manifest_digest,omitempty"`
	ImageDigestRef       string            `json:"image_digest_ref,omitempty"`
	ImagePlatform        string            `json:"image_platform,omitempty"`
	Size                 int               `json:"size"`
	Policy               string            `json:"policy,omitempty"`
	RequiredLabels       map[string]string `json:"required_labels,omitempty"`
	RequiredCapabilities []string          `json:"required_capabilities,omitempty"`
	Resources            Capacity          `json:"resources,omitempty"`
	Args                 []string          `json:"args,omitempty"`
	Created              time.Time         `json:"created,omitempty"`
	Updated              time.Time         `json:"updated,omitempty"`
}

type WarmPoolStatus struct {
	WarmPool
	Slots       int            `json:"slots"`
	Active      int            `json:"active"`
	Pending     int            `json:"pending"`
	Leased      int            `json:"leased"`
	Running     int            `json:"running"`
	Ready       int            `json:"ready"`
	Claimed     int            `json:"claimed"`
	Draining    int            `json:"draining"`
	Terminal    int            `json:"terminal"`
	ByStatus    map[string]int `json:"by_status,omitempty"`
	Assignments []Assignment   `json:"assignments,omitempty"`
}

type WarmPoolResult struct {
	Pool     WarmPoolStatus `json:"pool"`
	Created  []Assignment   `json:"created,omitempty"`
	Canceled []string       `json:"canceled,omitempty"`
	Cleanup  []Assignment   `json:"cleanup,omitempty"`
}

type WarmPoolListResult struct {
	WarmPools []WarmPoolStatus `json:"warm_pools"`
}

type WarmPoolDeleteResult struct {
	Namespace string       `json:"namespace,omitempty"`
	Pool      string       `json:"pool"`
	Canceled  []string     `json:"canceled,omitempty"`
	Cleanup   []Assignment `json:"cleanup,omitempty"`
	Deferred  []string     `json:"deferred,omitempty"`
}

type WarmPoolClaimResult struct {
	Namespace  string     `json:"namespace,omitempty"`
	Pool       string     `json:"pool"`
	VMName     string     `json:"vm_name"`
	Slot       Assignment `json:"slot"`
	Assignment Assignment `json:"assignment"`
}

type Lease struct {
	Holder  string    `json:"holder"`
	Expires time.Time `json:"expires"`
}

type LeaseResult struct {
	Sandbox SandboxStatus `json:"sandbox"`
	Lease   Lease         `json:"lease"`
}

type MeteringRecord struct {
	ID               string    `json:"id"`
	Time             time.Time `json:"time"`
	Namespace        string    `json:"namespace,omitempty"`
	SandboxID        string    `json:"sandbox_id"`
	AssignmentID     string    `json:"assignment_id"`
	WorkerID         string    `json:"worker_id,omitempty"`
	Status           string    `json:"status"`
	Started          time.Time `json:"started"`
	Ended            time.Time `json:"ended"`
	DurationMillis   int64     `json:"duration_millis"`
	VMMillis         int64     `json:"vm_millis"`
	CPUMillis        int64     `json:"cpu_millis,omitempty"`
	MemoryByteMillis uint64    `json:"memory_byte_millis,omitempty"`
}

type MeteringSummary struct {
	Namespace        string `json:"namespace,omitempty"`
	SandboxID        string `json:"sandbox_id,omitempty"`
	Records          int    `json:"records"`
	DurationMillis   int64  `json:"duration_millis"`
	VMMillis         int64  `json:"vm_millis"`
	CPUMillis        int64  `json:"cpu_millis,omitempty"`
	MemoryByteMillis uint64 `json:"memory_byte_millis,omitempty"`
}

type MeteringResult struct {
	Records []MeteringRecord `json:"records"`
	Summary MeteringSummary  `json:"summary"`
}

type SandboxEvent struct {
	ID           string            `json:"id"`
	Time         time.Time         `json:"time"`
	Namespace    string            `json:"namespace,omitempty"`
	Actor        string            `json:"actor,omitempty"`
	Action       string            `json:"action"`
	TargetType   string            `json:"target_type,omitempty"`
	TargetID     string            `json:"target_id,omitempty"`
	WorkerID     string            `json:"worker_id,omitempty"`
	AssignmentID string            `json:"assignment_id,omitempty"`
	Status       string            `json:"status,omitempty"`
	Fields       map[string]string `json:"fields,omitempty"`
	PrevHash     string            `json:"prev_hash,omitempty"`
	Hash         string            `json:"hash,omitempty"`
}

type SandboxEventListOptions struct {
	Actor  string
	Action string
	Offset int
	Limit  int
}

type SandboxEventListResult struct {
	Events     []SandboxEvent `json:"events"`
	Count      int            `json:"count,omitempty"`
	Offset     int            `json:"offset,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	NextOffset int            `json:"next_offset,omitempty"`
}

type SandboxReport struct {
	Namespace    string       `json:"namespace,omitempty"`
	SandboxID    string       `json:"sandbox_id"`
	AssignmentID string       `json:"assignment_id"`
	Role         string       `json:"role,omitempty"`
	WorkerID     string       `json:"worker_id,omitempty"`
	Status       string       `json:"status,omitempty"`
	Created      time.Time    `json:"created,omitempty"`
	Updated      time.Time    `json:"updated,omitempty"`
	Report       WorkerReport `json:"report"`
}

type WorkerReport struct {
	ID           string    `json:"id"`
	AssignmentID string    `json:"assignment_id,omitempty"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	ExitCode     int       `json:"exit_code,omitempty"`
	Stdout       string    `json:"stdout,omitempty"`
	Stderr       string    `json:"stderr,omitempty"`
	Time         time.Time `json:"time,omitempty"`
}

type SandboxReportListOptions struct {
	Role   string
	Status string
	Offset int
	Limit  int
}

type SandboxReportListResult struct {
	Reports    []SandboxReport `json:"reports"`
	Count      int             `json:"count,omitempty"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
}

type WaitResult struct {
	Done    bool          `json:"done"`
	Sandbox SandboxStatus `json:"sandbox"`
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
	if bundle := strings.TrimSpace(opts.ManifestBundle); bundle != "" {
		body["manifest_bundle"] = bundle
	}
	if digest := strings.TrimSpace(opts.ImageManifestDigest); digest != "" {
		body["image_manifest_digest"] = digest
	}
	if digestRef := strings.TrimSpace(opts.ImageDigestRef); digestRef != "" {
		body["image_digest_ref"] = digestRef
	}
	if platform := strings.TrimSpace(opts.ImagePlatform); platform != "" {
		body["image_platform"] = platform
	}
	if labels := cleanStringMap(opts.RequiredLabels); len(labels) > 0 {
		body["required_labels"] = labels
	}
	if capabilities := cleanStrings(opts.RequiredCapabilities); len(capabilities) > 0 {
		body["required_capabilities"] = capabilities
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

func Plan(ctx context.Context, opts ClientOptions) (PlacementPlan, error) {
	provider := normalizeProvider(opts)
	if provider != ProviderCloud {
		return PlacementPlan{}, errors.New("agentsandbox: plan is only supported for cloud sandboxes")
	}
	imageRef := strings.TrimSpace(opts.ImageRef)
	if imageRef == "" {
		return PlacementPlan{}, errors.New("agentsandbox: image ref required")
	}
	if opts.PlacementLimit < 0 {
		return PlacementPlan{}, errors.New("agentsandbox: placement limit must be non-negative")
	}
	seed := opts
	seed.SandboxID = "pending"
	c, err := NewClient(seed)
	if err != nil {
		return PlacementPlan{}, err
	}
	body := map[string]any{"image_ref": imageRef}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if bundle := strings.TrimSpace(opts.ManifestBundle); bundle != "" {
		body["manifest_bundle"] = bundle
	}
	if digest := strings.TrimSpace(opts.ImageManifestDigest); digest != "" {
		body["image_manifest_digest"] = digest
	}
	if digestRef := strings.TrimSpace(opts.ImageDigestRef); digestRef != "" {
		body["image_digest_ref"] = digestRef
	}
	if platform := strings.TrimSpace(opts.ImagePlatform); platform != "" {
		body["image_platform"] = platform
	}
	if labels := cleanStringMap(opts.RequiredLabels); len(labels) > 0 {
		body["required_labels"] = labels
	}
	if capabilities := cleanStrings(opts.RequiredCapabilities); len(capabilities) > 0 {
		body["required_capabilities"] = capabilities
	}
	if opts.PlacementLimit > 0 {
		body["limit"] = opts.PlacementLimit
	}
	var plan PlacementPlan
	if err := c.request(ctx, http.MethodPost, "/v1/placements/plan", body, &plan, c.timeout); err != nil {
		return PlacementPlan{}, err
	}
	return plan, nil
}

func PrepareImage(ctx context.Context, opts ImagePrepareOptions) (ImagePrepareResult, error) {
	sourceRef := strings.TrimSpace(opts.SourceRef)
	manifestBundle := strings.TrimSpace(opts.ManifestBundle)
	if sourceRef == "" && manifestBundle == "" {
		return ImagePrepareResult{}, errors.New("agentsandbox: image prepare source ref or manifest bundle required")
	}
	imageRef := strings.TrimSpace(opts.ImageRef)
	if imageRef == "" {
		return ImagePrepareResult{}, errors.New("agentsandbox: image prepare image ref required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "image-prepare")
	if err != nil {
		return ImagePrepareResult{}, err
	}
	body := map[string]any{"image_ref": imageRef}
	if sourceRef != "" {
		body["source_ref"] = sourceRef
	}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if manifestBundle != "" {
		body["manifest_bundle"] = manifestBundle
	}
	if digest := strings.TrimSpace(opts.ImageManifestDigest); digest != "" {
		body["image_manifest_digest"] = digest
	}
	if digestRef := strings.TrimSpace(opts.ImageDigestRef); digestRef != "" {
		body["image_digest_ref"] = digestRef
	}
	if platform := strings.TrimSpace(opts.ImagePlatform); platform != "" {
		body["image_platform"] = platform
	}
	if labels := cleanStringMap(opts.RequiredLabels); len(labels) > 0 {
		body["required_labels"] = labels
	}
	if capabilities := cleanStrings(opts.RequiredCapabilities); len(capabilities) > 0 {
		body["required_capabilities"] = capabilities
	}
	if opts.Force {
		body["force"] = true
	}
	var result ImagePrepareResult
	if err := c.request(ctx, http.MethodPost, "/v1/images/prepare", body, &result, c.timeout); err != nil {
		return ImagePrepareResult{}, err
	}
	return result, nil
}

func ListImagePreparations(ctx context.Context, opts ImagePrepareListOptions) (ImagePrepareListResult, error) {
	if opts.Limit < 0 {
		return ImagePrepareListResult{}, errors.New("agentsandbox: image preparation limit must be non-negative")
	}
	if opts.Offset < 0 {
		return ImagePrepareListResult{}, errors.New("agentsandbox: image preparation offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "image-preparations")
	if err != nil {
		return ImagePrepareListResult{}, err
	}
	query := map[string]string{
		"namespace":             opts.Namespace,
		"source_ref":            opts.SourceRef,
		"image_ref":             opts.ImageRef,
		"image_manifest_digest": opts.ImageManifestDigest,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result ImagePrepareListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/images/preparations", query), nil, &result, c.timeout); err != nil {
		return ImagePrepareListResult{}, err
	}
	if result.Count == 0 && len(result.Preparations) > 0 {
		result.Count = len(result.Preparations)
	}
	return result, nil
}

func GetImagePreparation(ctx context.Context, opts ImagePrepareGetOptions) (ImagePrepareResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return ImagePrepareResult{}, errors.New("agentsandbox: image preparation id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "image-preparation")
	if err != nil {
		return ImagePrepareResult{}, err
	}
	var result ImagePrepareResult
	if err := c.request(ctx, http.MethodGet, imagePreparationPath(id), nil, &result, c.timeout); err != nil {
		return ImagePrepareResult{}, err
	}
	return result, nil
}

func EnsureWarmPool(ctx context.Context, opts WarmPoolOptions) (WarmPoolResult, error) {
	name := strings.TrimSpace(opts.Name)
	imageRef := strings.TrimSpace(opts.ImageRef)
	if imageRef == "" {
		return WarmPoolResult{}, errors.New("agentsandbox: warm pool image ref required")
	}
	if opts.Size < 0 {
		return WarmPoolResult{}, errors.New("agentsandbox: warm pool size must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "warm-pool")
	if err != nil {
		return WarmPoolResult{}, err
	}
	body := map[string]any{
		"image_ref": imageRef,
		"size":      opts.Size,
	}
	if name != "" {
		body["name"] = name
	}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if bundle := strings.TrimSpace(opts.ManifestBundle); bundle != "" {
		body["manifest_bundle"] = bundle
	}
	if digest := strings.TrimSpace(opts.ImageManifestDigest); digest != "" {
		body["image_manifest_digest"] = digest
	}
	if digestRef := strings.TrimSpace(opts.ImageDigestRef); digestRef != "" {
		body["image_digest_ref"] = digestRef
	}
	if platform := strings.TrimSpace(opts.ImagePlatform); platform != "" {
		body["image_platform"] = platform
	}
	if policy := strings.TrimSpace(opts.Policy); policy != "" {
		body["policy"] = policy
	}
	if labels := cleanStringMap(opts.RequiredLabels); len(labels) > 0 {
		body["required_labels"] = labels
	}
	if capabilities := cleanStrings(opts.RequiredCapabilities); len(capabilities) > 0 {
		body["required_capabilities"] = capabilities
	}
	if nonzeroCapacity(opts.Resources) {
		body["resources"] = opts.Resources
	}
	if len(opts.Args) > 0 {
		body["args"] = cloneStrings(opts.Args)
	}
	var result WarmPoolResult
	if err := c.request(ctx, http.MethodPost, "/v1/warm-pools", body, &result, c.timeout); err != nil {
		return WarmPoolResult{}, err
	}
	return result, nil
}

func ListWarmPools(ctx context.Context, opts WarmPoolListOptions) ([]WarmPoolStatus, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "warm-pools")
	if err != nil {
		return nil, err
	}
	query := map[string]string{"namespace": opts.Namespace}
	var result WarmPoolListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/warm-pools", query), nil, &result, c.timeout); err != nil {
		return nil, err
	}
	return result.WarmPools, nil
}

func GetWarmPool(ctx context.Context, opts WarmPoolGetOptions) (WarmPoolStatus, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return WarmPoolStatus{}, errors.New("agentsandbox: warm pool name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "warm-pool")
	if err != nil {
		return WarmPoolStatus{}, err
	}
	var status WarmPoolStatus
	if err := c.request(ctx, http.MethodGet, warmPoolPath(name, ""), nil, &status, c.timeout); err != nil {
		return WarmPoolStatus{}, err
	}
	return status, nil
}

func DeleteWarmPool(ctx context.Context, opts WarmPoolGetOptions) (WarmPoolDeleteResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return WarmPoolDeleteResult{}, errors.New("agentsandbox: warm pool name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "warm-pool")
	if err != nil {
		return WarmPoolDeleteResult{}, err
	}
	var result WarmPoolDeleteResult
	if err := c.request(ctx, http.MethodDelete, warmPoolPath(name, ""), nil, &result, c.timeout); err != nil {
		return WarmPoolDeleteResult{}, err
	}
	return result, nil
}

func ClaimWarmPool(ctx context.Context, opts WarmPoolClaimOptions) (WarmPoolClaimResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return WarmPoolClaimResult{}, errors.New("agentsandbox: warm pool name required")
	}
	if len(opts.Command) == 0 || strings.TrimSpace(opts.Command[0]) == "" {
		return WarmPoolClaimResult{}, errors.New("agentsandbox: warm pool claim command required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "warm-pool")
	if err != nil {
		return WarmPoolClaimResult{}, err
	}
	body := map[string]any{
		"name":    name,
		"command": cloneStrings(opts.Command),
	}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if env := cloneStringMap(opts.Env); len(env) > 0 {
		body["env"] = env
	}
	var result WarmPoolClaimResult
	if err := c.request(ctx, http.MethodPost, "/v1/warm-pools/claim", body, &result, c.timeout); err != nil {
		return WarmPoolClaimResult{}, err
	}
	return result, nil
}

func WarmPoolEvents(ctx context.Context, opts WarmPoolEventListOptions) (SandboxEventListResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return SandboxEventListResult{}, errors.New("agentsandbox: warm pool name required")
	}
	if opts.Limit < 0 {
		return SandboxEventListResult{}, errors.New("agentsandbox: warm pool events limit must be non-negative")
	}
	if opts.Offset < 0 {
		return SandboxEventListResult{}, errors.New("agentsandbox: warm pool events offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "warm-pool")
	if err != nil {
		return SandboxEventListResult{}, err
	}
	query := map[string]string{
		"actor":         opts.Actor,
		"action":        opts.Action,
		"worker_id":     opts.WorkerID,
		"assignment_id": opts.AssignmentID,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result SandboxEventListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(warmPoolPath(name, "events"), query), nil, &result, c.timeout); err != nil {
		return SandboxEventListResult{}, err
	}
	if result.Count == 0 && len(result.Events) > 0 {
		result.Count = len(result.Events)
	}
	return result, nil
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

func (c *Client) Get(ctx context.Context) (SandboxStatus, error) {
	return c.Status(ctx)
}

func (c *Client) List(ctx context.Context, options ...SandboxListOptions) ([]SandboxStatus, error) {
	result, err := c.ListPage(ctx, options...)
	if err != nil {
		return nil, err
	}
	return result.Sandboxes, nil
}

func (c *Client) ListPage(ctx context.Context, options ...SandboxListOptions) (SandboxListResult, error) {
	if err := c.ready(); err != nil {
		return SandboxListResult{}, err
	}
	opt := SandboxListOptions{Namespace: c.namespace}
	if len(options) > 0 {
		opt = options[0]
		if opt.Namespace == "" {
			opt.Namespace = c.namespace
		}
	}
	query, err := opt.query()
	if err != nil {
		return SandboxListResult{}, err
	}
	if c.provider == ProviderLocal {
		status, err := c.Status(ctx)
		if err != nil {
			return SandboxListResult{}, err
		}
		result := SandboxListResult{Offset: opt.Offset, Limit: opt.Limit}
		if !sandboxMatchesListOptions(status, opt) {
			return result, nil
		}
		if opt.Offset == 0 {
			result.Sandboxes = []SandboxStatus{status}
		}
		result.Count = len(result.Sandboxes)
		return result, nil
	}
	var result SandboxListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/sandboxes", query), nil, &result, c.timeout); err != nil {
		return SandboxListResult{}, err
	}
	if result.Count == 0 && len(result.Sandboxes) > 0 {
		result.Count = len(result.Sandboxes)
	}
	return result, nil
}

func sandboxMatchesListOptions(status SandboxStatus, options SandboxListOptions) bool {
	if options.Namespace != "" && status.Namespace != "" && status.Namespace != options.Namespace {
		return false
	}
	if options.Status != "" && status.Status != options.Status {
		return false
	}
	if options.WorkerID != "" && status.WorkerID != options.WorkerID {
		return false
	}
	if options.ImageRef != "" && status.ImageRef != options.ImageRef {
		return false
	}
	return true
}

func (o SandboxListOptions) query() (map[string]string, error) {
	if o.Limit < 0 {
		return nil, errors.New("agentsandbox: sandbox list limit must be non-negative")
	}
	if o.Offset < 0 {
		return nil, errors.New("agentsandbox: sandbox list offset must be non-negative")
	}
	query := map[string]string{
		"namespace": o.Namespace,
		"status":    o.Status,
		"worker_id": o.WorkerID,
		"image_ref": o.ImageRef,
	}
	if o.Offset > 0 {
		query["offset"] = strconv.Itoa(o.Offset)
	}
	if o.Limit > 0 {
		query["limit"] = strconv.Itoa(o.Limit)
	}
	return query, nil
}

func (o SandboxEventListOptions) query() (map[string]string, error) {
	if o.Limit < 0 {
		return nil, errors.New("agentsandbox: sandbox events limit must be non-negative")
	}
	if o.Offset < 0 {
		return nil, errors.New("agentsandbox: sandbox events offset must be non-negative")
	}
	query := make(map[string]string)
	if actor := strings.TrimSpace(o.Actor); actor != "" {
		query["actor"] = actor
	}
	if action := strings.TrimSpace(o.Action); action != "" {
		query["action"] = action
	}
	if o.Offset > 0 {
		query["offset"] = strconv.Itoa(o.Offset)
	}
	if o.Limit > 0 {
		query["limit"] = strconv.Itoa(o.Limit)
	}
	return query, nil
}

func (o SandboxReportListOptions) query() (map[string]string, error) {
	if o.Limit < 0 {
		return nil, errors.New("agentsandbox: sandbox reports limit must be non-negative")
	}
	if o.Offset < 0 {
		return nil, errors.New("agentsandbox: sandbox reports offset must be non-negative")
	}
	query := make(map[string]string)
	if role := strings.TrimSpace(o.Role); role != "" {
		query["role"] = role
	}
	if status := strings.TrimSpace(o.Status); status != "" {
		query["status"] = status
	}
	if o.Offset > 0 {
		query["offset"] = strconv.Itoa(o.Offset)
	}
	if o.Limit > 0 {
		query["limit"] = strconv.Itoa(o.Limit)
	}
	return query, nil
}

func (c *Client) Wait(ctx context.Context, timeout time.Duration) (WaitResult, error) {
	if err := c.ready(); err != nil {
		return WaitResult{}, err
	}
	if c.provider != ProviderCloud {
		status, err := c.Status(ctx)
		if err != nil {
			return WaitResult{}, err
		}
		return WaitResult{Done: terminalSandboxStatus(status.Status), Sandbox: status}, nil
	}
	if timeout < 0 {
		return WaitResult{}, errors.New("agentsandbox: wait timeout must not be negative")
	}
	path := c.queryPath(c.sandboxPath("wait"), map[string]string{"timeout": formatSeconds(timeout)})
	requestTimeout := maxDuration(c.timeout, timeout+minDuration(timeout, 30*time.Second))
	var result WaitResult
	if err := c.request(ctx, http.MethodPost, path, map[string]any{}, &result, requestTimeout); err != nil {
		return WaitResult{}, err
	}
	c.vmName = result.Sandbox.VMName
	c.namespace = result.Sandbox.Namespace
	return result, nil
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

func (c *Client) Lease(ctx context.Context, holder string, ttl time.Duration) (LeaseResult, error) {
	if err := c.ready(); err != nil {
		return LeaseResult{}, err
	}
	if c.provider != ProviderCloud {
		return LeaseResult{}, errors.New("agentsandbox: lease is only supported for cloud sandboxes")
	}
	body := map[string]any{}
	if strings.TrimSpace(holder) != "" {
		body["holder"] = strings.TrimSpace(holder)
	}
	if ttl < 0 {
		return LeaseResult{}, errors.New("agentsandbox: lease ttl must not be negative")
	}
	if ttl > 0 {
		body["ttl"] = formatSeconds(ttl)
	}
	var result LeaseResult
	if err := c.request(ctx, http.MethodPost, c.sandboxPath("lease"), body, &result, c.timeout); err != nil {
		return LeaseResult{}, err
	}
	c.vmName = result.Sandbox.VMName
	c.namespace = result.Sandbox.Namespace
	c.leaseHolder = result.Lease.Holder
	return result, nil
}

func (c *Client) ReleaseLease(ctx context.Context, holder string) (LeaseResult, error) {
	if err := c.ready(); err != nil {
		return LeaseResult{}, err
	}
	if c.provider != ProviderCloud {
		return LeaseResult{}, errors.New("agentsandbox: lease is only supported for cloud sandboxes")
	}
	path := c.sandboxPath("lease")
	holder = strings.TrimSpace(holder)
	if holder == "" {
		holder = c.leaseHolder
	}
	if holder != "" {
		path = c.queryPath(path, map[string]string{"holder": holder})
	}
	var result LeaseResult
	if err := c.request(ctx, http.MethodDelete, path, nil, &result, c.timeout); err != nil {
		return LeaseResult{}, err
	}
	c.vmName = result.Sandbox.VMName
	c.namespace = result.Sandbox.Namespace
	if holder == c.leaseHolder {
		c.leaseHolder = ""
	}
	return result, nil
}

func (c *Client) Metering(ctx context.Context) (MeteringResult, error) {
	if err := c.ready(); err != nil {
		return MeteringResult{}, err
	}
	if c.provider != ProviderCloud {
		return MeteringResult{}, errors.New("agentsandbox: metering is only supported for cloud sandboxes")
	}
	var result MeteringResult
	if err := c.request(ctx, http.MethodGet, c.sandboxPath("metering"), nil, &result, c.timeout); err != nil {
		return MeteringResult{}, err
	}
	return result, nil
}

func (c *Client) ListMetering(ctx context.Context, sandboxID string) (MeteringResult, error) {
	if err := c.ready(); err != nil {
		return MeteringResult{}, err
	}
	if c.provider != ProviderCloud {
		return MeteringResult{}, errors.New("agentsandbox: metering is only supported for cloud sandboxes")
	}
	query := map[string]string{"namespace": c.namespace}
	if strings.TrimSpace(sandboxID) != "" {
		query["sandbox_id"] = strings.TrimSpace(sandboxID)
	}
	var result MeteringResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/metering/sandboxes", query), nil, &result, c.timeout); err != nil {
		return MeteringResult{}, err
	}
	return result, nil
}

func (c *Client) Events(ctx context.Context, options ...SandboxEventListOptions) (SandboxEventListResult, error) {
	if err := c.ready(); err != nil {
		return SandboxEventListResult{}, err
	}
	if c.provider != ProviderCloud {
		return SandboxEventListResult{}, errors.New("agentsandbox: events are only supported for cloud sandboxes")
	}
	opt := SandboxEventListOptions{}
	if len(options) > 0 {
		opt = options[0]
	}
	query, err := opt.query()
	if err != nil {
		return SandboxEventListResult{}, err
	}
	var result SandboxEventListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(c.sandboxPath("events"), query), nil, &result, c.timeout); err != nil {
		return SandboxEventListResult{}, err
	}
	if result.Count == 0 && len(result.Events) > 0 {
		result.Count = len(result.Events)
	}
	return result, nil
}

func (c *Client) Reports(ctx context.Context, options ...SandboxReportListOptions) (SandboxReportListResult, error) {
	if err := c.ready(); err != nil {
		return SandboxReportListResult{}, err
	}
	if c.provider != ProviderCloud {
		return SandboxReportListResult{}, errors.New("agentsandbox: reports are only supported for cloud sandboxes")
	}
	opt := SandboxReportListOptions{}
	if len(options) > 0 {
		opt = options[0]
	}
	query, err := opt.query()
	if err != nil {
		return SandboxReportListResult{}, err
	}
	var result SandboxReportListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(c.sandboxPath("reports"), query), nil, &result, c.timeout); err != nil {
		return SandboxReportListResult{}, err
	}
	if result.Count == 0 && len(result.Reports) > 0 {
		result.Count = len(result.Reports)
	}
	return result, nil
}

func (c *Client) Delete(ctx context.Context) error {
	if err := c.ready(); err != nil {
		return err
	}
	if c.provider == ProviderCloud {
		path := c.sandboxPath("")
		if c.leaseHolder != "" {
			path = c.queryPath(path, map[string]string{"holder": c.leaseHolder})
		}
		return c.request(ctx, http.MethodDelete, path, nil, nil, maxDuration(c.timeout, 2*time.Minute))
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
	if c.leaseHolder != "" {
		body["holder"] = c.leaseHolder
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
	body := map[string]any{}
	if c.leaseHolder != "" {
		body["holder"] = c.leaseHolder
	}
	return c.request(ctx, http.MethodPost, c.sandboxPath(action), body, &status, c.timeout)
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
	if c.leaseHolder != "" {
		body["holder"] = c.leaseHolder
	}
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

func warmPoolPath(name, action string) string {
	path := "/v1/warm-pools/" + url.PathEscape(name)
	if action != "" {
		path += "/" + url.PathEscape(action)
	}
	return path
}

func imagePreparationPath(id string) string {
	return "/v1/images/preparations/" + url.PathEscape(id)
}

func (c *Client) queryPath(path string, values map[string]string) string {
	query := make(url.Values)
	for key, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			query.Set(key, value)
		}
	}
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
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

func newFleetClient(fleetURL, apiKey, namespace string, timeout time.Duration, h *http.Client, id string) (*Client, error) {
	return NewClient(ClientOptions{
		Provider:  ProviderCloud,
		FleetURL:  fleetURL,
		APIKey:    apiKey,
		Namespace: namespace,
		SandboxID: id,
		Timeout:   timeout,
		HTTP:      h,
	})
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

func nonzeroCapacity(c Capacity) bool {
	return c.CPUs != 0 || c.MemoryBytes != 0 || c.VMs != 0 || c.MaxVMs != 0 || c.Images != 0
}

func cleanStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
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
