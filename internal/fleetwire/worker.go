// Package fleetwire defines the HTTP messages used by Cove workers.
package fleetwire

import (
	"strings"
	"time"
)

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
	Capabilities []string          `json:"capabilities,omitempty"`
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
	QueueTTL             string            `json:"queue_ttl,omitempty"`
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

func WarmPoolAssignmentVMName(assignment Assignment) string {
	return assignmentForkName(assignment, warmPoolForkName(assignment.WarmPool, assignment.ID))
}

func SandboxAssignmentVMName(assignment Assignment) string {
	return assignmentForkName(assignment, sandboxVMName(assignment.SandboxID))
}

func assignmentForkName(assignment Assignment, fallback string) string {
	for i, arg := range assignment.Args {
		if (arg == "-fork-name" || arg == "--fork-name") && i+1 < len(assignment.Args) {
			name := strings.TrimSpace(assignment.Args[i+1])
			if name != "" {
				return name
			}
		}
		for _, prefix := range []string{"-fork-name=", "--fork-name="} {
			if strings.HasPrefix(arg, prefix) {
				name := strings.TrimSpace(strings.TrimPrefix(arg, prefix))
				if name != "" {
					return name
				}
			}
		}
	}
	return fallback
}

func warmPoolForkName(poolName, assignmentID string) string {
	return "cove-warm-" + warmPoolSafeName(poolName) + "-" + warmPoolSafeName(assignmentID)
}

func warmPoolSafeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "pool"
	}
	return strings.ToLower(out)
}

func sandboxVMName(id string) string {
	return "cove-sandbox-" + warmPoolSafeName(id)
}
