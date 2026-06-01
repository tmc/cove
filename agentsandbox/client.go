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
	"sort"
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
	MaxActiveSandboxes   int
	Priority             int
	QueueTTL             time.Duration
	RunTimeout           time.Duration
	MaxAttempts          int
	RetryDelay           time.Duration
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
	Namespace            string       `json:"namespace,omitempty"`
	ID                   string       `json:"id"`
	VMName               string       `json:"vm_name,omitempty"`
	ImageRef             string       `json:"image_ref,omitempty"`
	ImageManifestDigest  string       `json:"image_manifest_digest,omitempty"`
	ImageDigestRef       string       `json:"image_digest_ref,omitempty"`
	ImagePlatform        string       `json:"image_platform,omitempty"`
	RequiredCapabilities []string     `json:"required_capabilities,omitempty"`
	Status               string       `json:"status,omitempty"`
	WorkerID             string       `json:"worker_id,omitempty"`
	Lease                *Lease       `json:"lease,omitempty"`
	QueueExpires         time.Time    `json:"queue_expires,omitempty"`
	QueueAgeMillis       int64        `json:"queue_age_millis,omitempty"`
	QueueRemainingMillis int64        `json:"queue_remaining_millis,omitempty"`
	MaxAttempts          int          `json:"max_attempts,omitempty"`
	Attempt              int          `json:"attempt,omitempty"`
	RetryDelay           string       `json:"retry_delay,omitempty"`
	RetryAt              time.Time    `json:"retry_at,omitempty"`
	RetryRemainingMillis int64        `json:"retry_remaining_millis,omitempty"`
	Cleanup              *Assignment  `json:"cleanup,omitempty"`
	OpenAssignments      []Assignment `json:"open_assignments,omitempty"`
	Created              time.Time    `json:"created,omitempty"`
	Updated              time.Time    `json:"updated,omitempty"`
}

type SandboxListOptions struct {
	Namespace           string
	Status              string
	WorkerID            string
	ImageRef            string
	ImageManifestDigest string
	ImageDigestRef      string
	ImagePlatform       string
	RequiredCapability  string
	HasOpenAssignments  *bool
	Retrying            *bool
	HasCleanup          *bool
	HasLease            *bool
	LeaseHolder         string
	Offset              int
	Limit               int
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

type PlacementPlanListOptions struct {
	FleetURL            string
	APIKey              string
	Namespace           string
	Policy              string
	ImageRef            string
	ImageManifestDigest string
	ImageDigestRef      string
	ImagePlatform       string
	RequiredCapability  string
	Offset              int
	Limit               int
	Timeout             time.Duration
	HTTP                *http.Client
}

type PlacementPlanGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type PlacementPlanListResult struct {
	Plans      []PlacementPlan `json:"plans"`
	Count      int             `json:"count"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
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
	Priority             int               `json:"priority,omitempty"`
	QueueExpires         time.Time         `json:"queue_expires,omitempty"`
	RunTimeout           string            `json:"run_timeout,omitempty"`
	MaxAttempts          int               `json:"max_attempts,omitempty"`
	Attempt              int               `json:"attempt,omitempty"`
	RetryDelay           string            `json:"retry_delay,omitempty"`
	RetryAt              time.Time         `json:"retry_at,omitempty"`
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
	DryRun               bool
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
	ImageDigestRef      string
	ImagePlatform       string
	RequiredCapability  string
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
	DryRun               bool               `json:"dry_run,omitempty"`
	Assignments          []Assignment       `json:"assignments,omitempty"`
	Skipped              []ImagePrepareSkip `json:"skipped,omitempty"`
}

type ImagePrepareSkip struct {
	WorkerID            string            `json:"worker_id"`
	Reason              string            `json:"reason"`
	Status              string            `json:"status,omitempty"`
	MissingLabels       map[string]string `json:"missing_labels,omitempty"`
	MissingCapabilities []string          `json:"missing_capabilities,omitempty"`
}

type ImagePrepareListResult struct {
	Preparations []ImagePrepareResult `json:"preparations"`
	Count        int                  `json:"count"`
	Offset       int                  `json:"offset,omitempty"`
	Limit        int                  `json:"limit,omitempty"`
	NextOffset   int                  `json:"next_offset,omitempty"`
}

type ImageGCOptions struct {
	FleetURL             string
	APIKey               string
	Namespace            string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	OlderThan            string
	Apply                bool
	DryRun               bool
	Timeout              time.Duration
	HTTP                 *http.Client
}

type ImageGCListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	OlderThan string
	Apply     *bool
	Offset    int
	Limit     int
	Timeout   time.Duration
	HTTP      *http.Client
}

type ImageGCGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type ImageGCResult struct {
	ID                   string            `json:"id,omitempty"`
	Created              time.Time         `json:"created,omitempty"`
	Namespace            string            `json:"namespace,omitempty"`
	RequiredLabels       map[string]string `json:"required_labels,omitempty"`
	RequiredCapabilities []string          `json:"required_capabilities,omitempty"`
	OlderThan            string            `json:"older_than,omitempty"`
	Apply                bool              `json:"apply"`
	DryRun               bool              `json:"dry_run,omitempty"`
	Assignments          []Assignment      `json:"assignments,omitempty"`
	Skipped              []ImageGCSkip     `json:"skipped,omitempty"`
}

type ImageGCSkip struct {
	WorkerID            string            `json:"worker_id"`
	Reason              string            `json:"reason"`
	Status              string            `json:"status,omitempty"`
	MissingLabels       map[string]string `json:"missing_labels,omitempty"`
	MissingCapabilities []string          `json:"missing_capabilities,omitempty"`
}

type ImageGCListResult struct {
	Runs       []ImageGCResult `json:"runs"`
	Count      int             `json:"count"`
	Offset     int             `json:"offset,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
}

type LifecyclePolicyOptions struct {
	FleetURL             string
	APIKey               string
	Namespace            string
	VMName               string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	Clear                bool
	IdleTimeout          string
	MaxAge               string
	RunBudget            int
	DryRun               bool
	Timeout              time.Duration
	HTTP                 *http.Client
}

type LifecyclePolicyListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	VMName    string
	Clear     *bool
	Offset    int
	Limit     int
	Timeout   time.Duration
	HTTP      *http.Client
}

type LifecyclePolicyGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type LifecyclePolicyResult struct {
	ID                   string                `json:"id,omitempty"`
	Created              time.Time             `json:"created,omitempty"`
	Namespace            string                `json:"namespace,omitempty"`
	VMName               string                `json:"vm_name"`
	RequiredLabels       map[string]string     `json:"required_labels,omitempty"`
	RequiredCapabilities []string              `json:"required_capabilities,omitempty"`
	Clear                bool                  `json:"clear"`
	IdleTimeout          string                `json:"idle_timeout,omitempty"`
	MaxAge               string                `json:"max_age,omitempty"`
	RunBudget            int                   `json:"run_budget,omitempty"`
	DryRun               bool                  `json:"dry_run,omitempty"`
	Assignments          []Assignment          `json:"assignments,omitempty"`
	Skipped              []LifecyclePolicySkip `json:"skipped,omitempty"`
}

type LifecyclePolicySkip struct {
	WorkerID            string            `json:"worker_id"`
	Reason              string            `json:"reason"`
	Status              string            `json:"status,omitempty"`
	MissingLabels       map[string]string `json:"missing_labels,omitempty"`
	MissingCapabilities []string          `json:"missing_capabilities,omitempty"`
}

type LifecyclePolicyListResult struct {
	Runs       []LifecyclePolicyResult `json:"runs"`
	Count      int                     `json:"count"`
	Offset     int                     `json:"offset,omitempty"`
	Limit      int                     `json:"limit,omitempty"`
	NextOffset int                     `json:"next_offset,omitempty"`
}

type StorageBudgetOptions struct {
	FleetURL             string
	APIKey               string
	Namespace            string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	Clear                bool
	Target               string
	WarnPct              *int
	HardPct              *int
	DryRun               bool
	Timeout              time.Duration
	HTTP                 *http.Client
}

type StorageBudgetListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Target    string
	Clear     *bool
	Offset    int
	Limit     int
	Timeout   time.Duration
	HTTP      *http.Client
}

type StorageBudgetGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type StorageBudgetResult struct {
	ID                   string              `json:"id,omitempty"`
	Created              time.Time           `json:"created,omitempty"`
	Namespace            string              `json:"namespace,omitempty"`
	RequiredLabels       map[string]string   `json:"required_labels,omitempty"`
	RequiredCapabilities []string            `json:"required_capabilities,omitempty"`
	Clear                bool                `json:"clear"`
	Target               string              `json:"target,omitempty"`
	WarnPct              *int                `json:"warn_pct,omitempty"`
	HardPct              *int                `json:"hard_pct,omitempty"`
	DryRun               bool                `json:"dry_run,omitempty"`
	Assignments          []Assignment        `json:"assignments,omitempty"`
	Skipped              []StoragePolicySkip `json:"skipped,omitempty"`
}

type StorageBudgetListResult struct {
	Runs       []StorageBudgetResult `json:"runs"`
	Count      int                   `json:"count"`
	Offset     int                   `json:"offset,omitempty"`
	Limit      int                   `json:"limit,omitempty"`
	NextOffset int                   `json:"next_offset,omitempty"`
}

type StoragePruneOptions struct {
	FleetURL             string
	APIKey               string
	Namespace            string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	Category             string
	OlderThan            string
	Apply                bool
	DryRun               bool
	Timeout              time.Duration
	HTTP                 *http.Client
}

type StoragePruneListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Category  string
	OlderThan string
	Apply     *bool
	Offset    int
	Limit     int
	Timeout   time.Duration
	HTTP      *http.Client
}

type StoragePruneGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type StoragePruneResult struct {
	ID                   string              `json:"id,omitempty"`
	Created              time.Time           `json:"created,omitempty"`
	Namespace            string              `json:"namespace,omitempty"`
	RequiredLabels       map[string]string   `json:"required_labels,omitempty"`
	RequiredCapabilities []string            `json:"required_capabilities,omitempty"`
	Category             string              `json:"category,omitempty"`
	OlderThan            string              `json:"older_than,omitempty"`
	Apply                bool                `json:"apply"`
	DryRun               bool                `json:"dry_run,omitempty"`
	Assignments          []Assignment        `json:"assignments,omitempty"`
	Skipped              []StoragePolicySkip `json:"skipped,omitempty"`
}

type StoragePolicySkip struct {
	WorkerID            string            `json:"worker_id"`
	Reason              string            `json:"reason"`
	Status              string            `json:"status,omitempty"`
	MissingLabels       map[string]string `json:"missing_labels,omitempty"`
	MissingCapabilities []string          `json:"missing_capabilities,omitempty"`
}

type StoragePruneListResult struct {
	Runs       []StoragePruneResult `json:"runs"`
	Count      int                  `json:"count"`
	Offset     int                  `json:"offset,omitempty"`
	Limit      int                  `json:"limit,omitempty"`
	NextOffset int                  `json:"next_offset,omitempty"`
}

type ControllerRunListOptions struct {
	FleetURL            string
	APIKey              string
	Namespace           string
	Kind                string
	TargetType          string
	TargetID            string
	SourceRef           string
	ImageRef            string
	ImageManifestDigest string
	ImageDigestRef      string
	ImagePlatform       string
	RequiredCapability  string
	AssignmentID        string
	WorkerID            string
	CandidateWorkerID   string
	SkippedWorkerID     string
	Offset              int
	Limit               int
	Timeout             time.Duration
	HTTP                *http.Client
}

type ControllerRunGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type ControllerRunListResult struct {
	Runs       []ControllerRunSummary `json:"runs"`
	Count      int                    `json:"count"`
	Offset     int                    `json:"offset,omitempty"`
	Limit      int                    `json:"limit,omitempty"`
	NextOffset int                    `json:"next_offset,omitempty"`
}

type ControllerRunDetail struct {
	Summary            ControllerRunSummary   `json:"summary"`
	AssignmentIDs      []string               `json:"assignment_ids,omitempty"`
	Assignments        []Assignment           `json:"assignments,omitempty"`
	WorkerIDs          []string               `json:"worker_ids,omitempty"`
	CandidateWorkerIDs []string               `json:"candidate_worker_ids,omitempty"`
	SkippedWorkerIDs   []string               `json:"skipped_worker_ids,omitempty"`
	PlacementPlan      *PlacementPlan         `json:"placement_plan,omitempty"`
	ImagePreparation   *ImagePrepareResult    `json:"image_preparation,omitempty"`
	ImageGC            *ImageGCResult         `json:"image_gc,omitempty"`
	LifecyclePolicy    *LifecyclePolicyResult `json:"lifecycle_policy,omitempty"`
	StorageBudget      *StorageBudgetResult   `json:"storage_budget,omitempty"`
	StoragePrune       *StoragePruneResult    `json:"storage_prune,omitempty"`
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

type ReconcileOptions struct {
	FleetURL string
	APIKey   string
	Timeout  time.Duration
	HTTP     *http.Client
}

type ReconcileResult struct {
	StaleWorkers        []string `json:"stale_workers,omitempty"`
	RequeuedAssignments []string `json:"requeued_assignments,omitempty"`
	ReplacedAssignments []string `json:"replaced_assignments,omitempty"`
	ExpiredAssignments  []string `json:"expired_assignments,omitempty"`
	WarmPoolAssignments []string `json:"warm_pool_assignments,omitempty"`
	WarmPoolCanceled    []string `json:"warm_pool_canceled,omitempty"`
	WarmPoolCleanup     []string `json:"warm_pool_cleanup,omitempty"`
}

type OperationsSummaryOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Timeout   time.Duration
	HTTP      *http.Client
}

type AuditListOptions struct {
	FleetURL     string
	APIKey       string
	Namespace    string
	Actor        string
	Action       string
	TargetType   string
	TargetID     string
	WorkerID     string
	AssignmentID string
	SandboxID    string
	Offset       int
	Limit        int
	Timeout      time.Duration
	HTTP         *http.Client
}

type AuditListResult struct {
	Events     []AuditEvent `json:"events"`
	Count      int          `json:"count"`
	Offset     int          `json:"offset,omitempty"`
	Limit      int          `json:"limit,omitempty"`
	NextOffset int          `json:"next_offset,omitempty"`
}

type AuditVerifyOptions struct {
	FleetURL string
	APIKey   string
	Timeout  time.Duration
	HTTP     *http.Client
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

type ServiceAccountListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Timeout   time.Duration
	HTTP      *http.Client
}

type ServiceAccountUpsertOptions struct {
	FleetURL  string
	APIKey    string
	Name      string
	Namespace string
	Role      string
	Token     string
	Timeout   time.Duration
	HTTP      *http.Client
}

type ServiceAccountDeleteOptions struct {
	FleetURL string
	APIKey   string
	Name     string
	Timeout  time.Duration
	HTTP     *http.Client
}

type ServiceAccountListResult struct {
	ServiceAccounts []ServiceAccount `json:"service_accounts"`
	Count           int              `json:"count,omitempty"`
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

type OIDCBindingListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Timeout   time.Duration
	HTTP      *http.Client
}

type OIDCBindingUpsertOptions struct {
	FleetURL  string
	APIKey    string
	Name      string
	Issuer    string
	Subject   string
	Audience  string
	Namespace string
	Role      string
	JWKSURL   string
	Keys      []OIDCKey
	Timeout   time.Duration
	HTTP      *http.Client
}

type OIDCBindingDeleteOptions struct {
	FleetURL string
	APIKey   string
	Name     string
	Timeout  time.Duration
	HTTP     *http.Client
}

type OIDCBindingListResult struct {
	Bindings []OIDCBinding `json:"oidc_bindings"`
	Count    int           `json:"count,omitempty"`
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

type SAMLBindingListOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	Timeout   time.Duration
	HTTP      *http.Client
}

type SAMLBindingUpsertOptions struct {
	FleetURL       string
	APIKey         string
	Name           string
	EntityID       string
	Subject        string
	SSOURL         string
	Audience       string
	Namespace      string
	Role           string
	CertificatePEM string
	MetadataURL    string
	MetadataXML    string
	Timeout        time.Duration
	HTTP           *http.Client
}

type SAMLBindingNameOptions struct {
	FleetURL string
	APIKey   string
	Name     string
	Timeout  time.Duration
	HTTP     *http.Client
}

type SAMLBindingLoginOptions struct {
	FleetURL   string
	APIKey     string
	Name       string
	RelayState string
	Timeout    time.Duration
	HTTP       *http.Client
}

type SAMLSessionOptions struct {
	FleetURL      string
	APIKey        string
	SAMLResponse  string
	SAMLAssertion string
	RelayState    string
	TTL           string
	Timeout       time.Duration
	HTTP          *http.Client
}

type SAMLBindingListResult struct {
	Bindings []SAMLBinding `json:"saml_bindings"`
	Count    int           `json:"count,omitempty"`
}

type SAMLBindingResult struct {
	Binding SAMLBinding `json:"binding"`
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

type SAMLSessionResult struct {
	Token      string      `json:"token,omitempty"`
	Expires    time.Time   `json:"expires"`
	Binding    SAMLBinding `json:"binding"`
	Subject    string      `json:"subject,omitempty"`
	RelayState string      `json:"relay_state,omitempty"`
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
	Capabilities     []string          `json:"capabilities,omitempty"`
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

type WorkerListOptions struct {
	FleetURL             string
	APIKey               string
	Status               string
	Host                 string
	Version              string
	ImageRef             string
	SourceManifestDigest string
	Labels               map[string]string
	Capabilities         []string
	Offset               int
	Limit                int
	Timeout              time.Duration
	HTTP                 *http.Client
}

type WorkerGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type WorkerLifecycleOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Reason   string
	Force    bool
	Timeout  time.Duration
	HTTP     *http.Client
}

type WorkerEvacuationOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Reason   string
	Apply    bool
	Force    bool
	Timeout  time.Duration
	HTTP     *http.Client
}

type WorkerEventListOptions struct {
	FleetURL   string
	APIKey     string
	ID         string
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	SandboxID  string
	Offset     int
	Limit      int
	Timeout    time.Duration
	HTTP       *http.Client
}

type WorkerReportListOptions struct {
	FleetURL     string
	APIKey       string
	ID           string
	AssignmentID string
	Status       string
	Offset       int
	Limit        int
	Timeout      time.Duration
	HTTP         *http.Client
}

type WorkerMeteringOptions struct {
	FleetURL  string
	APIKey    string
	ID        string
	Namespace string
	SandboxID string
	Status    string
	Timeout   time.Duration
	HTTP      *http.Client
}

type SandboxMeteringOptions struct {
	FleetURL  string
	APIKey    string
	Namespace string
	SandboxID string
	Timeout   time.Duration
	HTTP      *http.Client
}

type WorkerSandboxListOptions struct {
	FleetURL            string
	APIKey              string
	ID                  string
	Namespace           string
	Status              string
	ImageRef            string
	ImageManifestDigest string
	ImageDigestRef      string
	ImagePlatform       string
	RequiredCapability  string
	HasOpenAssignments  *bool
	Retrying            *bool
	HasCleanup          *bool
	HasLease            *bool
	LeaseHolder         string
	Offset              int
	Limit               int
	Timeout             time.Duration
	HTTP                *http.Client
}

type WorkerListResult struct {
	Workers    []HostRecord `json:"workers"`
	Count      int          `json:"count"`
	Offset     int          `json:"offset,omitempty"`
	Limit      int          `json:"limit,omitempty"`
	NextOffset int          `json:"next_offset,omitempty"`
}

type WorkerDrainResult struct {
	Worker    HostRecord          `json:"worker"`
	Sandboxes []SandboxStopResult `json:"sandboxes,omitempty"`
	Skipped   []WorkerDrainSkip   `json:"skipped,omitempty"`
}

type WorkerDrainSkip struct {
	SandboxID string `json:"sandbox_id"`
	Status    string `json:"status,omitempty"`
	Reason    string `json:"reason"`
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

type AssignmentCreateOptions struct {
	FleetURL             string
	APIKey               string
	Namespace            string
	ID                   string
	WorkerID             string
	Policy               string
	ImageRef             string
	ManifestBundle       string
	ImageManifestDigest  string
	ImageDigestRef       string
	ImagePlatform        string
	RequiredLabels       map[string]string
	RequiredCapabilities []string
	AntiAffinityKey      string
	Resources            Capacity
	Priority             int
	QueueTTL             time.Duration
	RunTimeout           time.Duration
	MaxAttempts          int
	RetryDelay           time.Duration
	Verb                 string
	Args                 []string
	Timeout              time.Duration
	HTTP                 *http.Client
}

type AssignmentListOptions struct {
	FleetURL            string
	APIKey              string
	Namespace           string
	Status              string
	WorkerID            string
	LeasedTo            string
	Verb                string
	ImageRef            string
	ImageManifestDigest string
	ImageDigestRef      string
	ImagePlatform       string
	RequiredCapability  string
	SandboxID           string
	WarmPool            string
	Offset              int
	Limit               int
	Timeout             time.Duration
	HTTP                *http.Client
}

type AssignmentGetOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Timeout  time.Duration
	HTTP     *http.Client
}

type AssignmentCancelOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Reason   string
	Force    bool
	Timeout  time.Duration
	HTTP     *http.Client
}

type AssignmentRetryOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Reason   string
	WorkerID string
	Replan   bool
	Timeout  time.Duration
	HTTP     *http.Client
}

type AssignmentEventListOptions struct {
	FleetURL   string
	APIKey     string
	ID         string
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	WorkerID   string
	SandboxID  string
	Offset     int
	Limit      int
	Timeout    time.Duration
	HTTP       *http.Client
}

type AssignmentReportListOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	WorkerID string
	Status   string
	Offset   int
	Limit    int
	Timeout  time.Duration
	HTTP     *http.Client
}

type AssignmentMeteringOptions struct {
	FleetURL string
	APIKey   string
	ID       string
	Status   string
	Timeout  time.Duration
	HTTP     *http.Client
}

type AssignmentListResult struct {
	Assignments []Assignment `json:"assignments"`
	Count       int          `json:"count"`
	Offset      int          `json:"offset,omitempty"`
	Limit       int          `json:"limit,omitempty"`
	NextOffset  int          `json:"next_offset,omitempty"`
}

type AssignmentCancelResult struct {
	Assignment     Assignment `json:"assignment"`
	Reason         string     `json:"reason,omitempty"`
	Force          bool       `json:"force,omitempty"`
	Canceled       bool       `json:"canceled"`
	PreviousStatus string     `json:"previous_status,omitempty"`
}

type AssignmentRetryResult struct {
	Assignment       Assignment `json:"assignment"`
	Reason           string     `json:"reason,omitempty"`
	PreviousStatus   string     `json:"previous_status,omitempty"`
	PreviousWorkerID string     `json:"previous_worker_id,omitempty"`
	Replanned        bool       `json:"replanned,omitempty"`
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
	Namespace           string      `json:"namespace,omitempty"`
	ID                  string      `json:"id"`
	VMName              string      `json:"vm_name"`
	Status              string      `json:"status,omitempty"`
	Restarting          bool        `json:"restarting,omitempty"`
	Assignment          Assignment  `json:"assignment"`
	Cleanup             *Assignment `json:"cleanup,omitempty"`
	CanceledAssignments []string    `json:"canceled_assignments,omitempty"`
}

type SandboxStopResult struct {
	Namespace           string      `json:"namespace,omitempty"`
	ID                  string      `json:"id"`
	VMName              string      `json:"vm_name"`
	Status              string      `json:"status,omitempty"`
	Canceled            bool        `json:"canceled,omitempty"`
	Assignment          Assignment  `json:"assignment"`
	Cleanup             *Assignment `json:"cleanup,omitempty"`
	CanceledAssignments []string    `json:"canceled_assignments,omitempty"`
}

type SandboxDeleteResult struct {
	Namespace           string      `json:"namespace,omitempty"`
	ID                  string      `json:"id"`
	VMName              string      `json:"vm_name"`
	Status              string      `json:"status,omitempty"`
	Canceled            bool        `json:"canceled,omitempty"`
	Assignment          Assignment  `json:"assignment"`
	Cleanup             *Assignment `json:"cleanup,omitempty"`
	CanceledAssignments []string    `json:"canceled_assignments,omitempty"`
}

type OperationsSummary struct {
	Time        time.Time                   `json:"time"`
	Namespace   string                      `json:"namespace,omitempty"`
	Workers     WorkerOperationsSummary     `json:"workers"`
	Assignments AssignmentOperationsSummary `json:"assignments"`
	Sandboxes   SandboxOperationsSummary    `json:"sandboxes"`
	WarmPools   WarmPoolOperationsSummary   `json:"warm_pools"`
	Metering    MeteringSummary             `json:"metering"`
}

type WorkerOperationsSummary struct {
	Total        int                       `json:"total"`
	Ready        int                       `json:"ready"`
	Cordoned     int                       `json:"cordoned"`
	Quarantined  int                       `json:"quarantined"`
	Stale        int                       `json:"stale"`
	ByStatus     map[string]int            `json:"by_status,omitempty"`
	Capabilities []WorkerCapabilitySummary `json:"capabilities,omitempty"`
	Attention    []HostRecord              `json:"attention,omitempty"`
}

type WorkerCapabilitySummary struct {
	Name        string         `json:"name"`
	Total       int            `json:"total"`
	Ready       int            `json:"ready"`
	Cordoned    int            `json:"cordoned"`
	Quarantined int            `json:"quarantined"`
	Stale       int            `json:"stale"`
	ByStatus    map[string]int `json:"by_status,omitempty"`
	Workers     []string       `json:"workers,omitempty"`
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
	FleetURL            string
	APIKey              string
	Namespace           string
	ImageRef            string
	ImageManifestDigest string
	ImageDigestRef      string
	ImagePlatform       string
	RequiredCapability  string
	Offset              int
	Limit               int
	Timeout             time.Duration
	HTTP                *http.Client
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
	WarmPools  []WarmPoolStatus `json:"warm_pools"`
	Count      int              `json:"count"`
	Offset     int              `json:"offset,omitempty"`
	Limit      int              `json:"limit,omitempty"`
	NextOffset int              `json:"next_offset,omitempty"`
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
	Resources        Capacity  `json:"resources,omitempty"`
	VMMillis         int64     `json:"vm_millis"`
	CPUMillis        int64     `json:"cpu_millis,omitempty"`
	MemoryByteMillis uint64    `json:"memory_byte_millis,omitempty"`
}

type MeteringSummary struct {
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

type AuditEvent = SandboxEvent

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
	Count      int                `json:"count,omitempty"`
	Offset     int                `json:"offset,omitempty"`
	Limit      int                `json:"limit,omitempty"`
	NextOffset int                `json:"next_offset,omitempty"`
}

type WaitResult struct {
	Done         bool          `json:"done"`
	TargetStatus string        `json:"target_status,omitempty"`
	Sandbox      SandboxStatus `json:"sandbox"`
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
	if opts.MaxActiveSandboxes < 0 {
		return nil, errors.New("agentsandbox: max active sandboxes must be non-negative")
	}
	if opts.Priority < 0 {
		return nil, errors.New("agentsandbox: priority must be non-negative")
	}
	if opts.QueueTTL < 0 {
		return nil, errors.New("agentsandbox: queue ttl must not be negative")
	}
	if opts.RunTimeout < 0 {
		return nil, errors.New("agentsandbox: run timeout must not be negative")
	}
	if opts.MaxAttempts < 0 {
		return nil, errors.New("agentsandbox: max attempts must be non-negative")
	}
	if opts.RetryDelay < 0 {
		return nil, errors.New("agentsandbox: retry delay must not be negative")
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
	if opts.MaxActiveSandboxes > 0 {
		body["max_active_sandboxes"] = opts.MaxActiveSandboxes
	}
	if opts.Priority > 0 {
		body["priority"] = opts.Priority
	}
	if opts.QueueTTL > 0 {
		body["queue_ttl"] = formatSeconds(opts.QueueTTL)
	}
	if opts.RunTimeout > 0 {
		body["run_timeout"] = formatSeconds(opts.RunTimeout)
	}
	if opts.MaxAttempts > 0 {
		body["max_attempts"] = opts.MaxAttempts
	}
	if opts.RetryDelay > 0 {
		body["retry_delay"] = formatSeconds(opts.RetryDelay)
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

func ListPlacementPlans(ctx context.Context, opts PlacementPlanListOptions) (PlacementPlanListResult, error) {
	if opts.Limit < 0 {
		return PlacementPlanListResult{}, errors.New("agentsandbox: placement plan limit must be non-negative")
	}
	if opts.Offset < 0 {
		return PlacementPlanListResult{}, errors.New("agentsandbox: placement plan offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "placement-plans")
	if err != nil {
		return PlacementPlanListResult{}, err
	}
	query := map[string]string{
		"namespace":             opts.Namespace,
		"policy":                opts.Policy,
		"image_ref":             opts.ImageRef,
		"image_manifest_digest": opts.ImageManifestDigest,
		"image_digest_ref":      opts.ImageDigestRef,
		"image_platform":        opts.ImagePlatform,
		"required_capability":   opts.RequiredCapability,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result PlacementPlanListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/placements/plans", query), nil, &result, c.timeout); err != nil {
		return PlacementPlanListResult{}, err
	}
	if result.Count == 0 && len(result.Plans) > 0 {
		result.Count = len(result.Plans)
	}
	return result, nil
}

func GetPlacementPlan(ctx context.Context, opts PlacementPlanGetOptions) (PlacementPlan, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return PlacementPlan{}, errors.New("agentsandbox: placement plan id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "placement-plan")
	if err != nil {
		return PlacementPlan{}, err
	}
	var result PlacementPlan
	if err := c.request(ctx, http.MethodGet, placementPlanPath(id), nil, &result, c.timeout); err != nil {
		return PlacementPlan{}, err
	}
	return result, nil
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
	if opts.DryRun {
		body["dry_run"] = true
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
		"image_digest_ref":      opts.ImageDigestRef,
		"image_platform":        opts.ImagePlatform,
		"required_capability":   opts.RequiredCapability,
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

func PushImageGC(ctx context.Context, opts ImageGCOptions) (ImageGCResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "image-gc")
	if err != nil {
		return ImageGCResult{}, err
	}
	body := selectorBody(opts.Namespace, opts.RequiredLabels, opts.RequiredCapabilities)
	if olderThan := strings.TrimSpace(opts.OlderThan); olderThan != "" {
		body["older_than"] = olderThan
	}
	if opts.Apply {
		body["apply"] = true
	}
	if opts.DryRun {
		body["dry_run"] = true
	}
	var result ImageGCResult
	if err := c.request(ctx, http.MethodPost, "/v1/images/gc", body, &result, c.timeout); err != nil {
		return ImageGCResult{}, err
	}
	return result, nil
}

func ListImageGCRuns(ctx context.Context, opts ImageGCListOptions) (ImageGCListResult, error) {
	if opts.Limit < 0 {
		return ImageGCListResult{}, errors.New("agentsandbox: image gc run limit must be non-negative")
	}
	if opts.Offset < 0 {
		return ImageGCListResult{}, errors.New("agentsandbox: image gc run offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "image-gc-runs")
	if err != nil {
		return ImageGCListResult{}, err
	}
	query := map[string]string{
		"namespace":  opts.Namespace,
		"older_than": opts.OlderThan,
	}
	if opts.Apply != nil {
		query["apply"] = strconv.FormatBool(*opts.Apply)
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result ImageGCListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/images/gc/runs", query), nil, &result, c.timeout); err != nil {
		return ImageGCListResult{}, err
	}
	if result.Count == 0 && len(result.Runs) > 0 {
		result.Count = len(result.Runs)
	}
	return result, nil
}

func GetImageGCRun(ctx context.Context, opts ImageGCGetOptions) (ImageGCResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return ImageGCResult{}, errors.New("agentsandbox: image gc run id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "image-gc-run")
	if err != nil {
		return ImageGCResult{}, err
	}
	var result ImageGCResult
	if err := c.request(ctx, http.MethodGet, imageGCRunPath(id), nil, &result, c.timeout); err != nil {
		return ImageGCResult{}, err
	}
	return result, nil
}

func PushLifecyclePolicy(ctx context.Context, opts LifecyclePolicyOptions) (LifecyclePolicyResult, error) {
	vmName := strings.TrimSpace(opts.VMName)
	if vmName == "" {
		return LifecyclePolicyResult{}, errors.New("agentsandbox: lifecycle policy vm name required")
	}
	if opts.RunBudget < 0 {
		return LifecyclePolicyResult{}, errors.New("agentsandbox: lifecycle policy run budget must be non-negative")
	}
	if opts.Clear && (strings.TrimSpace(opts.IdleTimeout) != "" || strings.TrimSpace(opts.MaxAge) != "" || opts.RunBudget != 0) {
		return LifecyclePolicyResult{}, errors.New("agentsandbox: lifecycle policy clear cannot include thresholds")
	}
	if !opts.Clear && strings.TrimSpace(opts.IdleTimeout) == "" && strings.TrimSpace(opts.MaxAge) == "" && opts.RunBudget == 0 {
		return LifecyclePolicyResult{}, errors.New("agentsandbox: lifecycle policy threshold required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "lifecycle-policy")
	if err != nil {
		return LifecyclePolicyResult{}, err
	}
	body := selectorBody(opts.Namespace, opts.RequiredLabels, opts.RequiredCapabilities)
	body["vm_name"] = vmName
	if opts.Clear {
		body["clear"] = true
	}
	if idle := strings.TrimSpace(opts.IdleTimeout); idle != "" {
		body["idle_timeout"] = idle
	}
	if maxAge := strings.TrimSpace(opts.MaxAge); maxAge != "" {
		body["max_age"] = maxAge
	}
	if opts.RunBudget > 0 {
		body["run_budget"] = opts.RunBudget
	}
	if opts.DryRun {
		body["dry_run"] = true
	}
	var result LifecyclePolicyResult
	if err := c.request(ctx, http.MethodPost, "/v1/policies/lifecycle", body, &result, c.timeout); err != nil {
		return LifecyclePolicyResult{}, err
	}
	return result, nil
}

func ListLifecyclePolicyRuns(ctx context.Context, opts LifecyclePolicyListOptions) (LifecyclePolicyListResult, error) {
	if opts.Limit < 0 {
		return LifecyclePolicyListResult{}, errors.New("agentsandbox: lifecycle policy run limit must be non-negative")
	}
	if opts.Offset < 0 {
		return LifecyclePolicyListResult{}, errors.New("agentsandbox: lifecycle policy run offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "lifecycle-policy-runs")
	if err != nil {
		return LifecyclePolicyListResult{}, err
	}
	query := map[string]string{
		"namespace": opts.Namespace,
		"vm_name":   opts.VMName,
	}
	if opts.Clear != nil {
		query["clear"] = strconv.FormatBool(*opts.Clear)
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result LifecyclePolicyListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/policies/lifecycle/runs", query), nil, &result, c.timeout); err != nil {
		return LifecyclePolicyListResult{}, err
	}
	if result.Count == 0 && len(result.Runs) > 0 {
		result.Count = len(result.Runs)
	}
	return result, nil
}

func GetLifecyclePolicyRun(ctx context.Context, opts LifecyclePolicyGetOptions) (LifecyclePolicyResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return LifecyclePolicyResult{}, errors.New("agentsandbox: lifecycle policy run id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "lifecycle-policy-run")
	if err != nil {
		return LifecyclePolicyResult{}, err
	}
	var result LifecyclePolicyResult
	if err := c.request(ctx, http.MethodGet, lifecyclePolicyRunPath(id), nil, &result, c.timeout); err != nil {
		return LifecyclePolicyResult{}, err
	}
	return result, nil
}

func PushStorageBudget(ctx context.Context, opts StorageBudgetOptions) (StorageBudgetResult, error) {
	target := strings.TrimSpace(opts.Target)
	if opts.Clear && (target != "" || opts.WarnPct != nil || opts.HardPct != nil) {
		return StorageBudgetResult{}, errors.New("agentsandbox: storage budget clear cannot include thresholds")
	}
	if !opts.Clear && target == "" {
		return StorageBudgetResult{}, errors.New("agentsandbox: storage budget target required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "storage-budget")
	if err != nil {
		return StorageBudgetResult{}, err
	}
	body := selectorBody(opts.Namespace, opts.RequiredLabels, opts.RequiredCapabilities)
	if opts.Clear {
		body["clear"] = true
	}
	if target != "" {
		body["target"] = target
	}
	if opts.WarnPct != nil {
		body["warn_pct"] = *opts.WarnPct
	}
	if opts.HardPct != nil {
		body["hard_pct"] = *opts.HardPct
	}
	if opts.DryRun {
		body["dry_run"] = true
	}
	var result StorageBudgetResult
	if err := c.request(ctx, http.MethodPost, "/v1/storage/budget", body, &result, c.timeout); err != nil {
		return StorageBudgetResult{}, err
	}
	return result, nil
}

func ListStorageBudgetRuns(ctx context.Context, opts StorageBudgetListOptions) (StorageBudgetListResult, error) {
	if opts.Limit < 0 {
		return StorageBudgetListResult{}, errors.New("agentsandbox: storage budget run limit must be non-negative")
	}
	if opts.Offset < 0 {
		return StorageBudgetListResult{}, errors.New("agentsandbox: storage budget run offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "storage-budget-runs")
	if err != nil {
		return StorageBudgetListResult{}, err
	}
	query := map[string]string{
		"namespace": opts.Namespace,
		"target":    opts.Target,
	}
	if opts.Clear != nil {
		query["clear"] = strconv.FormatBool(*opts.Clear)
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result StorageBudgetListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/storage/budget/runs", query), nil, &result, c.timeout); err != nil {
		return StorageBudgetListResult{}, err
	}
	if result.Count == 0 && len(result.Runs) > 0 {
		result.Count = len(result.Runs)
	}
	return result, nil
}

func GetStorageBudgetRun(ctx context.Context, opts StorageBudgetGetOptions) (StorageBudgetResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return StorageBudgetResult{}, errors.New("agentsandbox: storage budget run id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "storage-budget-run")
	if err != nil {
		return StorageBudgetResult{}, err
	}
	var result StorageBudgetResult
	if err := c.request(ctx, http.MethodGet, storageBudgetRunPath(id), nil, &result, c.timeout); err != nil {
		return StorageBudgetResult{}, err
	}
	return result, nil
}

func PushStoragePrune(ctx context.Context, opts StoragePruneOptions) (StoragePruneResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "storage-prune")
	if err != nil {
		return StoragePruneResult{}, err
	}
	body := selectorBody(opts.Namespace, opts.RequiredLabels, opts.RequiredCapabilities)
	if category := strings.TrimSpace(opts.Category); category != "" {
		body["category"] = category
	}
	if olderThan := strings.TrimSpace(opts.OlderThan); olderThan != "" {
		body["older_than"] = olderThan
	}
	if opts.Apply {
		body["apply"] = true
	}
	if opts.DryRun {
		body["dry_run"] = true
	}
	var result StoragePruneResult
	if err := c.request(ctx, http.MethodPost, "/v1/storage/prune", body, &result, c.timeout); err != nil {
		return StoragePruneResult{}, err
	}
	return result, nil
}

func ListStoragePruneRuns(ctx context.Context, opts StoragePruneListOptions) (StoragePruneListResult, error) {
	if opts.Limit < 0 {
		return StoragePruneListResult{}, errors.New("agentsandbox: storage prune run limit must be non-negative")
	}
	if opts.Offset < 0 {
		return StoragePruneListResult{}, errors.New("agentsandbox: storage prune run offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "storage-prune-runs")
	if err != nil {
		return StoragePruneListResult{}, err
	}
	query := map[string]string{
		"namespace":  opts.Namespace,
		"category":   opts.Category,
		"older_than": opts.OlderThan,
	}
	if opts.Apply != nil {
		query["apply"] = strconv.FormatBool(*opts.Apply)
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result StoragePruneListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/storage/prune/runs", query), nil, &result, c.timeout); err != nil {
		return StoragePruneListResult{}, err
	}
	if result.Count == 0 && len(result.Runs) > 0 {
		result.Count = len(result.Runs)
	}
	return result, nil
}

func GetStoragePruneRun(ctx context.Context, opts StoragePruneGetOptions) (StoragePruneResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return StoragePruneResult{}, errors.New("agentsandbox: storage prune run id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "storage-prune-run")
	if err != nil {
		return StoragePruneResult{}, err
	}
	var result StoragePruneResult
	if err := c.request(ctx, http.MethodGet, storagePruneRunPath(id), nil, &result, c.timeout); err != nil {
		return StoragePruneResult{}, err
	}
	return result, nil
}

func ListControllerRuns(ctx context.Context, opts ControllerRunListOptions) (ControllerRunListResult, error) {
	if opts.Limit < 0 {
		return ControllerRunListResult{}, errors.New("agentsandbox: controller run limit must be non-negative")
	}
	if opts.Offset < 0 {
		return ControllerRunListResult{}, errors.New("agentsandbox: controller run offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "controller-runs")
	if err != nil {
		return ControllerRunListResult{}, err
	}
	query := map[string]string{
		"namespace":             opts.Namespace,
		"kind":                  opts.Kind,
		"target_type":           opts.TargetType,
		"target_id":             opts.TargetID,
		"source_ref":            opts.SourceRef,
		"image_ref":             opts.ImageRef,
		"image_manifest_digest": opts.ImageManifestDigest,
		"image_digest_ref":      opts.ImageDigestRef,
		"image_platform":        opts.ImagePlatform,
		"required_capability":   opts.RequiredCapability,
		"assignment_id":         opts.AssignmentID,
		"worker_id":             opts.WorkerID,
		"candidate_worker_id":   opts.CandidateWorkerID,
		"skipped_worker_id":     opts.SkippedWorkerID,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result ControllerRunListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/operations/runs", query), nil, &result, c.timeout); err != nil {
		return ControllerRunListResult{}, err
	}
	if result.Count == 0 && len(result.Runs) > 0 {
		result.Count = len(result.Runs)
	}
	return result, nil
}

func GetControllerRun(ctx context.Context, opts ControllerRunGetOptions) (ControllerRunDetail, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return ControllerRunDetail{}, errors.New("agentsandbox: controller run id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "controller-run")
	if err != nil {
		return ControllerRunDetail{}, err
	}
	var result ControllerRunDetail
	if err := c.request(ctx, http.MethodGet, controllerRunPath(id), nil, &result, c.timeout); err != nil {
		return ControllerRunDetail{}, err
	}
	return result, nil
}

func PlanReconcile(ctx context.Context, opts ReconcileOptions) (ReconcileResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "reconcile-plan")
	if err != nil {
		return ReconcileResult{}, err
	}
	var result ReconcileResult
	if err := c.request(ctx, http.MethodGet, "/v1/reconcile/plan", nil, &result, c.timeout); err != nil {
		return ReconcileResult{}, err
	}
	return result, nil
}

func Reconcile(ctx context.Context, opts ReconcileOptions) (ReconcileResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "reconcile")
	if err != nil {
		return ReconcileResult{}, err
	}
	var result ReconcileResult
	if err := c.request(ctx, http.MethodPost, "/v1/reconcile", map[string]any{}, &result, c.timeout); err != nil {
		return ReconcileResult{}, err
	}
	return result, nil
}

func GetOperationsSummary(ctx context.Context, opts OperationsSummaryOptions) (OperationsSummary, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "operations-summary")
	if err != nil {
		return OperationsSummary{}, err
	}
	query := map[string]string{"namespace": opts.Namespace}
	var result OperationsSummary
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/operations/summary", query), nil, &result, c.timeout); err != nil {
		return OperationsSummary{}, err
	}
	return result, nil
}

func ListSandboxMetering(ctx context.Context, opts SandboxMeteringOptions) (MeteringResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "sandbox-metering")
	if err != nil {
		return MeteringResult{}, err
	}
	query := map[string]string{
		"namespace":  opts.Namespace,
		"sandbox_id": opts.SandboxID,
	}
	var result MeteringResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/metering/sandboxes", query), nil, &result, c.timeout); err != nil {
		return MeteringResult{}, err
	}
	return result, nil
}

func ListAuditEvents(ctx context.Context, opts AuditListOptions) (AuditListResult, error) {
	if opts.Limit < 0 {
		return AuditListResult{}, errors.New("agentsandbox: audit limit must be non-negative")
	}
	if opts.Offset < 0 {
		return AuditListResult{}, errors.New("agentsandbox: audit offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "audit")
	if err != nil {
		return AuditListResult{}, err
	}
	query := map[string]string{
		"namespace":     opts.Namespace,
		"actor":         opts.Actor,
		"action":        opts.Action,
		"target_type":   opts.TargetType,
		"target_id":     opts.TargetID,
		"worker_id":     opts.WorkerID,
		"assignment_id": opts.AssignmentID,
		"sandbox_id":    opts.SandboxID,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result AuditListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/audit", query), nil, &result, c.timeout); err != nil {
		return AuditListResult{}, err
	}
	if result.Count == 0 && len(result.Events) > 0 {
		result.Count = len(result.Events)
	}
	return result, nil
}

func VerifyAuditLog(ctx context.Context, opts AuditVerifyOptions) (AuditVerifyResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "audit-verify")
	if err != nil {
		return AuditVerifyResult{}, err
	}
	var result AuditVerifyResult
	if err := c.request(ctx, http.MethodGet, "/v1/audit/verify", nil, &result, c.timeout); err != nil {
		return AuditVerifyResult{}, err
	}
	return result, nil
}

func ListServiceAccounts(ctx context.Context, opts ServiceAccountListOptions) (ServiceAccountListResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "service-accounts")
	if err != nil {
		return ServiceAccountListResult{}, err
	}
	var result ServiceAccountListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/service-accounts", map[string]string{"namespace": opts.Namespace}), nil, &result, c.timeout); err != nil {
		return ServiceAccountListResult{}, err
	}
	if result.Count == 0 && len(result.ServiceAccounts) > 0 {
		result.Count = len(result.ServiceAccounts)
	}
	return result, nil
}

func UpsertServiceAccount(ctx context.Context, opts ServiceAccountUpsertOptions) (ServiceAccountResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return ServiceAccountResult{}, errors.New("agentsandbox: service account name required")
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return ServiceAccountResult{}, errors.New("agentsandbox: service account token required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "service-account")
	if err != nil {
		return ServiceAccountResult{}, err
	}
	body := map[string]any{"name": name, "token": token}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if role := strings.TrimSpace(opts.Role); role != "" {
		body["role"] = role
	}
	var result ServiceAccountResult
	if err := c.request(ctx, http.MethodPost, "/v1/service-accounts", body, &result, c.timeout); err != nil {
		return ServiceAccountResult{}, err
	}
	return result, nil
}

func DeleteServiceAccount(ctx context.Context, opts ServiceAccountDeleteOptions) (ServiceAccountResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return ServiceAccountResult{}, errors.New("agentsandbox: service account name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "service-account")
	if err != nil {
		return ServiceAccountResult{}, err
	}
	var result ServiceAccountResult
	if err := c.request(ctx, http.MethodDelete, serviceAccountPath(name), nil, &result, c.timeout); err != nil {
		return ServiceAccountResult{}, err
	}
	return result, nil
}

func ListOIDCBindings(ctx context.Context, opts OIDCBindingListOptions) (OIDCBindingListResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "oidc-bindings")
	if err != nil {
		return OIDCBindingListResult{}, err
	}
	var result OIDCBindingListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/oidc-bindings", map[string]string{"namespace": opts.Namespace}), nil, &result, c.timeout); err != nil {
		return OIDCBindingListResult{}, err
	}
	if result.Count == 0 && len(result.Bindings) > 0 {
		result.Count = len(result.Bindings)
	}
	return result, nil
}

func UpsertOIDCBinding(ctx context.Context, opts OIDCBindingUpsertOptions) (OIDCBindingResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return OIDCBindingResult{}, errors.New("agentsandbox: oidc binding name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "oidc-binding")
	if err != nil {
		return OIDCBindingResult{}, err
	}
	body := map[string]any{"name": name}
	if issuer := strings.TrimSpace(opts.Issuer); issuer != "" {
		body["issuer"] = issuer
	}
	if subject := strings.TrimSpace(opts.Subject); subject != "" {
		body["subject"] = subject
	}
	if audience := strings.TrimSpace(opts.Audience); audience != "" {
		body["audience"] = audience
	}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if role := strings.TrimSpace(opts.Role); role != "" {
		body["role"] = role
	}
	if jwksURL := strings.TrimSpace(opts.JWKSURL); jwksURL != "" {
		body["jwks_url"] = jwksURL
	}
	if len(opts.Keys) > 0 {
		body["keys"] = opts.Keys
	}
	var result OIDCBindingResult
	if err := c.request(ctx, http.MethodPost, "/v1/oidc-bindings", body, &result, c.timeout); err != nil {
		return OIDCBindingResult{}, err
	}
	return result, nil
}

func DeleteOIDCBinding(ctx context.Context, opts OIDCBindingDeleteOptions) (OIDCBindingResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return OIDCBindingResult{}, errors.New("agentsandbox: oidc binding name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "oidc-binding")
	if err != nil {
		return OIDCBindingResult{}, err
	}
	var result OIDCBindingResult
	if err := c.request(ctx, http.MethodDelete, oidcBindingPath(name), nil, &result, c.timeout); err != nil {
		return OIDCBindingResult{}, err
	}
	return result, nil
}

func ListSAMLBindings(ctx context.Context, opts SAMLBindingListOptions) (SAMLBindingListResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "saml-bindings")
	if err != nil {
		return SAMLBindingListResult{}, err
	}
	var result SAMLBindingListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/saml-bindings", map[string]string{"namespace": opts.Namespace}), nil, &result, c.timeout); err != nil {
		return SAMLBindingListResult{}, err
	}
	if result.Count == 0 && len(result.Bindings) > 0 {
		result.Count = len(result.Bindings)
	}
	return result, nil
}

func UpsertSAMLBinding(ctx context.Context, opts SAMLBindingUpsertOptions) (SAMLBindingResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return SAMLBindingResult{}, errors.New("agentsandbox: saml binding name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "saml-binding")
	if err != nil {
		return SAMLBindingResult{}, err
	}
	body := map[string]any{"name": name}
	if entityID := strings.TrimSpace(opts.EntityID); entityID != "" {
		body["entity_id"] = entityID
	}
	if subject := strings.TrimSpace(opts.Subject); subject != "" {
		body["subject"] = subject
	}
	if ssoURL := strings.TrimSpace(opts.SSOURL); ssoURL != "" {
		body["sso_url"] = ssoURL
	}
	if audience := strings.TrimSpace(opts.Audience); audience != "" {
		body["audience"] = audience
	}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if role := strings.TrimSpace(opts.Role); role != "" {
		body["role"] = role
	}
	if certificate := strings.TrimSpace(opts.CertificatePEM); certificate != "" {
		body["certificate_pem"] = certificate
	}
	if metadataURL := strings.TrimSpace(opts.MetadataURL); metadataURL != "" {
		body["metadata_url"] = metadataURL
	}
	if metadataXML := strings.TrimSpace(opts.MetadataXML); metadataXML != "" {
		body["metadata_xml"] = metadataXML
	}
	var result SAMLBindingResult
	if err := c.request(ctx, http.MethodPost, "/v1/saml-bindings", body, &result, c.timeout); err != nil {
		return SAMLBindingResult{}, err
	}
	return result, nil
}

func DeleteSAMLBinding(ctx context.Context, opts SAMLBindingNameOptions) (SAMLBindingResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return SAMLBindingResult{}, errors.New("agentsandbox: saml binding name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "saml-binding")
	if err != nil {
		return SAMLBindingResult{}, err
	}
	var result SAMLBindingResult
	if err := c.request(ctx, http.MethodDelete, samlBindingPath(name), nil, &result, c.timeout); err != nil {
		return SAMLBindingResult{}, err
	}
	return result, nil
}

func RefreshSAMLBinding(ctx context.Context, opts SAMLBindingNameOptions) (SAMLBindingResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return SAMLBindingResult{}, errors.New("agentsandbox: saml binding name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "saml-binding-refresh")
	if err != nil {
		return SAMLBindingResult{}, err
	}
	var result SAMLBindingResult
	if err := c.request(ctx, http.MethodPost, samlBindingActionPath(name, "refresh"), map[string]any{}, &result, c.timeout); err != nil {
		return SAMLBindingResult{}, err
	}
	return result, nil
}

func SAMLBindingLogin(ctx context.Context, opts SAMLBindingLoginOptions) (SAMLAuthnRequestResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return SAMLAuthnRequestResult{}, errors.New("agentsandbox: saml binding name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "saml-binding-login")
	if err != nil {
		return SAMLAuthnRequestResult{}, err
	}
	query := map[string]string{"relay_state": opts.RelayState}
	var result SAMLAuthnRequestResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(samlBindingActionPath(name, "login"), query), nil, &result, c.timeout); err != nil {
		return SAMLAuthnRequestResult{}, err
	}
	return result, nil
}

func GetSAMLMetadata(ctx context.Context, opts SAMLBindingNameOptions) ([]byte, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return nil, errors.New("agentsandbox: saml binding name required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "saml-metadata")
	if err != nil {
		return nil, err
	}
	data, err := c.requestBytes(ctx, http.MethodGet, samlBindingActionPath(name, "metadata"), c.timeout)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func CreateSAMLSession(ctx context.Context, opts SAMLSessionOptions) (SAMLSessionResult, error) {
	response := strings.TrimSpace(opts.SAMLResponse)
	assertion := strings.TrimSpace(opts.SAMLAssertion)
	if response == "" && assertion == "" {
		return SAMLSessionResult{}, errors.New("agentsandbox: saml response or assertion required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "saml-session")
	if err != nil {
		return SAMLSessionResult{}, err
	}
	body := map[string]any{}
	if response != "" {
		body["saml_response"] = response
	}
	if assertion != "" {
		body["saml_assertion"] = assertion
	}
	if relayState := strings.TrimSpace(opts.RelayState); relayState != "" {
		body["relay_state"] = relayState
	}
	if ttl := strings.TrimSpace(opts.TTL); ttl != "" {
		body["ttl"] = ttl
	}
	var result SAMLSessionResult
	if err := c.request(ctx, http.MethodPost, "/v1/saml/acs", body, &result, c.timeout); err != nil {
		return SAMLSessionResult{}, err
	}
	return result, nil
}

func ListWorkers(ctx context.Context, opts WorkerListOptions) (WorkerListResult, error) {
	if opts.Limit < 0 {
		return WorkerListResult{}, errors.New("agentsandbox: worker limit must be non-negative")
	}
	if opts.Offset < 0 {
		return WorkerListResult{}, errors.New("agentsandbox: worker offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "workers")
	if err != nil {
		return WorkerListResult{}, err
	}
	query := url.Values{}
	setQuery(query, "status", opts.Status)
	setQuery(query, "host", opts.Host)
	setQuery(query, "version", opts.Version)
	setQuery(query, "image_ref", opts.ImageRef)
	setQuery(query, "source_manifest_digest", opts.SourceManifestDigest)
	if opts.Offset > 0 {
		query.Set("offset", strconv.Itoa(opts.Offset))
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	labels := cleanStringMap(opts.Labels)
	labelKeys := make([]string, 0, len(labels))
	for key := range labels {
		labelKeys = append(labelKeys, key)
	}
	sort.Strings(labelKeys)
	for _, key := range labelKeys {
		query.Add("label", key+"="+labels[key])
	}
	for _, capability := range cleanStrings(opts.Capabilities) {
		query.Add("capability", capability)
	}
	var result WorkerListResult
	if err := c.request(ctx, http.MethodGet, queryPathValues("/v1/workers", query), nil, &result, c.timeout); err != nil {
		return WorkerListResult{}, err
	}
	if result.Count == 0 && len(result.Workers) > 0 {
		result.Count = len(result.Workers)
	}
	return result, nil
}

func GetWorker(ctx context.Context, opts WorkerGetOptions) (HostRecord, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return HostRecord{}, errors.New("agentsandbox: worker id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "worker")
	if err != nil {
		return HostRecord{}, err
	}
	var result HostRecord
	if err := c.request(ctx, http.MethodGet, workerPath(id), nil, &result, c.timeout); err != nil {
		return HostRecord{}, err
	}
	return result, nil
}

func ListWorkerEvents(ctx context.Context, opts WorkerEventListOptions) (AuditListResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return AuditListResult{}, errors.New("agentsandbox: worker id required")
	}
	if opts.Limit < 0 {
		return AuditListResult{}, errors.New("agentsandbox: worker events limit must be non-negative")
	}
	if opts.Offset < 0 {
		return AuditListResult{}, errors.New("agentsandbox: worker events offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "worker-events")
	if err != nil {
		return AuditListResult{}, err
	}
	query := map[string]string{
		"actor":       opts.Actor,
		"action":      opts.Action,
		"target_type": opts.TargetType,
		"target_id":   opts.TargetID,
		"sandbox_id":  opts.SandboxID,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result AuditListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(workerActionPath(id, "events"), query), nil, &result, c.timeout); err != nil {
		return AuditListResult{}, err
	}
	if result.Count == 0 && len(result.Events) > 0 {
		result.Count = len(result.Events)
	}
	return result, nil
}

func ListWorkerReports(ctx context.Context, opts WorkerReportListOptions) (AssignmentReportListResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return AssignmentReportListResult{}, errors.New("agentsandbox: worker id required")
	}
	if opts.Limit < 0 {
		return AssignmentReportListResult{}, errors.New("agentsandbox: worker reports limit must be non-negative")
	}
	if opts.Offset < 0 {
		return AssignmentReportListResult{}, errors.New("agentsandbox: worker reports offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "worker-reports")
	if err != nil {
		return AssignmentReportListResult{}, err
	}
	query := map[string]string{
		"assignment_id": opts.AssignmentID,
		"status":        opts.Status,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result AssignmentReportListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(workerActionPath(id, "reports"), query), nil, &result, c.timeout); err != nil {
		return AssignmentReportListResult{}, err
	}
	if result.Count == 0 && len(result.Reports) > 0 {
		result.Count = len(result.Reports)
	}
	return result, nil
}

func GetWorkerMetering(ctx context.Context, opts WorkerMeteringOptions) (MeteringResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return MeteringResult{}, errors.New("agentsandbox: worker id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "worker-metering")
	if err != nil {
		return MeteringResult{}, err
	}
	query := map[string]string{
		"namespace":  opts.Namespace,
		"sandbox_id": opts.SandboxID,
		"status":     opts.Status,
	}
	var result MeteringResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(workerActionPath(id, "metering"), query), nil, &result, c.timeout); err != nil {
		return MeteringResult{}, err
	}
	return result, nil
}

func ListWorkerSandboxes(ctx context.Context, opts WorkerSandboxListOptions) (SandboxListResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return SandboxListResult{}, errors.New("agentsandbox: worker id required")
	}
	if opts.Limit < 0 {
		return SandboxListResult{}, errors.New("agentsandbox: worker sandboxes limit must be non-negative")
	}
	if opts.Offset < 0 {
		return SandboxListResult{}, errors.New("agentsandbox: worker sandboxes offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "worker-sandboxes")
	if err != nil {
		return SandboxListResult{}, err
	}
	query := map[string]string{
		"namespace": opts.Namespace,
		"status":    opts.Status,
		"image_ref": opts.ImageRef,
	}
	if digest := strings.TrimSpace(opts.ImageManifestDigest); digest != "" {
		query["image_manifest_digest"] = digest
	}
	if ref := strings.TrimSpace(opts.ImageDigestRef); ref != "" {
		query["image_digest_ref"] = ref
	}
	if platform := strings.TrimSpace(opts.ImagePlatform); platform != "" {
		query["image_platform"] = platform
	}
	if capability := strings.TrimSpace(opts.RequiredCapability); capability != "" {
		query["required_capability"] = capability
	}
	if opts.HasOpenAssignments != nil {
		query["has_open_assignments"] = strconv.FormatBool(*opts.HasOpenAssignments)
	}
	if opts.Retrying != nil {
		query["retrying"] = strconv.FormatBool(*opts.Retrying)
	}
	if opts.HasCleanup != nil {
		query["has_cleanup"] = strconv.FormatBool(*opts.HasCleanup)
	}
	if opts.HasLease != nil {
		query["has_lease"] = strconv.FormatBool(*opts.HasLease)
	}
	if holder := strings.TrimSpace(opts.LeaseHolder); holder != "" {
		query["lease_holder"] = holder
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result SandboxListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(workerActionPath(id, "sandboxes"), query), nil, &result, c.timeout); err != nil {
		return SandboxListResult{}, err
	}
	if result.Count == 0 && len(result.Sandboxes) > 0 {
		result.Count = len(result.Sandboxes)
	}
	return result, nil
}

func CordonWorker(ctx context.Context, opts WorkerLifecycleOptions) (HostRecord, error) {
	return workerLifecycle(ctx, opts, "cordon")
}

func UncordonWorker(ctx context.Context, opts WorkerLifecycleOptions) (HostRecord, error) {
	return workerLifecycle(ctx, opts, "uncordon")
}

func QuarantineWorker(ctx context.Context, opts WorkerLifecycleOptions) (HostRecord, error) {
	return workerLifecycle(ctx, opts, "quarantine")
}

func UnquarantineWorker(ctx context.Context, opts WorkerLifecycleOptions) (HostRecord, error) {
	return workerLifecycle(ctx, opts, "unquarantine")
}

func DrainWorker(ctx context.Context, opts WorkerLifecycleOptions) (WorkerDrainResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return WorkerDrainResult{}, errors.New("agentsandbox: worker id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "worker-drain")
	if err != nil {
		return WorkerDrainResult{}, err
	}
	var result WorkerDrainResult
	if err := c.request(ctx, http.MethodPost, workerActionPath(id, "drain"), workerLifecycleBody(opts.Reason, false), &result, c.timeout); err != nil {
		return WorkerDrainResult{}, err
	}
	return result, nil
}

func EvacuateWorker(ctx context.Context, opts WorkerEvacuationOptions) (WorkerEvacuationResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return WorkerEvacuationResult{}, errors.New("agentsandbox: worker id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "worker-evacuate")
	if err != nil {
		return WorkerEvacuationResult{}, err
	}
	body := map[string]any{}
	if reason := strings.TrimSpace(opts.Reason); reason != "" {
		body["reason"] = reason
	}
	if opts.Apply {
		body["apply"] = true
	}
	if opts.Force {
		body["force"] = true
	}
	var result WorkerEvacuationResult
	if err := c.request(ctx, http.MethodPost, workerActionPath(id, "evacuate"), body, &result, c.timeout); err != nil {
		return WorkerEvacuationResult{}, err
	}
	return result, nil
}

func DecommissionWorker(ctx context.Context, opts WorkerLifecycleOptions) (WorkerDecommissionResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return WorkerDecommissionResult{}, errors.New("agentsandbox: worker id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "worker-decommission")
	if err != nil {
		return WorkerDecommissionResult{}, err
	}
	var result WorkerDecommissionResult
	if err := c.request(ctx, http.MethodPost, workerActionPath(id, "decommission"), workerLifecycleBody(opts.Reason, opts.Force), &result, c.timeout); err != nil {
		return WorkerDecommissionResult{}, err
	}
	return result, nil
}

func CreateAssignment(ctx context.Context, opts AssignmentCreateOptions) (Assignment, error) {
	verb := strings.TrimSpace(opts.Verb)
	if verb == "" {
		return Assignment{}, errors.New("agentsandbox: assignment verb required")
	}
	if opts.Priority < 0 {
		return Assignment{}, errors.New("agentsandbox: assignment priority must be non-negative")
	}
	if opts.QueueTTL < 0 {
		return Assignment{}, errors.New("agentsandbox: assignment queue ttl must not be negative")
	}
	if opts.RunTimeout < 0 {
		return Assignment{}, errors.New("agentsandbox: assignment run timeout must not be negative")
	}
	if opts.MaxAttempts < 0 {
		return Assignment{}, errors.New("agentsandbox: assignment max attempts must be non-negative")
	}
	if opts.RetryDelay < 0 {
		return Assignment{}, errors.New("agentsandbox: assignment retry delay must not be negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "assignment-create")
	if err != nil {
		return Assignment{}, err
	}
	body := map[string]any{"verb": verb}
	if id := strings.TrimSpace(opts.ID); id != "" {
		body["id"] = id
	}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		body["namespace"] = ns
	}
	if workerID := strings.TrimSpace(opts.WorkerID); workerID != "" {
		body["worker_id"] = workerID
	}
	if policy := strings.TrimSpace(opts.Policy); policy != "" {
		body["policy"] = policy
	}
	if imageRef := strings.TrimSpace(opts.ImageRef); imageRef != "" {
		body["image_ref"] = imageRef
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
	if antiAffinityKey := strings.TrimSpace(opts.AntiAffinityKey); antiAffinityKey != "" {
		body["anti_affinity_key"] = antiAffinityKey
	}
	if nonzeroCapacity(opts.Resources) {
		body["resources"] = opts.Resources
	}
	if opts.Priority > 0 {
		body["priority"] = opts.Priority
	}
	if opts.QueueTTL > 0 {
		body["queue_ttl"] = formatSeconds(opts.QueueTTL)
	}
	if opts.RunTimeout > 0 {
		body["run_timeout"] = formatSeconds(opts.RunTimeout)
	}
	if opts.MaxAttempts > 0 {
		body["max_attempts"] = opts.MaxAttempts
	}
	if opts.RetryDelay > 0 {
		body["retry_delay"] = formatSeconds(opts.RetryDelay)
	}
	if len(opts.Args) > 0 {
		body["args"] = cloneStrings(opts.Args)
	}
	var assignment Assignment
	if err := c.request(ctx, http.MethodPost, "/v1/assignments", body, &assignment, c.timeout); err != nil {
		return Assignment{}, err
	}
	return assignment, nil
}

func ListAssignments(ctx context.Context, opts AssignmentListOptions) (AssignmentListResult, error) {
	if opts.Limit < 0 {
		return AssignmentListResult{}, errors.New("agentsandbox: assignment limit must be non-negative")
	}
	if opts.Offset < 0 {
		return AssignmentListResult{}, errors.New("agentsandbox: assignment offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "assignments")
	if err != nil {
		return AssignmentListResult{}, err
	}
	query := map[string]string{
		"namespace":             opts.Namespace,
		"status":                opts.Status,
		"worker_id":             opts.WorkerID,
		"leased_to":             opts.LeasedTo,
		"verb":                  opts.Verb,
		"image_ref":             opts.ImageRef,
		"image_manifest_digest": opts.ImageManifestDigest,
		"image_digest_ref":      opts.ImageDigestRef,
		"image_platform":        opts.ImagePlatform,
		"required_capability":   opts.RequiredCapability,
		"sandbox_id":            opts.SandboxID,
		"warm_pool":             opts.WarmPool,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result AssignmentListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/assignments", query), nil, &result, c.timeout); err != nil {
		return AssignmentListResult{}, err
	}
	if result.Count == 0 && len(result.Assignments) > 0 {
		result.Count = len(result.Assignments)
	}
	return result, nil
}

func GetAssignment(ctx context.Context, opts AssignmentGetOptions) (Assignment, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return Assignment{}, errors.New("agentsandbox: assignment id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "assignment")
	if err != nil {
		return Assignment{}, err
	}
	var result Assignment
	if err := c.request(ctx, http.MethodGet, assignmentPath(id), nil, &result, c.timeout); err != nil {
		return Assignment{}, err
	}
	return result, nil
}

func ListAssignmentEvents(ctx context.Context, opts AssignmentEventListOptions) (AuditListResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return AuditListResult{}, errors.New("agentsandbox: assignment id required")
	}
	if opts.Limit < 0 {
		return AuditListResult{}, errors.New("agentsandbox: assignment events limit must be non-negative")
	}
	if opts.Offset < 0 {
		return AuditListResult{}, errors.New("agentsandbox: assignment events offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "assignment-events")
	if err != nil {
		return AuditListResult{}, err
	}
	query := map[string]string{
		"actor":       opts.Actor,
		"action":      opts.Action,
		"target_type": opts.TargetType,
		"target_id":   opts.TargetID,
		"worker_id":   opts.WorkerID,
		"sandbox_id":  opts.SandboxID,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result AuditListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(assignmentActionPath(id, "events"), query), nil, &result, c.timeout); err != nil {
		return AuditListResult{}, err
	}
	if result.Count == 0 && len(result.Events) > 0 {
		result.Count = len(result.Events)
	}
	return result, nil
}

func ListAssignmentReports(ctx context.Context, opts AssignmentReportListOptions) (AssignmentReportListResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return AssignmentReportListResult{}, errors.New("agentsandbox: assignment id required")
	}
	if opts.Limit < 0 {
		return AssignmentReportListResult{}, errors.New("agentsandbox: assignment reports limit must be non-negative")
	}
	if opts.Offset < 0 {
		return AssignmentReportListResult{}, errors.New("agentsandbox: assignment reports offset must be non-negative")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "assignment-reports")
	if err != nil {
		return AssignmentReportListResult{}, err
	}
	query := map[string]string{
		"worker_id": opts.WorkerID,
		"status":    opts.Status,
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	var result AssignmentReportListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(assignmentActionPath(id, "reports"), query), nil, &result, c.timeout); err != nil {
		return AssignmentReportListResult{}, err
	}
	if result.Count == 0 && len(result.Reports) > 0 {
		result.Count = len(result.Reports)
	}
	return result, nil
}

func GetAssignmentMetering(ctx context.Context, opts AssignmentMeteringOptions) (MeteringResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return MeteringResult{}, errors.New("agentsandbox: assignment id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "assignment-metering")
	if err != nil {
		return MeteringResult{}, err
	}
	query := map[string]string{"status": opts.Status}
	var result MeteringResult
	if err := c.request(ctx, http.MethodGet, c.queryPath(assignmentActionPath(id, "metering"), query), nil, &result, c.timeout); err != nil {
		return MeteringResult{}, err
	}
	return result, nil
}

func CancelAssignment(ctx context.Context, opts AssignmentCancelOptions) (AssignmentCancelResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return AssignmentCancelResult{}, errors.New("agentsandbox: assignment id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "assignment-cancel")
	if err != nil {
		return AssignmentCancelResult{}, err
	}
	body := map[string]any{}
	if reason := strings.TrimSpace(opts.Reason); reason != "" {
		body["reason"] = reason
	}
	if opts.Force {
		body["force"] = true
	}
	var result AssignmentCancelResult
	if err := c.request(ctx, http.MethodPost, assignmentActionPath(id, "cancel"), body, &result, c.timeout); err != nil {
		return AssignmentCancelResult{}, err
	}
	return result, nil
}

func RetryAssignment(ctx context.Context, opts AssignmentRetryOptions) (AssignmentRetryResult, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return AssignmentRetryResult{}, errors.New("agentsandbox: assignment id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "assignment-retry")
	if err != nil {
		return AssignmentRetryResult{}, err
	}
	body := map[string]any{}
	if reason := strings.TrimSpace(opts.Reason); reason != "" {
		body["reason"] = reason
	}
	if workerID := strings.TrimSpace(opts.WorkerID); workerID != "" {
		body["worker_id"] = workerID
	}
	if opts.Replan {
		body["replan"] = true
	}
	var result AssignmentRetryResult
	if err := c.request(ctx, http.MethodPost, assignmentActionPath(id, "retry"), body, &result, c.timeout); err != nil {
		return AssignmentRetryResult{}, err
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

func (opts WarmPoolListOptions) query() (map[string]string, error) {
	if opts.Limit < 0 {
		return nil, errors.New("agentsandbox: warm pool list limit must be non-negative")
	}
	if opts.Offset < 0 {
		return nil, errors.New("agentsandbox: warm pool list offset must be non-negative")
	}
	query := map[string]string{
		"namespace": opts.Namespace,
		"image_ref": opts.ImageRef,
	}
	if digest := strings.TrimSpace(opts.ImageManifestDigest); digest != "" {
		query["image_manifest_digest"] = digest
	}
	if ref := strings.TrimSpace(opts.ImageDigestRef); ref != "" {
		query["image_digest_ref"] = ref
	}
	if platform := strings.TrimSpace(opts.ImagePlatform); platform != "" {
		query["image_platform"] = platform
	}
	if capability := strings.TrimSpace(opts.RequiredCapability); capability != "" {
		query["required_capability"] = capability
	}
	if opts.Offset > 0 {
		query["offset"] = strconv.Itoa(opts.Offset)
	}
	if opts.Limit > 0 {
		query["limit"] = strconv.Itoa(opts.Limit)
	}
	return query, nil
}

func ListWarmPools(ctx context.Context, opts WarmPoolListOptions) ([]WarmPoolStatus, error) {
	result, err := ListWarmPoolsPage(ctx, opts)
	if err != nil {
		return nil, err
	}
	return result.WarmPools, nil
}

func ListWarmPoolsPage(ctx context.Context, opts WarmPoolListOptions) (WarmPoolListResult, error) {
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, opts.Namespace, opts.Timeout, opts.HTTP, "warm-pools")
	if err != nil {
		return WarmPoolListResult{}, err
	}
	query, err := opts.query()
	if err != nil {
		return WarmPoolListResult{}, err
	}
	var result WarmPoolListResult
	if err := c.request(ctx, http.MethodGet, c.queryPath("/v1/warm-pools", query), nil, &result, c.timeout); err != nil {
		return WarmPoolListResult{}, err
	}
	return result, nil
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
	if digest := strings.TrimSpace(options.ImageManifestDigest); digest != "" && status.ImageManifestDigest != digest {
		return false
	}
	if ref := strings.TrimSpace(options.ImageDigestRef); ref != "" && status.ImageDigestRef != ref {
		return false
	}
	if platform := strings.TrimSpace(options.ImagePlatform); platform != "" && status.ImagePlatform != platform {
		return false
	}
	if capability := strings.TrimSpace(options.RequiredCapability); capability != "" && !stringSliceContains(status.RequiredCapabilities, capability) {
		return false
	}
	if options.HasOpenAssignments != nil && (len(status.OpenAssignments) > 0) != *options.HasOpenAssignments {
		return false
	}
	if options.Retrying != nil && sandboxStatusRetrying(status) != *options.Retrying {
		return false
	}
	if options.HasCleanup != nil && (status.Cleanup != nil) != *options.HasCleanup {
		return false
	}
	if options.HasLease != nil && (status.Lease != nil) != *options.HasLease {
		return false
	}
	if holder := strings.TrimSpace(options.LeaseHolder); holder != "" && (status.Lease == nil || status.Lease.Holder != holder) {
		return false
	}
	return true
}

func sandboxStatusRetrying(status SandboxStatus) bool {
	return status.Status == "pending" && status.Attempt > 0
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
	if digest := strings.TrimSpace(o.ImageManifestDigest); digest != "" {
		query["image_manifest_digest"] = digest
	}
	if ref := strings.TrimSpace(o.ImageDigestRef); ref != "" {
		query["image_digest_ref"] = ref
	}
	if platform := strings.TrimSpace(o.ImagePlatform); platform != "" {
		query["image_platform"] = platform
	}
	if capability := strings.TrimSpace(o.RequiredCapability); capability != "" {
		query["required_capability"] = capability
	}
	if o.HasOpenAssignments != nil {
		query["has_open_assignments"] = strconv.FormatBool(*o.HasOpenAssignments)
	}
	if o.Retrying != nil {
		query["retrying"] = strconv.FormatBool(*o.Retrying)
	}
	if o.HasCleanup != nil {
		query["has_cleanup"] = strconv.FormatBool(*o.HasCleanup)
	}
	if o.HasLease != nil {
		query["has_lease"] = strconv.FormatBool(*o.HasLease)
	}
	if holder := strings.TrimSpace(o.LeaseHolder); holder != "" {
		query["lease_holder"] = holder
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
	return c.WaitStatus(ctx, "", timeout)
}

func (c *Client) WaitStatus(ctx context.Context, status string, timeout time.Duration) (WaitResult, error) {
	if err := c.ready(); err != nil {
		return WaitResult{}, err
	}
	status = strings.TrimSpace(status)
	if c.provider != ProviderCloud {
		current, err := c.Status(ctx)
		if err != nil {
			return WaitResult{}, err
		}
		return WaitResult{Done: waitResultDone(current.Status, status), TargetStatus: status, Sandbox: current}, nil
	}
	if timeout < 0 {
		return WaitResult{}, errors.New("agentsandbox: wait timeout must not be negative")
	}
	query := map[string]string{"timeout": formatSeconds(timeout)}
	if status != "" {
		query["status"] = status
	}
	path := c.queryPath(c.sandboxPath("wait"), query)
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
	if c.provider == ProviderCloud {
		result, err := c.WaitStatus(ctx, "ready", timeout)
		if err != nil {
			return err
		}
		if result.Sandbox.Status == "ready" {
			return nil
		}
		if terminalSandboxStatus(result.Sandbox.Status) {
			return fmt.Errorf("agentsandbox: sandbox %s is %s", result.Sandbox.ID, result.Sandbox.Status)
		}
		return fmt.Errorf("agentsandbox: timed out waiting for sandbox %s to become ready: %s", c.sandboxID, result.Sandbox.Status)
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
	_, err := c.StartResult(ctx)
	return err
}

func (c *Client) StartResult(ctx context.Context) (SandboxStartResult, error) {
	var result SandboxStartResult
	if err := c.sandboxAction(ctx, "start", &result); err != nil {
		return SandboxStartResult{}, err
	}
	return result, nil
}

func (c *Client) Stop(ctx context.Context) error {
	_, err := c.StopResult(ctx)
	return err
}

func (c *Client) StopResult(ctx context.Context) (SandboxStopResult, error) {
	var result SandboxStopResult
	if err := c.sandboxAction(ctx, "stop", &result); err != nil {
		return SandboxStopResult{}, err
	}
	return result, nil
}

func (c *Client) Restart(ctx context.Context) error {
	_, err := c.RestartResult(ctx)
	return err
}

func (c *Client) RestartResult(ctx context.Context) (SandboxRestartResult, error) {
	var result SandboxRestartResult
	if err := c.sandboxAction(ctx, "restart", &result); err != nil {
		return SandboxRestartResult{}, err
	}
	return result, nil
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
		_, err := c.DeleteResult(ctx)
		return err
	}
	if c.vm == "" {
		return errors.New("agentsandbox: local delete requires vm name")
	}
	return runCommand(ctx, c.coveBin, "vm", "delete", c.vm)
}

func (c *Client) DeleteResult(ctx context.Context) (SandboxDeleteResult, error) {
	if err := c.ready(); err != nil {
		return SandboxDeleteResult{}, err
	}
	if c.provider != ProviderCloud {
		return SandboxDeleteResult{}, errors.New("agentsandbox: delete result is only supported for cloud sandboxes")
	}
	path := c.sandboxPath("")
	if c.leaseHolder != "" {
		path = c.queryPath(path, map[string]string{"holder": c.leaseHolder})
	}
	var result SandboxDeleteResult
	if err := c.request(ctx, http.MethodDelete, path, nil, &result, maxDuration(c.timeout, 2*time.Minute)); err != nil {
		return SandboxDeleteResult{}, err
	}
	return result, nil
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

func (c *Client) sandboxAction(ctx context.Context, action string, out any) error {
	if err := c.ready(); err != nil {
		return err
	}
	if c.provider != ProviderCloud {
		return fmt.Errorf("agentsandbox: %s is only supported for cloud sandboxes", action)
	}
	body := map[string]any{}
	if c.leaseHolder != "" {
		body["holder"] = c.leaseHolder
	}
	return c.request(ctx, http.MethodPost, c.sandboxPath(action), body, out, c.timeout)
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

func (c *Client) requestBytes(ctx context.Context, method, path string, timeout time.Duration) ([]byte, error) {
	if c.provider != ProviderCloud {
		return nil, errors.New("agentsandbox: cloud client not configured")
	}
	ctx, cancel := contextWithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.fleetURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("agentsandbox: create request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentsandbox: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, responseError(method, path, resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("agentsandbox: read response: %w", err)
	}
	return data, nil
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

func placementPlanPath(id string) string {
	return "/v1/placements/plans/" + url.PathEscape(id)
}

func serviceAccountPath(name string) string {
	return "/v1/service-accounts/" + url.PathEscape(name)
}

func oidcBindingPath(name string) string {
	return "/v1/oidc-bindings/" + url.PathEscape(name)
}

func samlBindingPath(name string) string {
	return "/v1/saml-bindings/" + url.PathEscape(name)
}

func samlBindingActionPath(name, action string) string {
	return samlBindingPath(name) + "/" + url.PathEscape(action)
}

func imagePreparationPath(id string) string {
	return "/v1/images/preparations/" + url.PathEscape(id)
}

func imageGCRunPath(id string) string {
	return "/v1/images/gc/runs/" + url.PathEscape(id)
}

func lifecyclePolicyRunPath(id string) string {
	return "/v1/policies/lifecycle/runs/" + url.PathEscape(id)
}

func storageBudgetRunPath(id string) string {
	return "/v1/storage/budget/runs/" + url.PathEscape(id)
}

func storagePruneRunPath(id string) string {
	return "/v1/storage/prune/runs/" + url.PathEscape(id)
}

func controllerRunPath(id string) string {
	return "/v1/operations/runs/" + url.PathEscape(id)
}

func workerPath(id string) string {
	return "/v1/workers/" + url.PathEscape(id)
}

func assignmentPath(id string) string {
	return "/v1/assignments/" + url.PathEscape(id)
}

func assignmentActionPath(id, action string) string {
	return assignmentPath(id) + "/" + url.PathEscape(action)
}

func workerActionPath(id, action string) string {
	return workerPath(id) + "/" + url.PathEscape(action)
}

func workerLifecycle(ctx context.Context, opts WorkerLifecycleOptions, action string) (HostRecord, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return HostRecord{}, errors.New("agentsandbox: worker id required")
	}
	c, err := newFleetClient(opts.FleetURL, opts.APIKey, "", opts.Timeout, opts.HTTP, "worker-"+action)
	if err != nil {
		return HostRecord{}, err
	}
	body := workerLifecycleBody(opts.Reason, false)
	var result HostRecord
	if err := c.request(ctx, http.MethodPost, workerActionPath(id, action), body, &result, c.timeout); err != nil {
		return HostRecord{}, err
	}
	return result, nil
}

func workerLifecycleBody(reason string, force bool) map[string]any {
	body := map[string]any{}
	if reason := strings.TrimSpace(reason); reason != "" {
		body["reason"] = reason
	}
	if force {
		body["force"] = true
	}
	return body
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

func setQuery(query url.Values, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		query.Set(key, value)
	}
}

func queryPathValues(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
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

func waitResultDone(status, targetStatus string) bool {
	status = strings.TrimSpace(status)
	targetStatus = strings.TrimSpace(targetStatus)
	if targetStatus != "" && status == targetStatus {
		return true
	}
	return terminalSandboxStatus(status)
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

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func selectorBody(namespace string, labels map[string]string, capabilities []string) map[string]any {
	body := make(map[string]any)
	if ns := strings.TrimSpace(namespace); ns != "" {
		body["namespace"] = ns
	}
	if labels := cleanStringMap(labels); len(labels) > 0 {
		body["required_labels"] = labels
	}
	if capabilities := cleanStrings(capabilities); len(capabilities) > 0 {
		body["required_capabilities"] = capabilities
	}
	return body
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
		Error              string         `json:"error"`
		PlacementPlan      *PlacementPlan `json:"placement_plan"`
		MaxActiveSandboxes int            `json:"max_active_sandboxes"`
		ActiveCount        int            `json:"active_count"`
	}
	if json.Unmarshal(data, &errBody) == nil && strings.TrimSpace(errBody.Error) != "" {
		msg = strings.TrimSpace(errBody.Error)
		if errBody.PlacementPlan != nil && strings.TrimSpace(errBody.PlacementPlan.ID) != "" {
			msg += " (placement_plan=" + strings.TrimSpace(errBody.PlacementPlan.ID) + ")"
		}
		if errBody.MaxActiveSandboxes > 0 {
			msg += fmt.Sprintf(" (active_sandboxes=%d max_active_sandboxes=%d)", errBody.ActiveCount, errBody.MaxActiveSandboxes)
		}
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
