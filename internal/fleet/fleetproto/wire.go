// Package fleetproto defines the worker<->controller wire protocol for the
// cove fleet control plane. The four verbs are register, heartbeat,
// poll/await-assignment, and report-status, exchanged as JSON over HTTP.
//
// The message types in this file are header-free because both the MIT
// worker-mode client (internal/coved) and the paid controller import them.
// Controller-only request routing lives in the headered files of this package.
package fleetproto

import "encoding/json"

// HTTP paths for the four verbs. Workers POST to register/heartbeat/status and
// long-poll GET on assignments.
const (
	PathRegister    = "/v1/register"
	PathHeartbeat   = "/v1/heartbeat"
	PathAssignments = "/v1/assignments"
	PathStatus      = "/v1/status"
)

// AuthHeader carries the bearer credential: the one-time register token on
// Register, then the per-host LeaseID on every subsequent call.
const AuthHeader = "Authorization"

// Assignment kinds. The worker refuses any kind not in this set, so adding a
// kind here is a deliberate widening of the worker's authority.
const (
	KindForkRun   = "fork-run"
	KindStopVM    = "stop-vm"
	KindImageSync = "image-sync"
	KindPolicy    = "policy"
	KindImageGC   = "image-gc"
)

// ForkRunPayload is the Payload of a KindForkRun assignment: fork a VM from a
// base image and run it. BaseRef is the parent image; Name is the requested VM
// name; RAMBytes is the memory the controller reserved for the fork; JobID
// groups replicas for the scheduler's anti-affinity term.
type ForkRunPayload struct {
	BaseRef  string `json:"base_ref"`
	Name     string `json:"name,omitempty"`
	RAMBytes int64  `json:"ram_bytes,omitempty"`
	JobID    string `json:"job_id,omitempty"`
}

// ImageSyncPayload is the Payload of a KindImageSync assignment: pull Ref onto
// the worker (over the design 034 host-to-host SSH transfer) so a subsequent
// fork-run is image-local. From, when set, names a host already holding the
// image as the transfer source; empty lets the worker resolve a source.
type ImageSyncPayload struct {
	Ref  string `json:"ref"`
	From string `json:"from,omitempty"`
}

// PolicyPayload is the Payload of a KindPolicy assignment: the fleet-wide
// lifecycle thresholds the controller pushes to a worker. Durations are encoded
// as Go duration strings (e.g. "30m", "24h") so the on-wire shape matches the
// per-VM policy file; an empty string means "leave unset".
type PolicyPayload struct {
	IdleTimeout string `json:"idle_timeout,omitempty"`
	MaxAge      string `json:"max_age,omitempty"`
	RunBudget   int    `json:"run_budget,omitempty"`
}

// PolicyResult is the structured Detail a worker reports after applying a
// KindPolicy assignment: how many local VMs received the policy and how many
// were subsequently stopped by lifecycle enforcement.
type PolicyResult struct {
	Applied int `json:"applied"`
	Stopped int `json:"stopped"`
	Failed  int `json:"failed"`
}

// ImageGCResult is the structured Detail a worker reports after running a
// KindImageGC assignment one-shot.
type ImageGCResult struct {
	ManifestsScanned int   `json:"manifests_scanned"`
	ManifestsRemoved int   `json:"manifests_removed"`
	BytesFreed       int64 `json:"bytes_freed"`
	Skipped          bool  `json:"skipped,omitempty"`
}

// Register is the worker's first call. The Token is the controller's one-time
// register token; the controller answers with a per-host LeaseID used as the
// bearer credential on all later calls.
type Register struct {
	HostID       string `json:"host_id"`
	Hostname     string `json:"hostname"`
	Arch         string `json:"arch"`
	MacOSVersion string `json:"macos_version"`
	Token        string `json:"token"`
}

// RegisterResp acknowledges a Register and issues the lease.
type RegisterResp struct {
	HostID  string `json:"host_id"`
	LeaseID string `json:"lease_id"`
	OK      bool   `json:"ok"`
}

// Heartbeat reports a worker's live facts and pulls any queued assignments in
// the response. The controller materializes live VM truth from these fields; it
// keeps no authoritative global VM ledger.
type Heartbeat struct {
	HostID       string   `json:"host_id"`
	LeaseID      string   `json:"lease_id"`
	FreeRAMBytes int64    `json:"free_ram_bytes"`
	VMCount      int      `json:"vm_count"`
	Images       []string `json:"images,omitempty"`
	RunningVMs   []string `json:"running_vms,omitempty"`
}

// HeartbeatResp returns assignments queued for the host since its last poll.
type HeartbeatResp struct {
	Assignments []Assignment `json:"assignments,omitempty"`
}

// Assignment is a unit of work the controller pushes to a worker. Kind selects
// the bounded handler; Payload is the kind-specific arguments.
type Assignment struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ReportStatus is the worker's terminal (or progress) report for an assignment.
type ReportStatus struct {
	HostID       string `json:"host_id"`
	LeaseID      string `json:"lease_id"`
	AssignmentID string `json:"assignment_id"`
	State        string `json:"state"`
	Detail       string `json:"detail,omitempty"`
}

// StatusAck acknowledges a ReportStatus.
type StatusAck struct {
	OK bool `json:"ok"`
}

// Assignment states reported by workers.
const (
	StateAccepted = "accepted"
	StateRunning  = "running"
	StateDone     = "done"
	StateFailed   = "failed"
	StateRefused  = "refused"
)

// KnownKind reports whether kind is an assignment kind the protocol defines.
// Workers use this to refuse unknown (e.g. host-shell) kinds.
func KnownKind(kind string) bool {
	switch kind {
	case KindForkRun, KindStopVM, KindImageSync, KindPolicy, KindImageGC:
		return true
	default:
		return false
	}
}
