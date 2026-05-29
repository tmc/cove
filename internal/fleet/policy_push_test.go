// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// operatorPost POSTs req as JSON to url with an optional bearer token and
// decodes the response into out, returning an error on non-200 status.
func operatorPost(ctx context.Context, url, token string, req, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set(fleetproto.AuthHeader, "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, bytes.TrimSpace(data))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func registerHosts(t *testing.T, reg *HostRegistry, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := reg.Register(fleetproto.Register{HostID: id}); err != nil {
			t.Fatalf("register %q: %v", id, err)
		}
	}
}

func TestPushPolicyTargetsSelectedHosts(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	registerHosts(t, reg, "h1", "h2", "h3")

	policy := FleetPolicy{IdleTimeout: 30 * time.Minute, MaxAge: 24 * time.Hour, RunBudget: 5}
	res, err := reg.PushPolicy(policy, []string{"h1", "h3"})
	if err != nil {
		t.Fatalf("PushPolicy: %v", err)
	}
	if res.Enqueued != 2 || res.Failed != 0 {
		t.Fatalf("enqueued/failed = %d/%d, want 2/0", res.Enqueued, res.Failed)
	}
	if len(res.Outcomes) != 2 || res.Outcomes[0].HostID != "h1" || res.Outcomes[1].HostID != "h3" {
		t.Fatalf("outcomes = %+v, want sorted h1,h3", res.Outcomes)
	}

	// h2 must have received nothing.
	if got, _ := reg.Assignments("h2", reg.hosts["h2"].LeaseID); len(got) != 0 {
		t.Fatalf("h2 got %d assignments, want 0", len(got))
	}
	// h1 must have one policy assignment carrying the encoded payload.
	got, err := reg.Assignments("h1", reg.hosts["h1"].LeaseID)
	if err != nil {
		t.Fatalf("assignments h1: %v", err)
	}
	if len(got) != 1 || got[0].Kind != fleetproto.KindPolicy {
		t.Fatalf("h1 assignments = %+v, want one policy", got)
	}
	var pp fleetproto.PolicyPayload
	if err := json.Unmarshal(got[0].Payload, &pp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if pp.IdleTimeout != "30m0s" || pp.MaxAge != "24h0m0s" || pp.RunBudget != 5 {
		t.Fatalf("payload = %+v, want 30m/24h/5", pp)
	}
}

func TestPushPolicyEmptyHostsTargetsAll(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	registerHosts(t, reg, "a", "b")
	res, err := reg.PushPolicy(FleetPolicy{RunBudget: 1}, nil)
	if err != nil {
		t.Fatalf("PushPolicy: %v", err)
	}
	if res.Enqueued != 2 {
		t.Fatalf("enqueued = %d, want 2 (all hosts)", res.Enqueued)
	}
}

func TestPushPolicyRejectsNegative(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	registerHosts(t, reg, "h1")
	if _, err := reg.PushPolicy(FleetPolicy{IdleTimeout: -time.Second}, nil); err == nil {
		t.Fatal("expected negative idle timeout to be rejected")
	}
}

func TestPushImageGCTargetsHosts(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	registerHosts(t, reg, "h1", "h2")
	res := reg.PushImageGC([]string{"h1"})
	if res.Enqueued != 1 || res.Kind != fleetproto.KindImageGC {
		t.Fatalf("push image-gc = %+v, want one image-gc", res)
	}
	got, _ := reg.Assignments("h1", reg.hosts["h1"].LeaseID)
	if len(got) != 1 || got[0].Kind != fleetproto.KindImageGC {
		t.Fatalf("h1 assignments = %+v, want one image-gc", got)
	}
}

func TestPushRecordsFailureForUnknownHost(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	registerHosts(t, reg, "h1")
	res, err := reg.PushPolicy(FleetPolicy{RunBudget: 1}, []string{"h1", "ghost"})
	if err != nil {
		t.Fatalf("PushPolicy: %v", err)
	}
	if res.Enqueued != 1 || res.Failed != 1 {
		t.Fatalf("enqueued/failed = %d/%d, want 1/1", res.Enqueued, res.Failed)
	}
	var ghost PushOutcome
	for _, o := range res.Outcomes {
		if o.HostID == "ghost" {
			ghost = o
		}
	}
	if ghost.Error == "" || ghost.AssignmentID != "" {
		t.Fatalf("ghost outcome = %+v, want an error and no assignment id", ghost)
	}
}

// TestAggregateResultsAcrossHosts pushes to three hosts, then simulates each
// reporting: one done, one failed, one silent (pending). Aggregation must
// reflect all three states.
func TestAggregateResultsAcrossHosts(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	reg, srv := newTestController(t, "", func() time.Time { return clock })
	registerHosts(t, reg, "h1", "h2", "h3")

	res, err := reg.PushPolicy(FleetPolicy{RunBudget: 3}, nil)
	if err != nil {
		t.Fatalf("PushPolicy: %v", err)
	}
	byHost := map[string]string{}
	for _, o := range res.Outcomes {
		byHost[o.HostID] = o.AssignmentID
	}

	ctx := context.Background()
	client := srv.Client()
	report := func(host, id, state, detail string) {
		lease := reg.hosts[host].LeaseID
		if _, err := fleetproto.Call[fleetproto.ReportStatus, fleetproto.StatusAck](
			ctx, client, srv.URL, fleetproto.PathStatus, lease,
			fleetproto.ReportStatus{HostID: host, LeaseID: lease, AssignmentID: id, State: state, Detail: detail}); err != nil {
			t.Fatalf("report %s: %v", host, err)
		}
	}
	report("h1", byHost["h1"], fleetproto.StateDone, `{"applied":2,"stopped":1}`)
	report("h2", byHost["h2"], fleetproto.StateFailed, "boom")
	// h3 stays silent.

	results := reg.AggregateResults(fleetproto.KindPolicy)
	if len(results) != 3 {
		t.Fatalf("aggregated %d results, want 3", len(results))
	}
	want := map[string]struct {
		state   string
		pending bool
	}{
		"h1": {fleetproto.StateDone, false},
		"h2": {fleetproto.StateFailed, false},
		"h3": {"", true},
	}
	for _, r := range results {
		w := want[r.HostID]
		if r.State != w.state || r.Pending != w.pending {
			t.Fatalf("host %s: state=%q pending=%v, want %q/%v", r.HostID, r.State, r.Pending, w.state, w.pending)
		}
	}
}

func TestAggregateResultsFilterByKind(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	registerHosts(t, reg, "h1")
	if _, err := reg.PushPolicy(FleetPolicy{RunBudget: 1}, nil); err != nil {
		t.Fatalf("PushPolicy: %v", err)
	}
	reg.PushImageGC(nil)

	if got := reg.AggregateResults(fleetproto.KindPolicy); len(got) != 1 || got[0].Kind != fleetproto.KindPolicy {
		t.Fatalf("policy filter = %+v, want one policy", got)
	}
	if got := reg.AggregateResults(fleetproto.KindImageGC); len(got) != 1 || got[0].Kind != fleetproto.KindImageGC {
		t.Fatalf("image-gc filter = %+v, want one image-gc", got)
	}
	if got := reg.AggregateResults(""); len(got) != 2 {
		t.Fatalf("unfiltered = %d, want 2", len(got))
	}
}

func TestOperatorEndpointsRequireToken(t *testing.T) {
	reg, err := NewHostRegistry("", "admin-secret")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	if _, err := reg.Register(fleetproto.Register{HostID: "h1", Token: "admin-secret"}); err != nil {
		t.Fatalf("register h1: %v", err)
	}
	srv := httptest.NewServer(NewController(reg).Handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()

	// Without the operator token the push must be rejected.
	var ignored PushResult
	if err := operatorPost(ctx, srv.URL+PathPushPolicy, "", PushPolicyRequest{RunBudget: 1}, &ignored); err == nil {
		t.Fatal("expected push without token to fail")
	}

	// With the token it succeeds and the results endpoint returns the host.
	var pushed PushResult
	if err := operatorPost(ctx, srv.URL+PathPushPolicy, "admin-secret", PushPolicyRequest{RunBudget: 1}, &pushed); err != nil {
		t.Fatalf("authorized push: %v", err)
	}
	if pushed.Enqueued != 1 {
		t.Fatalf("enqueued = %d, want 1", pushed.Enqueued)
	}
}

func TestPushPolicyHTTPEndpointParsesDurations(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	registerHosts(t, reg, "h1")
	srv := httptest.NewServer(NewController(reg).Handler())
	t.Cleanup(srv.Close)

	var pushed PushResult
	if err := operatorPost(context.Background(), srv.URL+PathPushPolicy, "",
		PushPolicyRequest{IdleTimeout: "15m", MaxAge: "12h", RunBudget: 7}, &pushed); err != nil {
		t.Fatalf("push: %v", err)
	}
	got, _ := reg.Assignments("h1", reg.hosts["h1"].LeaseID)
	if len(got) != 1 {
		t.Fatalf("assignments = %d, want 1", len(got))
	}
	var pp fleetproto.PolicyPayload
	if err := json.Unmarshal(got[0].Payload, &pp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if pp.IdleTimeout != "15m0s" || pp.MaxAge != "12h0m0s" || pp.RunBudget != 7 {
		t.Fatalf("payload = %+v, want 15m/12h/7", pp)
	}
}
