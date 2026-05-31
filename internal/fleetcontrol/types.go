package fleetcontrol

import "time"

const (
	VerbRegister        = "register"
	VerbHeartbeat       = "heartbeat"
	VerbAwaitAssignment = "await-assignment"
	VerbReportStatus    = "report-status"
)

const DefaultWorkerTTL = 30 * time.Second

type Capacity struct {
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	VMs         int    `json:"vms,omitempty"`
	Images      int    `json:"images,omitempty"`
}

type WorkerHeartbeat struct {
	ID      string            `json:"id"`
	Host    string            `json:"host,omitempty"`
	Address string            `json:"address,omitempty"`
	Version string            `json:"version,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Capacity
}

type HostRecord struct {
	ID       string            `json:"id"`
	Host     string            `json:"host,omitempty"`
	Address  string            `json:"address,omitempty"`
	Version  string            `json:"version,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Capacity Capacity          `json:"capacity,omitempty"`
	Status   string            `json:"status"`
	LastSeen time.Time         `json:"last_seen"`
	Expires  time.Time         `json:"expires"`
	Report   *WorkerReport     `json:"last_report,omitempty"`
}

type WorkerReport struct {
	ID           string    `json:"id"`
	AssignmentID string    `json:"assignment_id,omitempty"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Time         time.Time `json:"time,omitempty"`
}

type Assignment struct {
	ID   string   `json:"id"`
	Verb string   `json:"verb"`
	Args []string `json:"args,omitempty"`
}
