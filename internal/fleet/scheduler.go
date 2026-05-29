// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// MaxRetainedScores bounds the number of ranked candidates the scheduler keeps,
// matching Nomad's MaxRetainedNodeScores. Placement stays O(k) in the returned
// set rather than O(all-hosts).
const MaxRetainedScores = 5

// imageAffinityBonus is the rank bonus for a host that already holds the
// requested base image. Forking from a local parent is a ~132-140ms APFS
// clonefile; a cold base forces a multi-GB cross-host transfer, so the bonus is
// large enough to dominate bin-pack density and anti-affinity terms when an
// image-local host is feasible.
const imageAffinityBonus = 1_000_000.0

// antiAffinityPenalty is subtracted per existing replica of the same job or base
// already running on a host, spreading replicas so one host failure does not
// take out every fork of a base.
const antiAffinityPenalty = 10_000.0

// ScheduleRequest describes the VM the controller wants to place. RequestedRAM
// is the bytes the fork needs; BaseRef is the parent image to fork from; JobID
// groups replicas for anti-affinity (empty falls back to BaseRef). Arch and
// MinMacOS are feasibility floors; empty values impose no constraint.
type ScheduleRequest struct {
	RequestedRAM int64
	BaseRef      string
	JobID        string
	Arch         string
	MinMacOS     string
}

// Candidate is one ranked feasible host. Score is higher-is-better; ImageLocal
// reports whether the host already holds the requested base image.
type Candidate struct {
	HostID     string
	Score      float64
	ImageLocal bool
	FreeRAM    int64
}

// Placement is the scheduler's decision. Chosen is the winning host; Candidates
// is the retained top-k ranked best-first. NeedsImageSync is true when no
// feasible host holds the base image, so the controller must run a pre-flight
// image sync onto Chosen before issuing the fork.
type Placement struct {
	Chosen         string
	Candidates     []Candidate
	NeedsImageSync bool
}

// replicaKey groups replicas for anti-affinity. The job id is preferred so
// distinct jobs sharing a base still spread, falling back to the base ref.
func (req ScheduleRequest) replicaKey() string {
	if req.JobID != "" {
		return "job:" + req.JobID
	}
	return "base:" + req.BaseRef
}

// feasible reports whether a host can host the request, given the bytes already
// reserved against it but not yet reflected in its heartbeat FreeRAMBytes. The
// caller must hold r.mu.
func (r *HostRegistry) feasible(h *Host, req ScheduleRequest, reserved int64) bool {
	if !r.isOnline(h) {
		return false
	}
	if r.isCordoned(h.HostID) {
		return false
	}
	if req.Arch != "" && h.Arch != "" && !strings.EqualFold(h.Arch, req.Arch) {
		return false
	}
	if req.MinMacOS != "" && h.MacOSVersion != "" && !versionAtLeast(h.MacOSVersion, req.MinMacOS) {
		return false
	}
	avail := h.FreeRAMBytes - reserved
	return avail >= req.RequestedRAM
}

// hostHasImage reports whether the host's heartbeat image list contains ref.
func hostHasImage(h *Host, ref string) bool {
	if ref == "" {
		return false
	}
	for _, img := range h.Images {
		if img == ref {
			return true
		}
	}
	return false
}

// replicaCount counts running VMs on the host that belong to the request's
// replica group. RunningVMs entries are tagged "name@key" by the warm pool /
// fork path; an untagged name contributes nothing to anti-affinity.
func replicaCount(h *Host, key string) int {
	n := 0
	for _, vm := range h.RunningVMs {
		if at := strings.LastIndexByte(vm, '@'); at >= 0 && vm[at+1:] == key {
			n++
		}
	}
	return n
}

// rankScore computes a host's placement score. Higher is better. The terms,
// largest-magnitude first: a fixed image-affinity bonus when image-local, an
// anti-affinity penalty per same-group replica, and a bin-pack density term that
// prefers the densest host that still fits (least free RAM after placement).
func rankScore(h *Host, req ScheduleRequest, reserved int64, imageLocal bool) float64 {
	var score float64
	if imageLocal {
		score += imageAffinityBonus
	}
	score -= antiAffinityPenalty * float64(replicaCount(h, req.replicaKey()))
	// Bin-pack: prefer the densest fit. remaining is the free RAM after this
	// placement; smaller remaining is denser, so negate it. Scaled to GiB to keep
	// the term well below the affinity/anti-affinity magnitudes.
	remaining := float64(h.FreeRAMBytes-reserved-req.RequestedRAM) / (1 << 30)
	score -= remaining
	return score
}

// Schedule runs two-phase feasibility-then-rank placement and atomically
// reserves the request's RAM on the chosen host. The reservation prevents two
// concurrent Schedule calls from oversubscribing one Mac's RAM: the durable
// commit (reservation) happens inside a single mutex critical section before the
// scheduler returns. The reservation is released by ReleaseReservation once the
// worker reports the VM running (so the next heartbeat's FreeRAMBytes already
// reflects it).
//
// Schedule returns the placement and a reservation token. A nil placement with a
// non-nil error means no feasible host exists.
func (r *HostRegistry) Schedule(req ScheduleRequest) (Placement, string, error) {
	if req.RequestedRAM <= 0 {
		return Placement{}, "", fmt.Errorf("schedule: requested ram must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Phase 1: feasibility filter, accounting for outstanding reservations.
	type scored struct {
		host       *Host
		candidate  Candidate
		imageLocal bool
	}
	var feasible []scored
	anyImageLocal := false
	for _, h := range r.hosts {
		reserved := r.reservedFor(h.HostID)
		if !r.feasible(h, req, reserved) {
			continue
		}
		imageLocal := hostHasImage(h, req.BaseRef)
		if imageLocal {
			anyImageLocal = true
		}
		feasible = append(feasible, scored{
			host:       h,
			imageLocal: imageLocal,
			candidate: Candidate{
				HostID:     h.HostID,
				Score:      rankScore(h, req, reserved, imageLocal),
				ImageLocal: imageLocal,
				FreeRAM:    h.FreeRAMBytes - reserved,
			},
		})
	}
	if len(feasible) == 0 {
		return Placement{}, "", fmt.Errorf("schedule: no feasible host for %d bytes", req.RequestedRAM)
	}

	// Phase 2: rank best-first; ties broken by host id for determinism.
	sort.Slice(feasible, func(i, j int) bool {
		if feasible[i].candidate.Score != feasible[j].candidate.Score {
			return feasible[i].candidate.Score > feasible[j].candidate.Score
		}
		return feasible[i].candidate.HostID < feasible[j].candidate.HostID
	})
	if len(feasible) > MaxRetainedScores {
		feasible = feasible[:MaxRetainedScores]
	}

	candidates := make([]Candidate, len(feasible))
	for i := range feasible {
		candidates[i] = feasible[i].candidate
	}
	chosen := feasible[0]

	token := r.reserve(chosen.host.HostID, req.RequestedRAM)
	return Placement{
		Chosen:         chosen.host.HostID,
		Candidates:     candidates,
		NeedsImageSync: !anyImageLocal,
	}, token, nil
}

// ForkRunPlacement is the result of ScheduleForkRun: where the fork was placed,
// the reservation token to release once it is running, the enqueued fork-run
// assignment id, and (when a pre-flight sync was needed) the image-sync
// assignment id queued ahead of it.
type ForkRunPlacement struct {
	Placement
	ReservationToken string
	ForkAssignmentID string
	SyncAssignmentID string
}

// ScheduleForkRun is the controller-mode replacement for the client-side
// least-loaded path: it runs Schedule, optionally queues a pre-flight image sync
// when no feasible host holds the base image, then enqueues the fork-run
// assignment on the chosen host. The MIT client-side SelectLeastLoadedHost stays
// for the stateless SSH path; this method is only used in controller mode.
//
// The image-sync assignment is enqueued before the fork-run so the worker
// processes them in order. The caller releases the reservation via
// ReleaseReservation once the worker reports the VM running.
func (r *HostRegistry) ScheduleForkRun(req ScheduleRequest, name string) (ForkRunPlacement, error) {
	placement, token, err := r.Schedule(req)
	if err != nil {
		return ForkRunPlacement{}, err
	}
	out := ForkRunPlacement{Placement: placement, ReservationToken: token}

	if placement.NeedsImageSync {
		syncPayload, err := json.Marshal(fleetproto.ImageSyncPayload{Ref: req.BaseRef})
		if err != nil {
			r.ReleaseReservation(token)
			return ForkRunPlacement{}, fmt.Errorf("schedule fork-run: encode image-sync: %w", err)
		}
		syncID, err := r.Enqueue(placement.Chosen, fleetproto.Assignment{Kind: fleetproto.KindImageSync, Payload: syncPayload})
		if err != nil {
			r.ReleaseReservation(token)
			return ForkRunPlacement{}, fmt.Errorf("schedule fork-run: enqueue image-sync: %w", err)
		}
		out.SyncAssignmentID = syncID
	}

	forkPayload, err := json.Marshal(fleetproto.ForkRunPayload{
		BaseRef:  req.BaseRef,
		Name:     name,
		RAMBytes: req.RequestedRAM,
		JobID:    req.JobID,
	})
	if err != nil {
		r.ReleaseReservation(token)
		return ForkRunPlacement{}, fmt.Errorf("schedule fork-run: encode fork-run: %w", err)
	}
	forkID, err := r.Enqueue(placement.Chosen, fleetproto.Assignment{Kind: fleetproto.KindForkRun, Payload: forkPayload})
	if err != nil {
		r.ReleaseReservation(token)
		return ForkRunPlacement{}, fmt.Errorf("schedule fork-run: enqueue fork-run: %w", err)
	}
	out.ForkAssignmentID = forkID
	return out, nil
}

// reservation is one outstanding RAM hold on a host, kept until the worker
// reports the VM running.
type reservation struct {
	hostID string
	bytes  int64
}

// reservedFor sums outstanding reservation bytes against a host. Caller holds
// r.mu.
func (r *HostRegistry) reservedFor(hostID string) int64 {
	var sum int64
	for _, res := range r.reservations {
		if res.hostID == hostID {
			sum += res.bytes
		}
	}
	return sum
}

// reserve records a RAM hold and returns its token. Caller holds r.mu.
func (r *HostRegistry) reserve(hostID string, bytes int64) string {
	r.resSeq++
	token := fmt.Sprintf("res-%d", r.resSeq)
	if r.reservations == nil {
		r.reservations = make(map[string]reservation)
	}
	r.reservations[token] = reservation{hostID: hostID, bytes: bytes}
	return token
}

// ReleaseReservation drops a reservation by token. It is idempotent: an unknown
// token is a no-op. The controller calls this once the worker confirms the VM is
// running (and thus reflected in the next heartbeat's FreeRAMBytes), or when a
// placement fails to launch.
func (r *HostRegistry) ReleaseReservation(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.reservations, token)
}

// ReservedRAM reports the total bytes reserved against a host. It is exported
// for observability and tests.
func (r *HostRegistry) ReservedRAM(hostID string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reservedFor(hostID)
}

// versionAtLeast reports whether dotted-decimal version got is >= want. Missing
// components are treated as zero, so "15" >= "14.5" and "14.5" >= "14". A
// non-numeric component compares as zero.
func versionAtLeast(got, want string) bool {
	g := splitVersion(got)
	w := splitVersion(want)
	n := len(g)
	if len(w) > n {
		n = len(w)
	}
	for i := 0; i < n; i++ {
		gi, wi := 0, 0
		if i < len(g) {
			gi = g[i]
		}
		if i < len(w) {
			wi = w[i]
		}
		if gi != wi {
			return gi > wi
		}
	}
	return true
}

func splitVersion(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i], _ = strconv.Atoi(strings.TrimSpace(p))
	}
	return out
}
