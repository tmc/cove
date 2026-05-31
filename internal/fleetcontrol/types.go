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

const DefaultWorkerTTL = 30 * time.Second

const DefaultAssignmentTTL = 30 * time.Second

const DefaultPlacementPlanLimit = 5

type Capacity struct {
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	VMs         int    `json:"vms,omitempty"`
	MaxVMs      int    `json:"max_vms,omitempty"`
	Images      int    `json:"images,omitempty"`
}

type WorkerHeartbeat struct {
	ID        string            `json:"id"`
	Host      string            `json:"host,omitempty"`
	Address   string            `json:"address,omitempty"`
	Version   string            `json:"version,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	ImageRefs []string          `json:"image_refs,omitempty"`
	Capacity
}

type HostRecord struct {
	ID           string            `json:"id"`
	Host         string            `json:"host,omitempty"`
	Address      string            `json:"address,omitempty"`
	Version      string            `json:"version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	ImageRefs    []string          `json:"image_refs,omitempty"`
	Capacity     Capacity          `json:"capacity,omitempty"`
	Status       string            `json:"status"`
	Cordoned     bool              `json:"cordoned"`
	CordonReason string            `json:"cordon_reason,omitempty"`
	CordonedAt   time.Time         `json:"cordoned_at,omitempty"`
	LastSeen     time.Time         `json:"last_seen"`
	Expires      time.Time         `json:"expires"`
	Report       *WorkerReport     `json:"last_report,omitempty"`
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
}

type WorkerLifecycle struct {
	Reason string `json:"reason,omitempty"`
}

type ImagePrepareRequest struct {
	SourceRef      string            `json:"source_ref"`
	ImageRef       string            `json:"image_ref"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Force          bool              `json:"force,omitempty"`
}

type ImagePrepareResult struct {
	SourceRef   string             `json:"source_ref"`
	ImageRef    string             `json:"image_ref"`
	Assignments []Assignment       `json:"assignments,omitempty"`
	Skipped     []ImagePrepareSkip `json:"skipped,omitempty"`
}

type ImagePrepareSkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type ImageGCRequest struct {
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	OlderThan      string            `json:"older_than,omitempty"`
	Apply          bool              `json:"apply,omitempty"`
}

type ImageGCResult struct {
	Assignments []Assignment  `json:"assignments,omitempty"`
	Skipped     []ImageGCSkip `json:"skipped,omitempty"`
}

type ImageGCSkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type LifecyclePolicyRequest struct {
	VMName         string            `json:"vm_name"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Clear          bool              `json:"clear,omitempty"`
	IdleTimeout    string            `json:"idle_timeout,omitempty"`
	MaxAge         string            `json:"max_age,omitempty"`
	RunBudget      int               `json:"run_budget,omitempty"`
}

type LifecyclePolicyResult struct {
	VMName      string                `json:"vm_name"`
	Assignments []Assignment          `json:"assignments,omitempty"`
	Skipped     []LifecyclePolicySkip `json:"skipped,omitempty"`
}

type LifecyclePolicySkip struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

type StorageBudgetRequest struct {
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Clear          bool              `json:"clear,omitempty"`
	Target         string            `json:"target,omitempty"`
	WarnPct        *int              `json:"warn_pct,omitempty"`
	HardPct        *int              `json:"hard_pct,omitempty"`
}

type StorageBudgetResult struct {
	Assignments []Assignment        `json:"assignments,omitempty"`
	Skipped     []StoragePolicySkip `json:"skipped,omitempty"`
}

type StoragePruneRequest struct {
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Category       string            `json:"category,omitempty"`
	OlderThan      string            `json:"older_than,omitempty"`
	Apply          bool              `json:"apply,omitempty"`
}

type StoragePruneResult struct {
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
	Policy          string               `json:"policy"`
	ImageRef        string               `json:"image_ref,omitempty"`
	RequiredLabels  map[string]string    `json:"required_labels,omitempty"`
	AntiAffinityKey string               `json:"anti_affinity_key,omitempty"`
	Resources       Capacity             `json:"resources,omitempty"`
	Candidates      []PlacementCandidate `json:"candidates,omitempty"`
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
	Name           string            `json:"name,omitempty"`
	ImageRef       string            `json:"image_ref"`
	Size           int               `json:"size"`
	Policy         string            `json:"policy,omitempty"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Resources      Capacity          `json:"resources,omitempty"`
	Args           []string          `json:"args,omitempty"`
}

type WarmPool struct {
	Name           string            `json:"name"`
	ImageRef       string            `json:"image_ref"`
	Size           int               `json:"size"`
	Policy         string            `json:"policy,omitempty"`
	RequiredLabels map[string]string `json:"required_labels,omitempty"`
	Resources      Capacity          `json:"resources,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Created        time.Time         `json:"created,omitempty"`
	Updated        time.Time         `json:"updated,omitempty"`
}

type WarmPoolStatus struct {
	WarmPool
	Active      int          `json:"active"`
	Assignments []Assignment `json:"assignments,omitempty"`
}

type WarmPoolResult struct {
	Pool    WarmPoolStatus `json:"pool"`
	Created []Assignment   `json:"created,omitempty"`
}

type WarmPoolClaimRequest struct {
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

type WarmPoolClaimResult struct {
	Pool       string     `json:"pool"`
	VMName     string     `json:"vm_name"`
	Slot       Assignment `json:"slot"`
	Assignment Assignment `json:"assignment"`
}

type Assignment struct {
	ID              string            `json:"id"`
	WorkerID        string            `json:"worker_id,omitempty"`
	WarmPool        string            `json:"warm_pool,omitempty"`
	WarmPoolSlot    string            `json:"warm_pool_slot,omitempty"`
	Policy          string            `json:"policy,omitempty"`
	ImageRef        string            `json:"image_ref,omitempty"`
	RequiredLabels  map[string]string `json:"required_labels,omitempty"`
	AntiAffinityKey string            `json:"anti_affinity_key,omitempty"`
	Resources       Capacity          `json:"resources,omitempty"`
	Verb            string            `json:"verb"`
	Args            []string          `json:"args,omitempty"`
	Status          string            `json:"status,omitempty"`
	Created         time.Time         `json:"created,omitempty"`
	Updated         time.Time         `json:"updated,omitempty"`
	LeasedTo        string            `json:"leased_to,omitempty"`
	LeaseExpires    time.Time         `json:"lease_expires,omitempty"`
	LastReport      *WorkerReport     `json:"last_report,omitempty"`
}
