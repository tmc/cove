// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"sort"
	"sync"
	"time"
)

// MeterEventKind is the lifecycle transition a metering event reports. Only
// transitions that change whether a sandbox is billable matter: a sandbox
// accrues usage while running and stops accruing while stopped, deleted, or
// failed.
type MeterEventKind string

const (
	// MeterStart begins (or resumes) accrual for a sandbox.
	MeterStart MeterEventKind = "start"
	// MeterStop ends accrual without tearing the sandbox down.
	MeterStop MeterEventKind = "stop"
	// MeterDelete ends accrual permanently.
	MeterDelete MeterEventKind = "delete"
)

// MeterEvent is one worker-reported lifecycle transition the ledger ingests.
// SandboxID is the billable unit; VCPUs and RAMBytes are the resource shape that
// was running between this event and the next; At is the transition time.
//
// BYO-LLM-key is intentionally absent: the control plane never sees tokens, so
// there is nothing token-related to meter. Compute (vCPU+RAM over wall time) is
// the only SKU.
type MeterEvent struct {
	SandboxID string         `json:"sandbox_id"`
	Kind      MeterEventKind `json:"kind"`
	VCPUs     int            `json:"vcpus,omitempty"`
	RAMBytes  int64          `json:"ram_bytes,omitempty"`
	At        time.Time      `json:"at"`
}

// Usage is the accrued, billable consumption for one sandbox. WallSeconds is the
// total time spent running; VCPUSeconds and RAMByteSeconds integrate the running
// resource shape over that time. Stopped intervals contribute nothing.
type Usage struct {
	SandboxID      string  `json:"sandbox_id"`
	WallSeconds    float64 `json:"wall_seconds"`
	VCPUSeconds    float64 `json:"vcpu_seconds"`
	RAMByteSeconds float64 `json:"ram_byte_seconds"`
	Running        bool    `json:"running"`
}

// ledgerEntry is the mutable per-sandbox accumulator. When running, runningSince
// marks the start of the open interval and vcpus/ramBytes are the shape that
// interval is accruing.
type ledgerEntry struct {
	usage        Usage
	running      bool
	runningSince time.Time
	vcpus        int
	ramBytes     int64
}

// UsageLedger accrues per-sandbox compute usage from worker-reported lifecycle
// events. It charges only while a sandbox is running: a MeterStart opens an
// accrual interval and a MeterStop/MeterDelete closes it, integrating
// vCPU-seconds and RAM-byte-seconds over the wall time of the interval.
//
// It is safe for concurrent use. The zero value is not usable; build one with
// NewUsageLedger.
type UsageLedger struct {
	// Now is injected for testability; nil falls back to time.Now.
	Now func() time.Time

	mu      sync.Mutex
	entries map[string]*ledgerEntry
}

// NewUsageLedger returns an empty ledger.
func NewUsageLedger() *UsageLedger {
	return &UsageLedger{entries: make(map[string]*ledgerEntry)}
}

func (l *UsageLedger) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Record ingests one lifecycle event. A MeterStart opens an accrual interval at
// the event time (a repeated start with no intervening stop is a no-op, so
// duplicate reports do not double-bill). MeterStop and MeterDelete close any
// open interval, folding its wall/vCPU/RAM contribution into the sandbox's
// totals. Events with an empty SandboxID are ignored.
func (l *UsageLedger) Record(ev MeterEvent) {
	if ev.SandboxID == "" {
		return
	}
	at := ev.At
	if at.IsZero() {
		at = l.now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[ev.SandboxID]
	if e == nil {
		e = &ledgerEntry{usage: Usage{SandboxID: ev.SandboxID}}
		l.entries[ev.SandboxID] = e
	}
	switch ev.Kind {
	case MeterStart:
		if e.running {
			return
		}
		e.running = true
		e.runningSince = at
		e.vcpus = ev.VCPUs
		e.ramBytes = ev.RAMBytes
	case MeterStop, MeterDelete:
		l.closeInterval(e, at)
	}
}

// closeInterval folds an open accrual interval into the entry's totals. Caller
// holds l.mu. A non-running entry or a backwards/zero-length interval contributes
// nothing.
func (l *UsageLedger) closeInterval(e *ledgerEntry, at time.Time) {
	if !e.running {
		return
	}
	e.running = false
	secs := at.Sub(e.runningSince).Seconds()
	if secs <= 0 {
		return
	}
	e.usage.WallSeconds += secs
	e.usage.VCPUSeconds += secs * float64(e.vcpus)
	e.usage.RAMByteSeconds += secs * float64(e.ramBytes)
}

// Report returns the accrued usage for a sandbox. When the sandbox is currently
// running, the open interval up to now is included so a live caller sees usage
// trending without waiting for the stop event. The persisted totals are not
// mutated; the open interval is only closed by a Record(stop/delete).
func (l *UsageLedger) Report(sandboxID string) (Usage, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[sandboxID]
	if e == nil {
		return Usage{}, false
	}
	usage := e.usage
	usage.Running = e.running
	if e.running {
		secs := l.now().Sub(e.runningSince).Seconds()
		if secs > 0 {
			usage.WallSeconds += secs
			usage.VCPUSeconds += secs * float64(e.vcpus)
			usage.RAMByteSeconds += secs * float64(e.ramBytes)
		}
	}
	return usage, true
}

// Reports returns the accrued usage for every known sandbox, sorted by id.
func (l *UsageLedger) Reports() []Usage {
	l.mu.Lock()
	ids := make([]string, 0, len(l.entries))
	for id := range l.entries {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	sort.Strings(ids)
	out := make([]Usage, 0, len(ids))
	for _, id := range ids {
		if u, ok := l.Report(id); ok {
			out = append(out, u)
		}
	}
	return out
}
