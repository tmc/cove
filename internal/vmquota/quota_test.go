package vmquota

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadSave(t *testing.T) {
	dir := t.TempDir()
	want := Quota{CPUs: 4, MemoryGB: 8, DiskGB: 50}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("Load = %#v, want %#v", got, want)
	}
}

func TestLoadMissing(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if got != (Quota{}) {
		t.Fatalf("Load missing = %#v, want zero", got)
	}
}

func TestPositiveValueRejectsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int64
	}{
		{name: "zero"},
		{name: "negative", n: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PositiveValue("cpu", tc.n); err == nil {
				t.Fatal("PositiveValue succeeded, want error")
			}
		})
	}
}

func TestQuotaCheckExceeded(t *testing.T) {
	cap := Quota{CPUs: 4, MemoryGB: 8, DiskGB: 50}
	tests := []struct {
		name string
		req  Quota
	}{
		{name: "cpu", req: Quota{CPUs: 5}},
		{name: "memory", req: Quota{MemoryGB: 9}},
		{name: "disk", req: Quota{DiskGB: 51}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := cap.Check(tc.req); !errors.Is(err, ErrQuotaExceeded) {
				t.Fatalf("Check() error = %v, want ErrQuotaExceeded", err)
			}
		})
	}
}

func TestQuotaUpdateOverwritesSavedCap(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Quota{CPUs: 2, MemoryGB: 4, DiskGB: 20}); err != nil {
		t.Fatalf("Save initial: %v", err)
	}
	want := Quota{CPUs: 4, MemoryGB: 8, DiskGB: 40}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load update: %v", err)
	}
	if got != want {
		t.Fatalf("Load update = %#v, want %#v", got, want)
	}
	if err := got.Check(Quota{CPUs: 4, MemoryGB: 8, DiskGB: 40}); err != nil {
		t.Fatalf("Check updated cap: %v", err)
	}
}

func TestApplyAPFSQuotaCommand(t *testing.T) {
	vmDir := filepath.Join(t.TempDir(), "vm")
	runner := &recordRunner{}
	if err := ApplyAPFSQuotaWithRunner(vmDir, 50, runner); err != nil {
		t.Fatalf("ApplyAPFSQuotaWithRunner: %v", err)
	}
	if runner.name != "diskutil" {
		t.Fatalf("command = %q, want diskutil", runner.name)
	}
	wantArgs := []string{"apfs", "setQuota", vmDir, "50g"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestApplyAPFSQuotaRejectsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		vmDir string
		gb    uint64
	}{
		{name: "empty dir", gb: 1},
		{name: "zero gb", vmDir: t.TempDir()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ApplyAPFSQuotaWithRunner(tc.vmDir, tc.gb, &recordRunner{}); err == nil {
				t.Fatal("ApplyAPFSQuotaWithRunner succeeded, want error")
			}
		})
	}
}

func TestApplyAPFSQuotaLiveSkipped(t *testing.T) {
	if _, err := exec.LookPath("diskutil"); err != nil {
		t.Skip("diskutil unavailable")
	}
	if os.Geteuid() != 0 {
		t.Skip("diskutil apfs setQuota live test requires root")
	}
	if testing.Short() {
		t.Skip("skipping live diskutil command in short mode")
	}
	if err := ApplyAPFSQuota(t.TempDir(), 1); err != nil {
		t.Skipf("diskutil apfs setQuota requires APFS quota support/root: %v", err)
	}
}

type recordRunner struct {
	name string
	args []string
	err  error
}

func (r *recordRunner) Run(name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	if r.err != nil {
		return []byte("runner failed"), r.err
	}
	return nil, nil
}

func TestApplyAPFSQuotaIncludesOutput(t *testing.T) {
	err := ApplyAPFSQuotaWithRunner(t.TempDir(), 2, &recordRunner{err: errors.New("exit status 1")})
	if err == nil {
		t.Fatal("ApplyAPFSQuotaWithRunner succeeded, want error")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "runner failed") {
		t.Fatalf("error = %q, want command output", got)
	}
}
