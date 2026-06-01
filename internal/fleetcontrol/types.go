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

type WorkerListFilter struct {
	Status               string            `json:"status,omitempty"`
	Host                 string            `json:"host,omitempty"`
	Version              string            `json:"version,omitempty"`
	ImageRef             string            `json:"image_ref,omitempty"`
	SourceManifestDigest string            `json:"source_manifest_digest,omitempty"`
	Labels               map[string]string `json:"labels,omitempty"`
	Offset               int               `json:"offset,omitempty"`
	Limit                int               `json:"limit,omitempty"`
}

type WorkerListResult struct {
	Workers    []HostRecord `json:"workers"`
	Count      int          `json:"count"`
	Offset     int          `json:"offset,omitempty"`
	Limit      int          `json:"limit,omitempty"`
	NextOffset int          `json:"next_offset,omitempty"`
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
	Namespace    string `json:"namespace,omitempty"`
	Actor        string `json:"actor,omitempty"`
	Action       string `json:"action,omitempty"`
	TargetType   string `json:"target_type,omitempty"`
	TargetID     string `json:"target_id,omitempty"`
	WorkerID     string `json:"worker_id,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
	SandboxID    string `json:"sandbox_id,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Limit        int    `json:"limit,omitempty"`
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
	MetadataURL       string    `json:"metadata_url,omitempty"`
	MetadataFetched   time.Time `json:"metadata_fetched,omitempty"`
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
	MetadataURL    string `json:"metadata_url,omitempty"`
	MetadataXML    string `json:"metadata_xml,omitempty"`
}

type SAMLBindingResult struct {
	Binding SAMLBinding `json:"binding"`
}

type SAMLSessionRequest struct {
	SAMLResponse  string `json:"saml_response,omitempty"`
	SAMLAssertion string `json:"saml_assertion,omitempty"`
	RelayState    string `json:"relay_state,omitempty"`
	TTL           string `json:"ttl,omitempty"`
}

type SAMLSessionResult struct {
	Token      string      `json:"token,omitempty"`
	Expires    time.Time   `json:"expires"`
	Binding    SAMLBinding `json:"binding"`
	Subject    string      `json:"subject,omitempty"`
	RelayState string      `json:"relay_state,omitempty"`
}

type SAMLAuthnRequestResult struct {
	Binding      SAMLBinding `json:"binding"`
	RequestID    string      `json:"request_id"`
	IssueInstant time.Time   `json:"issue_instant"`
	RelayState   string      `json:"relay_state,omitempty"`
	XML          string      `json:"xml"`
	SAMLRequest  string      `json:"saml_request"`
	RedirectURL  string      `json:"redirect_url"`
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

type WorkerEvacuationRequest struct {
	Reason string `json:"reason,omitempty"`
	Apply  bool   `json:"apply,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

type WorkerEvacuationResult struct {
	Worker      HostRecord                   `json:"worker"`
	Reason      string                       `json:"reason,omitempty"`
	Apply       bool                         `json:"apply,omitempty"`
	Applied     bool                         `json:"applied,omitempty"`
	Force       bool                         `json:"force,omitempty"`
	Assignments []WorkerEvacuationAssignment `json:"assignments,omitempty"`
	Requeued    []Assignment                 `json:"requeued,omitempty"`
	Sandboxes   []SandboxStopResult          `json:"sandboxes,omitempty"`
	Canceled    []string                     `json:"canceled,omitempty"`
	Blocked     []WorkerEvacuationAssignment `json:"blocked,omitempty"`
}

type WorkerEvacuationAssignment struct {
	AssignmentID   string               `json:"assignment_id"`
	Namespace      string               `json:"namespace,omitempty"`
	SandboxID      string               `json:"sandbox_id,omitempty"`
	Status         string               `json:"status,omitempty"`
	WorkerID       string               `json:"worker_id,omitempty"`
	LeasedTo       string               `json:"leased_to,omitempty"`
	Action         string               `json:"action"`
	Reason         string               `json:"reason,omitempty"`
	TargetWorkerID string               `json:"target_worker_id,omitempty"`
	Candidates     []PlacementCandidate `json:"candidates,omitempty"`
}

type AssignmentCancelRequest struct {
	Reason string `json:"reason,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

type AssignmentCancelResult struct {
	Assignment     Assignment `json:"assignment"`
	Reason         string     `json:"reason,omitempty"`
	Force          bool       `json:"force,omitempty"`
	Canceled       bool       `json:"canceled"`
	PreviousStatus string     `json:"previous_status,omitempty"`
}

type AssignmentRetryRequest struct {
	Reason   string `json:"reason,omitempty"`
	WorkerID string `json:"worker_id,omitempty"`
	Replan   bool   `json:"replan,omitempty"`
}

type AssignmentRetryResult struct {
	Assignment       Assignment `json:"assignment"`
	Reason           string     `json:"reason,omitempty"`
	PreviousStatus   string     `json:"previous_status,omitempty"`
	PreviousWorkerID string     `json:"previous_worker_id,omitempty"`
	Replanned        bool       `json:"replanned,omitempty"`
}

type AssignmentListFilter struct {
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status,omitempty"`
	WorkerID  string `json:"worker_id,omitempty"`
	LeasedTo  string `json:"leased_to,omitempty"`
	Verb      string `json:"verb,omitempty"`
	ImageRef  string `json:"image_ref,omitempty"`
	SandboxID string `json:"sandbox_id,omitempty"`
	WarmPool  string `json:"warm_pool,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type AssignmentListResult struct {
	Assignments []Assignment `json:"assignments"`
	Count       int          `json:"count"`
	Offset      int          `json:"offset,omitempty"`
	Limit       int          `json:"limit,omitempty"`
	NextOffset  int          `json:"next_offset,omitempty"`
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
	ID                  string             `json:"id,omitempty"`
	Created             time.Time          `json:"created,omitempty"`
	Namespace           string             `json:"namespace,omitempty"`
	SourceRef           string             `json:"source_ref"`
	ImageRef            string             `json:"image_ref"`
	ImageManifestDigest string             `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string             `json:"image_digest_ref,omitempty"`
	ImagePlatform       string             `json:"image_platform,omitempty"`
	Assignments         []Assignment       `json:"assignments,omitempty"`
	Skipped             []ImagePrepareSkip `json:"skipped,omitempty"`
}

type ImagePrepareListFilter struct {
	Namespace           string `json:"namespace,omitempty"`
	SourceRef           string `json:"source_ref,omitempty"`
	ImageRef            string `json:"image_ref,omitempty"`
	ImageManifestDigest string `json:"image_manifest_digest,omitempty"`
	Offset              int    `json:"offset,omitempty"`
	Limit               int    `json:"limit,omitempty"`
}

type ImagePrepareListResult struct {
	Preparations []ImagePrepareResult `json:"preparations"`
	Count        int                  `json:"count"`
	Offset       int                  `json:"offset,omitempty"`
	Limit        int                  `json:"limit,omitempty"`
	NextOffset   int                  `json:"next_offset,omitempty"`
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
	ID             string            `json:"id,omitempty"`
	Created        time.Time         `json:"created,omitempty"`
	Namespace      string            `json:"namespace,omitempty"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	OlderThan      string            `json:"older_than,omitempty"`
	Apply          bool              `json:"apply"`
	Assignments    []Assignment      `json:"assignments,omitempty"`
	Skipped        []ImageGCSkip     `json:"skipped,omitempty"`
}

type ImageGCSkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type ImageGCListFilter struct {
	Namespace string `json:"namespace,omitempty"`
	OlderThan string `json:"older_than,omitempty"`
	Apply     *bool  `json:"apply,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type ImageGCListResult struct {
	Runs       []ImageGCResult `json:"runs"`
	Count      int             `json:"count"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
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
	ID             string                `json:"id,omitempty"`
	Created        time.Time             `json:"created,omitempty"`
	Namespace      string                `json:"namespace,omitempty"`
	VMName         string                `json:"vm_name"`
	RequiredLabels map[string]string     `json:"required_labels,omitempty"`
	Clear          bool                  `json:"clear"`
	IdleTimeout    string                `json:"idle_timeout,omitempty"`
	MaxAge         string                `json:"max_age,omitempty"`
	RunBudget      int                   `json:"run_budget,omitempty"`
	Assignments    []Assignment          `json:"assignments,omitempty"`
	Skipped        []LifecyclePolicySkip `json:"skipped,omitempty"`
}

type LifecyclePolicySkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type LifecyclePolicyListFilter struct {
	Namespace string `json:"namespace,omitempty"`
	VMName    string `json:"vm_name,omitempty"`
	Clear     *bool  `json:"clear,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type LifecyclePolicyListResult struct {
	Runs       []LifecyclePolicyResult `json:"runs"`
	Count      int                     `json:"count"`
	Offset     int                     `json:"offset,omitempty"`
	Limit      int                     `json:"limit,omitempty"`
	NextOffset int                     `json:"next_offset,omitempty"`
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
	ID             string              `json:"id,omitempty"`
	Created        time.Time           `json:"created,omitempty"`
	Namespace      string              `json:"namespace,omitempty"`
	RequiredLabels map[string]string   `json:"required_labels,omitempty"`
	Clear          bool                `json:"clear"`
	Target         string              `json:"target,omitempty"`
	WarnPct        *int                `json:"warn_pct,omitempty"`
	HardPct        *int                `json:"hard_pct,omitempty"`
	Assignments    []Assignment        `json:"assignments,omitempty"`
	Skipped        []StoragePolicySkip `json:"skipped,omitempty"`
}

type StorageBudgetListFilter struct {
	Namespace string `json:"namespace,omitempty"`
	Target    string `json:"target,omitempty"`
	Clear     *bool  `json:"clear,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type StorageBudgetListResult struct {
	Runs       []StorageBudgetResult `json:"runs"`
	Count      int                   `json:"count"`
	Offset     int                   `json:"offset,omitempty"`
	Limit      int                   `json:"limit,omitempty"`
	NextOffset int                   `json:"next_offset,omitempty"`
}

type StoragePruneRequest struct {
	Namespace      string            `json:"namespace,omitempty"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Category       string            `json:"category,omitempty"`
	OlderThan      string            `json:"older_than,omitempty"`
	Apply          bool              `json:"apply,omitempty"`
}

type StoragePruneResult struct {
	ID             string              `json:"id,omitempty"`
	Created        time.Time           `json:"created,omitempty"`
	Namespace      string              `json:"namespace,omitempty"`
	RequiredLabels map[string]string   `json:"required_labels,omitempty"`
	Category       string              `json:"category,omitempty"`
	OlderThan      string              `json:"older_than,omitempty"`
	Apply          bool                `json:"apply"`
	Assignments    []Assignment        `json:"assignments,omitempty"`
	Skipped        []StoragePolicySkip `json:"skipped,omitempty"`
}

type StoragePruneListFilter struct {
	Namespace string `json:"namespace,omitempty"`
	Category  string `json:"category,omitempty"`
	OlderThan string `json:"older_than,omitempty"`
	Apply     *bool  `json:"apply,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type StoragePruneListResult struct {
	Runs       []StoragePruneResult `json:"runs"`
	Count      int                  `json:"count"`
	Offset     int                  `json:"offset,omitempty"`
	Limit      int                  `json:"limit,omitempty"`
	NextOffset int                  `json:"next_offset,omitempty"`
}

type StoragePolicySkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

const (
	ControllerRunKindPlacementPlan   = "placement.plan"
	ControllerRunKindImagePrepare    = "image.prepare"
	ControllerRunKindImageGC         = "image.gc"
	ControllerRunKindLifecyclePolicy = "policy.lifecycle"
	ControllerRunKindStorageBudget   = "storage.budget"
	ControllerRunKindStoragePrune    = "storage.prune"
)

type ControllerRunListFilter struct {
	Namespace  string `json:"namespace,omitempty"`
	Kind       string `json:"kind,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type ControllerRunListResult struct {
	Runs       []ControllerRunSummary `json:"runs"`
	Count      int                    `json:"count"`
	Offset     int                    `json:"offset,omitempty"`
	Limit      int                    `json:"limit,omitempty"`
	NextOffset int                    `json:"next_offset,omitempty"`
}

type ControllerRunSummary struct {
	ID              string            `json:"id"`
	Created         time.Time         `json:"created"`
	Namespace       string            `json:"namespace,omitempty"`
	Kind            string            `json:"kind"`
	TargetType      string            `json:"target_type,omitempty"`
	TargetID        string            `json:"target_id,omitempty"`
	AssignmentCount int               `json:"assignment_count,omitempty"`
	SkipCount       int               `json:"skip_count,omitempty"`
	CandidateCount  int               `json:"candidate_count,omitempty"`
	Fields          map[string]string `json:"fields,omitempty"`
}

type PlacementPlanRequest struct {
	Assignment
	Limit int `json:"limit,omitempty"`
}

type PlacementPlan struct {
	ID                  string               `json:"id,omitempty"`
	Created             time.Time            `json:"created,omitempty"`
	Namespace           string               `json:"namespace,omitempty"`
	Policy              string               `json:"policy"`
	ImageRef            string               `json:"image_ref,omitempty"`
	ImageManifestDigest string               `json:"image_manifest_digest,omitempty"`
	ImageDigestRef      string               `json:"image_digest_ref,omitempty"`
	ImagePlatform       string               `json:"image_platform,omitempty"`
	RequiredLabels      map[string]string    `json:"required_labels,omitempty"`
	AntiAffinityKey     string               `json:"anti_affinity_key,omitempty"`
	Resources           Capacity             `json:"resources,omitempty"`
	Limit               int                  `json:"limit,omitempty"`
	Candidates          []PlacementCandidate `json:"candidates,omitempty"`
}

type PlacementPlanListFilter struct {
	Namespace string `json:"namespace,omitempty"`
	Policy    string `json:"policy,omitempty"`
	ImageRef  string `json:"image_ref,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type PlacementPlanListResult struct {
	Plans      []PlacementPlan `json:"plans"`
	Count      int             `json:"count"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
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
	AssignmentID     string `json:"assignment_id,omitempty"`
	WorkerID         string `json:"worker_id,omitempty"`
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

type AssignmentReportFilter struct {
	Namespace    string `json:"namespace,omitempty"`
	AssignmentID string `json:"assignment_id,omitempty"`
	WorkerID     string `json:"worker_id,omitempty"`
	Status       string `json:"status,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type AssignmentReport struct {
	Namespace    string       `json:"namespace,omitempty"`
	AssignmentID string       `json:"assignment_id"`
	WorkerID     string       `json:"worker_id,omitempty"`
	Status       string       `json:"status,omitempty"`
	Created      time.Time    `json:"created,omitempty"`
	Updated      time.Time    `json:"updated,omitempty"`
	Report       WorkerReport `json:"report"`
}

type AssignmentReportListResult struct {
	Reports    []AssignmentReport `json:"reports"`
	Count      int                `json:"count"`
	Offset     int                `json:"offset,omitempty"`
	Limit      int                `json:"limit,omitempty"`
	NextOffset int                `json:"next_offset,omitempty"`
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
