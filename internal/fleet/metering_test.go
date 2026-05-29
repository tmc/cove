// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"testing"
	"time"
)

func TestUsageLedgerAccruesOnlyWhileRunning(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name        string
		events      []MeterEvent
		reportAt    time.Time // when Report is called (drives the open interval)
		wantWall    float64
		wantVCPU    float64
		wantRAM     float64
		wantRunning bool
	}{
		{
			name: "start then stop bills the interval",
			events: []MeterEvent{
				{SandboxID: "a", Kind: MeterStart, VCPUs: 2, RAMBytes: 1 << 30, At: base},
				{SandboxID: "a", Kind: MeterStop, At: base.Add(10 * time.Second)},
			},
			reportAt:    base.Add(time.Minute),
			wantWall:    10,
			wantVCPU:    20,
			wantRAM:     10 * float64(int64(1)<<30),
			wantRunning: false,
		},
		{
			name: "stopped gap does not accrue",
			events: []MeterEvent{
				{SandboxID: "a", Kind: MeterStart, VCPUs: 1, RAMBytes: 1 << 30, At: base},
				{SandboxID: "a", Kind: MeterStop, At: base.Add(10 * time.Second)},
				// 50s stopped here — must not bill.
				{SandboxID: "a", Kind: MeterStart, VCPUs: 1, RAMBytes: 1 << 30, At: base.Add(60 * time.Second)},
				{SandboxID: "a", Kind: MeterStop, At: base.Add(65 * time.Second)},
			},
			reportAt:    base.Add(2 * time.Minute),
			wantWall:    15, // 10 + 5, not 65
			wantVCPU:    15,
			wantRAM:     15 * float64(int64(1)<<30),
			wantRunning: false,
		},
		{
			name: "delete closes the open interval",
			events: []MeterEvent{
				{SandboxID: "a", Kind: MeterStart, VCPUs: 4, RAMBytes: 2 << 30, At: base},
				{SandboxID: "a", Kind: MeterDelete, At: base.Add(30 * time.Second)},
			},
			reportAt:    base.Add(time.Minute),
			wantWall:    30,
			wantVCPU:    120,
			wantRAM:     30 * float64(int64(2)<<30),
			wantRunning: false,
		},
		{
			name: "duplicate start does not double-bill",
			events: []MeterEvent{
				{SandboxID: "a", Kind: MeterStart, VCPUs: 2, RAMBytes: 1 << 30, At: base},
				{SandboxID: "a", Kind: MeterStart, VCPUs: 8, RAMBytes: 8 << 30, At: base.Add(5 * time.Second)},
				{SandboxID: "a", Kind: MeterStop, At: base.Add(10 * time.Second)},
			},
			reportAt:    base.Add(time.Minute),
			wantWall:    10,
			wantVCPU:    20, // the second start is ignored; shape stays 2 vCPU
			wantRAM:     10 * float64(int64(1)<<30),
			wantRunning: false,
		},
		{
			name: "open interval included live in report",
			events: []MeterEvent{
				{SandboxID: "a", Kind: MeterStart, VCPUs: 2, RAMBytes: 1 << 30, At: base},
			},
			reportAt:    base.Add(20 * time.Second),
			wantWall:    20,
			wantVCPU:    40,
			wantRAM:     20 * float64(int64(1)<<30),
			wantRunning: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewUsageLedger()
			l.Now = func() time.Time { return tt.reportAt }
			for _, ev := range tt.events {
				l.Record(ev)
			}
			u, ok := l.Report("a")
			if !ok {
				t.Fatalf("Report(a) missing")
			}
			if u.WallSeconds != tt.wantWall {
				t.Errorf("WallSeconds = %v, want %v", u.WallSeconds, tt.wantWall)
			}
			if u.VCPUSeconds != tt.wantVCPU {
				t.Errorf("VCPUSeconds = %v, want %v", u.VCPUSeconds, tt.wantVCPU)
			}
			if u.RAMByteSeconds != tt.wantRAM {
				t.Errorf("RAMByteSeconds = %v, want %v", u.RAMByteSeconds, tt.wantRAM)
			}
			if u.Running != tt.wantRunning {
				t.Errorf("Running = %v, want %v", u.Running, tt.wantRunning)
			}
		})
	}
}

func TestUsageLedgerReportMissingAndEmptyID(t *testing.T) {
	l := NewUsageLedger()
	if _, ok := l.Report("nope"); ok {
		t.Errorf("Report on unknown id returned ok")
	}
	l.Record(MeterEvent{SandboxID: "", Kind: MeterStart})
	if got := l.Reports(); len(got) != 0 {
		t.Errorf("empty-id event recorded a usage: %v", got)
	}
}

func TestUsageLedgerReportsSorted(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	l := NewUsageLedger()
	for _, id := range []string{"c", "a", "b"} {
		l.Record(MeterEvent{SandboxID: id, Kind: MeterStart, VCPUs: 1, At: base})
		l.Record(MeterEvent{SandboxID: id, Kind: MeterStop, At: base.Add(time.Second)})
	}
	reports := l.Reports()
	if len(reports) != 3 {
		t.Fatalf("Reports len = %d, want 3", len(reports))
	}
	for i, want := range []string{"a", "b", "c"} {
		if reports[i].SandboxID != want {
			t.Errorf("Reports[%d].SandboxID = %q, want %q", i, reports[i].SandboxID, want)
		}
	}
}
