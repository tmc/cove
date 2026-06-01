package fleetcontrol

import "time"

const (
	VerbRegister        = "register"
	VerbHeartbeat       = "heartbeat"
	VerbAwaitAssignment = "await-assignment"
	VerbReportStatus    = "report-status"
)

const (
	PolicyLeastLoaded   = "least-loaded"
	PolicyImageAffinity = "image-affinity"
	PolicyBinPack       = "bin-pack"
)

const (
	ServiceAccountRoleViewer   = "viewer"
	ServiceAccountRoleOperator = "operator"
	ServiceAccountRoleAdmin    = "admin"
)

const DefaultWorkerTTL = 30 * time.Second

const DefaultAssignmentTTL = 30 * time.Second

const DefaultSandboxLeaseTTL = 30 * time.Second

const DefaultPlacementPlanLimit = 5

type Capacity struct {
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	VMs         int    `json:"vms,omitempty"`
	MaxVMs      int    `json:"max_vms,omitempty"`
	Images      int    `json:"images,omitempty"`
}

type WorkerHeartbeat struct {
	ID           string            `json:"id"`
	Host         string            `json:"host,omitempty"`
	Address      string            `json:"address,omitempty"`
	Version      string            `json:"version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	ImageRefs    []string          `json:"image_refs,omitempty"`
	ImageDetails []WorkerImage     `json:"image_details,omitempty"`
	Capacity
}

type WorkerImage struct {
	Ref                  string `json:"ref"`
	SourceManifestDigest string `json:"source_manifest_digest,omitempty"`
}

type HostRecord struct {
	ID               string            `json:"id"`
	Host             string            `json:"host,omitempty"`
	Address          string            `json:"address,omitempty"`
	Version          string            `json:"version,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	ImageRefs        []string          `json:"image_refs,omitempty"`
	ImageDetails     []WorkerImage     `json:"image_details,omitempty"`
	Capacity         Capacity          `json:"capacity,omitempty"`
	Status           string            `json:"status"`
	Cordoned         bool              `json:"cordoned"`
	CordonReason     string            `json:"cordon_reason,omitempty"`
	CordonedAt       time.Time         `json:"cordoned_at,omitempty"`
	Quarantined      bool              `json:"quarantined"`
	QuarantineReason string            `json:"quarantine_reason,omitempty"`
	QuarantinedAt    time.Time         `json:"quarantined_at,omitempty"`
	LastSeen         time.Time         `json:"last_seen"`
	Expires          time.Time         `json:"expires"`
	Report           *WorkerReport     `json:"last_report,omitempty"`
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

type ReconcileResult struct {
	StaleWorkers        []string `json:"stale_workers,omitempty"`
	RequeuedAssignments []string `json:"requeued_assignments,omitempty"`
	ReplacedAssignments []string `json:"replaced_assignments,omitempty"`
	WarmPoolAssignments []string `json:"warm_pool_assignments,omitempty"`
	WarmPoolCanceled    []string `json:"warm_pool_canceled,omitempty"`
	WarmPoolCleanup     []string `json:"warm_pool_cleanup,omitempty"`
}

type AuditEvent struct {
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

type AuditListFilter struct {
	Namespace  string `json:"namespace,omitempty"`
	Actor      string `json:"actor,omitempty"`
	Action     string `json:"action,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	SandboxID  string `json:"sandbox_id,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type AuditListResult struct {
	Events     []AuditEvent `json:"events"`
	Count      int          `json:"count"`
	Offset     int          `json:"offset,omitempty"`
	Limit      int          `json:"limit,omitempty"`
	NextOffset int          `json:"next_offset,omitempty"`
}

type AuditVerifyResult struct {
	OK       bool              `json:"ok"`
	Events   int               `json:"events"`
	HeadHash string            `json:"head_hash,omitempty"`
	Issues   []AuditChainIssue `json:"issues,omitempty"`
}

type AuditChainIssue struct {
	Index  int    `json:"index"`
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
}

type ServiceAccount struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace,omitempty"`
	Role      string    `json:"role,omitempty"`
	Created   time.Time `json:"created,omitempty"`
	Updated   time.Time `json:"updated,omitempty"`
}

type ServiceAccountRequest struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Role      string `json:"role,omitempty"`
	Token     string `json:"token"`
}

type ServiceAccountResult struct {
	ServiceAccount ServiceAccount `json:"service_account"`
}

type OIDCKey struct {
	KID string `json:"kid,omitempty"`
	Alg string `json:"alg,omitempty"`
	PEM string `json:"pem"`
}

type OIDCBinding struct {
	Name        string    `json:"name"`
	Issuer      string    `json:"issuer"`
	Subject     string    `json:"subject"`
	Audience    string    `json:"audience"`
	Namespace   string    `json:"namespace,omitempty"`
	Role        string    `json:"role,omitempty"`
	JWKSURL     string    `json:"jwks_url,omitempty"`
	JWKSFetched time.Time `json:"jwks_fetched,omitempty"`
	KeyIDs      []string  `json:"key_ids,omitempty"`
	Created     time.Time `json:"created,omitempty"`
	Updated     time.Time `json:"updated,omitempty"`
}

type OIDCBindingRequest struct {
	Name      string    `json:"name"`
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	Audience  string    `json:"audience"`
	Namespace string    `json:"namespace,omitempty"`
	Role      string    `json:"role,omitempty"`
	JWKSURL   string    `json:"jwks_url,omitempty"`
	Keys      []OIDCKey `json:"keys,omitempty"`
}

type OIDCBindingResult struct {
	Binding OIDCBinding `json:"binding"`
}

type SAMLBinding struct {
	Name              string    `json:"name"`
	EntityID          string    `json:"entity_id"`
	Subject           string    `json:"subject,omitempty"`
	SSOURL            string    `json:"sso_url"`
	Audience          string    `json:"audience"`
	Namespace         string    `json:"namespace,omitempty"`
	Role              string    `json:"role,omitempty"`
	CertificateSHA256 string    `json:"certificate_sha256,omitempty"`
	Created           time.Time `json:"created,omitempty"`
	Updated           time.Time `json:"updated,omitempty"`
}

type SAMLBindingRequest struct {
	Name           string `json:"name"`
	EntityID       string `json:"entity_id"`
	Subject        string `json:"subject,omitempty"`
	SSOURL         string `json:"sso_url"`
	Audience       string `json:"audience"`
	Namespace      string `json:"namespace,omitempty"`
	Role           string `json:"role,omitempty"`
	CertificatePEM string `json:"certificate_pem"`
}

type SAMLBindingResult struct {
	Binding SAMLBinding `json:"binding"`
}

type WorkerLifecycle struct {
	Reason string `json:"reason,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

type WorkerDrainResult struct {
	Worker    HostRecord          `json:"worker"`
	Sandboxes []SandboxStopResult `json:"sandboxes,omitempty"`
	Skipped   []WorkerDrainSkip   `json:"skipped,omitempty"`
}

type WorkerDecommissionResult struct {
	Worker   HostRecord                `json:"worker"`
	Reason   string                    `json:"reason,omitempty"`
	Force    bool                      `json:"force,omitempty"`
	Removed  bool                      `json:"removed"`
	Canceled []string                  `json:"canceled,omitempty"`
	Blocked  []WorkerDecommissionBlock `json:"blocked,omitempty"`
}

type WorkerDecommissionBlock struct {
	AssignmentID string `json:"assignment_id"`
	Status       string `json:"status,omitempty"`
	Reason       string `json:"reason"`
}

type WorkerDrainSkip struct {
	SandboxID string `json:"sandbox_id"`
	Status    string `json:"status,omitempty"`
	Reason    string `json:"reason"`
}

type ImagePrepareRequest struct {
	Namespace           string            `json:"namespace,omitempty"`
	SourceRef           string            `json:"source_ref"`
	ImageRef            string            `json:"image_ref"`
	ManifestBundle      string            `json:"manifest_bundle,omitempty"`
	ImageManifestDigest string            `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string            `json:"image_digest_ref,omitempty"`
	ImagePlatform       string            `json:"image_platform,omitempty"`
	RequiredLabels      map[string]string `json:"required_labels,omitempty"`
	Force               bool              `json:"force,omitempty"`
}

type ImagePrepareResult struct {
	Namespace           string             `json:"namespace,omitempty"`
	SourceRef           string             `json:"source_ref"`
	ImageRef            string             `json:"image_ref"`
	ImageManifestDigest string             `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string             `json:"image_digest_ref,omitempty"`
	ImagePlatform       string             `json:"image_platform,omitempty"`
	Assignments         []Assignment       `json:"assignments,omitempty"`
	Skipped             []ImagePrepareSkip `json:"skipped,omitempty"`
}

type ImagePrepareSkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type ImageGCRequest struct {
	Namespace      string            `json:"namespace,omitempty"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	OlderThan      string            `json:"older_than,omitempty"`
	Apply          bool              `json:"apply,omitempty"`
}

type ImageGCResult struct {
	Namespace   string        `json:"namespace,omitempty"`
	Assignments []Assignment  `json:"assignments,omitempty"`
	Skipped     []ImageGCSkip `json:"skipped,omitempty"`
}

type ImageGCSkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type LifecyclePolicyRequest struct {
	Namespace      string            `json:"namespace,omitempty"`
	VMName         string            `json:"vm_name"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Clear          bool              `json:"clear,omitempty"`
	IdleTimeout    string            `json:"idle_timeout,omitempty"`
	MaxAge         string            `json:"max_age,omitempty"`
	RunBudget      int               `json:"run_budget,omitempty"`
}

type LifecyclePolicyResult struct {
	Namespace   string                `json:"namespace,omitempty"`
	VMName      string                `json:"vm_name"`
	Assignments []Assignment          `json:"assignments,omitempty"`
	Skipped     []LifecyclePolicySkip `json:"skipped,omitempty"`
}

type LifecyclePolicySkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type StorageBudgetRequest struct {
	Namespace      string            `json:"namespace,omitempty"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Clear          bool              `json:"clear,omitempty"`
	Target         string            `json:"target,omitempty"`
	WarnPct        *int              `json:"warn_pct,omitempty"`
	HardPct        *int              `json:"hard_pct,omitempty"`
}

type StorageBudgetResult struct {
	Namespace   string              `json:"namespace,omitempty"`
	Assignments []Assignment        `json:"assignments,omitempty"`
	Skipped     []StoragePolicySkip `json:"skipped,omitempty"`
}

type StoragePruneRequest struct {
	Namespace      string            `json:"namespace,omitempty"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Category       string            `json:"category,omitempty"`
	OlderThan      string            `json:"older_than,omitempty"`
	Apply          bool              `json:"apply,omitempty"`
}

type StoragePruneResult struct {
	Namespace   string              `json:"namespace,omitempty"`
	Assignments []Assignment        `json:"assignments,omitempty"`
	Skipped     []StoragePolicySkip `json:"skipped,omitempty"`
}

type StoragePolicySkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type PlacementPlanRequest struct {
	Assignment
	Limit int `json:"limit,omitempty"`
}

type PlacementPlan struct {
	Namespace           string               `json:"namespace,omitempty"`
	Policy              string               `json:"policy"`
	ImageRef            string               `json:"image_ref,omitempty"`
	ImageManifestDigest string               `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string               `json:"image_digest_ref,omitempty"`
	ImagePlatform       string               `json:"image_platform,omitempty"`
	RequiredLabels      map[string]string    `json:"required_labels,omitempty"`
	AntiAffinityKey     string               `json:"anti_affinity_key,omitempty"`
	Resources           Capacity             `json:"resources,omitempty"`
	Candidates          []PlacementCandidate `json:"candidates,omitempty"`
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

type WarmPoolRequest struct {
	Namespace           string            `json:"namespace,omitempty"`
	Name                string            `json:"name,omitempty"`
	ImageRef            string            `json:"image_ref"`
	ManifestBundle      string            `json:"manifest_bundle,omitempty"`
	ImageManifestDigest string            `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string            `json:"image_digest_ref,omitempty"`
	ImagePlatform       string            `json:"image_platform,omitempty"`
	Size                int               `json:"size"`
	Policy              string            `json:"policy,omitempty"`
	RequiredLabels      map[string]string `json:"required_labels,omitempty"`
	Resources           Capacity          `json:"resources,omitempty"`
	Args                []string          `json:"args,omitempty"`
}

type WarmPool struct {
	Namespace           string            `json:"namespace,omitempty"`
	Name                string            `json:"name"`
	ImageRef            string            `json:"image_ref"`
	ImageManifestDigest string            `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string            `json:"image_digest_ref,omitempty"`
	ImagePlatform       string            `json:"image_platform,omitempty"`
	Size                int               `json:"size"`
	Policy              string            `json:"policy,omitempty"`
	RequiredLabels      map[string]string `json:"required_labels,omitempty"`
	Resources           Capacity          `json:"resources,omitempty"`
	Args                []string          `json:"args,omitempty"`
	Created             time.Time         `json:"created,omitempty"`
	Updated             time.Time         `json:"updated,omitempty"`
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

type WarmPoolDeleteResult struct {
	Namespace string       `json:"namespace,omitempty"`
	Pool      string       `json:"pool"`
	Canceled  []string     `json:"canceled,omitempty"`
	Cleanup   []Assignment `json:"cleanup,omitempty"`
	Deferred  []string     `json:"deferred,omitempty"`
}

type WarmPoolClaimRequest struct {
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name"`
	Command   []string          `json:"command"`
	Env       map[string]string `json:"env,omitempty"`
}

type WarmPoolClaimResult struct {
	Namespace  string     `json:"namespace,omitempty"`
	Pool       string     `json:"pool"`
	VMName     string     `json:"vm_name"`
	Slot       Assignment `json:"slot"`
	Assignment Assignment `json:"assignment"`
}

type SandboxRequest struct {
	Namespace           string            `json:"namespace,omitempty"`
	ID                  string            `json:"id,omitempty"`
	ImageRef            string            `json:"image_ref"`
	ManifestBundle      string            `json:"manifest_bundle,omitempty"`
	ImageManifestDigest string            `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string            `json:"image_digest_ref,omitempty"`
	ImagePlatform       string            `json:"image_platform,omitempty"`
	VMName              string            `json:"vm_name,omitempty"`
	Policy              string            `json:"policy,omitempty"`
	RequiredLabels      map[string]string `json:"required_labels,omitempty"`
	AntiAffinityKey     string            `json:"anti_affinity_key,omitempty"`
	Resources           Capacity          `json:"resources,omitempty"`
	Args                []string          `json:"args,omitempty"`
}

type SandboxStatus struct {
	Namespace           string        `json:"namespace,omitempty"`
	ID                  string        `json:"id"`
	VMName              string        `json:"vm_name"`
	ImageRef            string        `json:"image_ref"`
	ImageManifestDigest string        `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string        `json:"image_digest_ref,omitempty"`
	ImagePlatform       string        `json:"image_platform,omitempty"`
	WorkerID            string        `json:"worker_id,omitempty"`
	Status              string        `json:"status"`
	Lease               *SandboxLease `json:"lease,omitempty"`
	Assignment          Assignment    `json:"assignment"`
	Created             time.Time     `json:"created,omitempty"`
	Updated             time.Time     `json:"updated,omitempty"`
}

type SandboxListFilter struct {
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status,omitempty"`
	WorkerID  string `json:"worker_id,omitempty"`
	ImageRef  string `json:"image_ref,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type SandboxListResult struct {
	Sandboxes  []SandboxStatus `json:"sandboxes"`
	Count      int             `json:"count"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
}

type SandboxLeaseRequest struct {
	Holder string `json:"holder,omitempty"`
	TTL    string `json:"ttl,omitempty"`
}

type SandboxMutationRequest struct {
	Holder string `json:"holder,omitempty"`
}

type SandboxLease struct {
	Holder  string    `json:"holder"`
	Expires time.Time `json:"expires"`
}

type SandboxLeaseResult struct {
	Sandbox SandboxStatus `json:"sandbox"`
	Lease   SandboxLease  `json:"lease"`
}

type SandboxStartResult struct {
	Namespace  string     `json:"namespace,omitempty"`
	ID         string     `json:"id"`
	VMName     string     `json:"vm_name"`
	Status     string     `json:"status,omitempty"`
	Started    bool       `json:"started,omitempty"`
	Assignment Assignment `json:"assignment"`
}

type SandboxRestartResult struct {
	Namespace  string      `json:"namespace,omitempty"`
	ID         string      `json:"id"`
	VMName     string      `json:"vm_name"`
	Status     string      `json:"status,omitempty"`
	Restarting bool        `json:"restarting,omitempty"`
	Assignment Assignment  `json:"assignment"`
	Cleanup    *Assignment `json:"cleanup,omitempty"`
}

type SandboxMeteringRecord struct {
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
	Resources        Capacity  `json:"resources,omitempty"`
	VMMillis         int64     `json:"vm_millis"`
	CPUMillis        int64     `json:"cpu_millis,omitempty"`
	MemoryByteMillis uint64    `json:"memory_byte_millis,omitempty"`
}

type SandboxMeteringSummary struct {
	Namespace        string `json:"namespace,omitempty"`
	SandboxID        string `json:"sandbox_id,omitempty"`
	Records          int    `json:"records"`
	DurationMillis   int64  `json:"duration_millis"`
	VMMillis         int64  `json:"vm_millis"`
	CPUMillis        int64  `json:"cpu_millis,omitempty"`
	MemoryByteMillis uint64 `json:"memory_byte_millis,omitempty"`
}

type SandboxMeteringResult struct {
	Records []SandboxMeteringRecord `json:"records"`
	Summary SandboxMeteringSummary  `json:"summary"`
}

type SandboxReportFilter struct {
	Namespace string `json:"namespace,omitempty"`
	SandboxID string `json:"sandbox_id,omitempty"`
	Role      string `json:"role,omitempty"`
	Status    string `json:"status,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
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

type SandboxReportListResult struct {
	Reports    []SandboxReport `json:"reports"`
	Count      int             `json:"count"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
}

type OperationsSummary struct {
	Time        time.Time                   `json:"time"`
	Namespace   string                      `json:"namespace,omitempty"`
	Workers     WorkerOperationsSummary     `json:"workers"`
	Assignments AssignmentOperationsSummary `json:"assignments"`
	Sandboxes   SandboxOperationsSummary    `json:"sandboxes"`
	WarmPools   WarmPoolOperationsSummary   `json:"warm_pools"`
	Metering    SandboxMeteringSummary      `json:"metering"`
}

type WorkerOperationsSummary struct {
	Total       int            `json:"total"`
	Ready       int            `json:"ready"`
	Cordoned    int            `json:"cordoned"`
	Quarantined int            `json:"quarantined"`
	Stale       int            `json:"stale"`
	ByStatus    map[string]int `json:"by_status,omitempty"`
	Attention   []HostRecord   `json:"attention,omitempty"`
}

type AssignmentOperationsSummary struct {
	Total             int            `json:"total"`
	Active            int            `json:"active"`
	Terminal          int            `json:"terminal"`
	ByStatus          map[string]int `json:"by_status,omitempty"`
	ActiveAssignments []Assignment   `json:"active_assignments,omitempty"`
}

type SandboxOperationsSummary struct {
	Total             int             `json:"total"`
	Active            int             `json:"active"`
	Terminal          int             `json:"terminal"`
	ByStatus          map[string]int  `json:"by_status,omitempty"`
	ActiveSandboxes   []SandboxStatus `json:"active_sandboxes,omitempty"`
	DrainingSandboxes []SandboxStatus `json:"draining_sandboxes,omitempty"`
}

type WarmPoolOperationsSummary struct {
	Total    int              `json:"total"`
	Desired  int              `json:"desired"`
	Slots    int              `json:"slots"`
	Active   int              `json:"active"`
	Ready    int              `json:"ready"`
	Claimed  int              `json:"claimed"`
	Draining int              `json:"draining"`
	Terminal int              `json:"terminal"`
	ByStatus map[string]int   `json:"by_status,omitempty"`
	Pools    []WarmPoolStatus `json:"pools,omitempty"`
}

type SandboxExecRequest struct {
	Holder  string            `json:"holder,omitempty"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	Timeout string            `json:"timeout,omitempty"`
}

type SandboxExecResult struct {
	Namespace  string     `json:"namespace,omitempty"`
	ID         string     `json:"id"`
	VMName     string     `json:"vm_name"`
	Done       bool       `json:"done"`
	Assignment Assignment `json:"assignment"`
	ExitCode   int        `json:"exit_code,omitempty"`
	Stdout     string     `json:"stdout,omitempty"`
	Stderr     string     `json:"stderr,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type SandboxControlRequest struct {
	Holder     string         `json:"holder,omitempty"`
	Type       string         `json:"type"`
	Timeout    string         `json:"timeout,omitempty"`
	Screenshot map[string]any `json:"screenshot,omitempty"`
	Key        map[string]any `json:"key,omitempty"`
	Mouse      map[string]any `json:"mouse,omitempty"`
	Text       map[string]any `json:"text,omitempty"`
}

type SandboxControlResult struct {
	Namespace  string         `json:"namespace,omitempty"`
	ID         string         `json:"id"`
	VMName     string         `json:"vm_name"`
	Type       string         `json:"type"`
	Done       bool           `json:"done"`
	Assignment Assignment     `json:"assignment"`
	Data       string         `json:"data,omitempty"`
	Response   map[string]any `json:"response,omitempty"`
	ExitCode   int            `json:"exit_code,omitempty"`
	Stdout     string         `json:"stdout,omitempty"`
	Stderr     string         `json:"stderr,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type SandboxDeleteResult struct {
	Namespace  string      `json:"namespace,omitempty"`
	ID         string      `json:"id"`
	VMName     string      `json:"vm_name"`
	Status     string      `json:"status,omitempty"`
	Canceled   bool        `json:"canceled,omitempty"`
	Assignment Assignment  `json:"assignment"`
	Cleanup    *Assignment `json:"cleanup,omitempty"`
}

type SandboxStopResult struct {
	Namespace  string      `json:"namespace,omitempty"`
	ID         string      `json:"id"`
	VMName     string      `json:"vm_name"`
	Status     string      `json:"status,omitempty"`
	Canceled   bool        `json:"canceled,omitempty"`
	Assignment Assignment  `json:"assignment"`
	Cleanup    *Assignment `json:"cleanup,omitempty"`
}

type SandboxWaitResult struct {
	Done    bool          `json:"done"`
	Sandbox SandboxStatus `json:"sandbox"`
}

type Assignment struct {
	ID                  string            `json:"id"`
	Namespace           string            `json:"namespace,omitempty"`
	WorkerID            string            `json:"worker_id,omitempty"`
	WarmPool            string            `json:"warm_pool,omitempty"`
	WarmPoolSlot        string            `json:"warm_pool_slot,omitempty"`
	SandboxID           string            `json:"sandbox_id,omitempty"`
	SandboxRole         string            `json:"sandbox_role,omitempty"`
	SandboxLeaseHolder  string            `json:"sandbox_lease_holder,omitempty"`
	SandboxLeaseExpires time.Time         `json:"sandbox_lease_expires,omitempty"`
	Policy              string            `json:"policy,omitempty"`
	ImageRef            string            `json:"image_ref,omitempty"`
	ManifestBundle      string            `json:"manifest_bundle,omitempty"`
	ImageManifestDigest string            `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string            `json:"image_digest_ref,omitempty"`
	ImagePlatform       string            `json:"image_platform,omitempty"`
	RequiredLabels      map[string]string `json:"required_labels,omitempty"`
	AntiAffinityKey     string            `json:"anti_affinity_key,omitempty"`
	Resources           Capacity          `json:"resources,omitempty"`
	Verb                string            `json:"verb"`
	Args                []string          `json:"args,omitempty"`
	Status              string            `json:"status,omitempty"`
	Created             time.Time         `json:"created,omitempty"`
	Updated             time.Time         `json:"updated,omitempty"`
	LeasedTo            string            `json:"leased_to,omitempty"`
	LeaseExpires        time.Time         `json:"lease_expires,omitempty"`
	LastReport          *WorkerReport     `json:"last_report,omitempty"`
}
