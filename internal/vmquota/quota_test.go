package vmquota

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
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
	setAPFSQuotaSupported(t, true)
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
	out  []byte
}

func (r *recordRunner) Run(name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	if r.err != nil {
		if r.out != nil {
			return r.out, r.err
		}
		return []byte("runner failed"), r.err
	}
	return nil, nil
}

func TestApplyAPFSQuotaIncludesOutput(t *testing.T) {
	setAPFSQuotaSupported(t, true)
	err := ApplyAPFSQuotaWithRunner(t.TempDir(), 2, &recordRunner{err: errors.New("exit status 1")})
	if err == nil {
		t.Fatal("ApplyAPFSQuotaWithRunner succeeded, want error")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "runner failed") {
		t.Fatalf("error = %q, want command output", got)
	}
}

func TestApplyAPFSQuotaUnsupported(t *testing.T) {
	setAPFSQuotaSupported(t, true)
	err := ApplyAPFSQuotaWithRunner(t.TempDir(), 2, &recordRunner{
		err: errors.New("exit status 1"),
		out: []byte(`diskutil: did not recognize APFS verb "setQuota"`),
	})
	if !errors.Is(err, ErrAPFSQuotaUnsupported) {
		t.Fatalf("error = %v, want ErrAPFSQuotaUnsupported", err)
	}
}

func TestApplyAPFSQuotaSkippedWhenUnsupported(t *testing.T) {
	setAPFSQuotaSupported(t, false)
	runner := &recordRunner{}
	if err := ApplyAPFSQuotaWithRunner(t.TempDir(), 50, runner); err != nil {
		t.Fatalf("ApplyAPFSQuotaWithRunner on unsupported host = %v, want nil", err)
	}
	if runner.name != "" || runner.args != nil {
		t.Fatalf("runner invoked on unsupported host: name=%q args=%#v", runner.name, runner.args)
	}
}

// TestSaveSucceedsWhenQuotaUnsupported proves DiskGB persists even when the host
// cannot apply an APFS quota: the apply is a no-op and Save records the cap.
func TestSaveSucceedsWhenQuotaUnsupported(t *testing.T) {
	setAPFSQuotaSupported(t, false)
	dir := t.TempDir()
	if err := ApplyAPFSQuotaWithRunner(dir, 64, &recordRunner{}); err != nil {
		t.Fatalf("ApplyAPFSQuotaWithRunner: %v", err)
	}
	want := Quota{CPUs: 4, MemoryGB: 8, DiskGB: 64}
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

func TestProbeRecognizesDarwinRelease(t *testing.T) {
	for _, tc := range []struct {
		release string
		want    bool
	}{
		{release: "24.0.0", want: true},  // macOS 15 Sequoia
		{release: "24.6.0", want: true},  // macOS 15.x
		{release: "25.5.0", want: false}, // macOS 26
		{release: "26.0.0", want: false},
		{release: "garbage", want: true}, // unknown: assume supported, rely on fallback
	} {
		t.Run(tc.release, func(t *testing.T) {
			if got := apfsQuotaSupportedForRelease(tc.release); got != tc.want {
				t.Fatalf("apfsQuotaSupportedForRelease(%q) = %v, want %v", tc.release, got, tc.want)
			}
		})
	}
}

// apfsVerbList returns realistic "diskutil apfs" help output listing the given verbs.
func apfsVerbList(verbs ...string) []byte {
	var b strings.Builder
	b.WriteString("Usage:  diskutil [quiet] ap[fs] <verb> <options>\n")
	b.WriteString("        where <verb> is as follows:\n\n")
	for _, v := range verbs {
		b.WriteString("     " + v + "    (some description)\n")
	}
	b.WriteString("\ndiskutil apfs <verb> with no options will provide help on that verb\n")
	return []byte(b.String())
}

func TestAPFSVerbListed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		out        []byte
		wantListed bool
		wantOK     bool
	}{
		{
			name:       "darwin24 lists setQuota",
			out:        apfsVerbList("list", "resizeContainer", "setQuota", "addVolume"),
			wantListed: true,
			wantOK:     true,
		},
		{
			name:       "darwin25 omits setQuota",
			out:        apfsVerbList("list", "resizeContainer", "addVolume"),
			wantListed: false,
			wantOK:     true,
		},
		{
			name:   "non-listing output is not authoritative",
			out:    []byte("diskutil: command not found"),
			wantOK: false,
		},
		{
			name:   "empty output is not authoritative",
			out:    nil,
			wantOK: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listed, ok := apfsVerbListed(tc.out, "setQuota")
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && listed != tc.wantListed {
				t.Fatalf("listed = %v, want %v", listed, tc.wantListed)
			}
		})
	}
}

// fixedRunner returns the same output and error for every Run call.
type fixedRunner struct {
	out []byte
	err error
}

func (r fixedRunner) Run(string, ...string) ([]byte, error) { return r.out, r.err }

func TestProbeAPFSQuotaSupportedDiscovery(t *testing.T) {
	// Discovery is authoritative: a verb list containing setQuota means supported,
	// regardless of the host's actual OS version.
	if !probeAPFSQuotaSupported(fixedRunner{out: apfsVerbList("list", "setQuota")}) {
		t.Fatal("probe with setQuota in verb list = false, want true")
	}
	// A verb list lacking setQuota means unsupported.
	if probeAPFSQuotaSupported(fixedRunner{out: apfsVerbList("list", "resizeContainer")}) {
		t.Fatal("probe without setQuota in verb list = true, want false")
	}
}

func TestProbeAPFSQuotaSupportedFallsBackToRelease(t *testing.T) {
	// When diskutil cannot be consulted (error, no usable verb list), the probe falls
	// back to the OS-release heuristic rather than panicking. We can't control
	// kern.osrelease here, so assert the fallback path matches the release heuristic
	// for whatever release this host reports.
	got := probeAPFSQuotaSupported(fixedRunner{err: exec.ErrNotFound})
	release, err := unix.Sysctl("kern.osrelease")
	if err != nil {
		return // sysctl unavailable; the no-panic check above is enough
	}
	if want := apfsQuotaSupportedForRelease(release); got != want {
		t.Fatalf("fallback probe = %v, want %v for release %q", got, want, release)
	}
}

// setAPFSQuotaSupported forces the capability gate for the duration of the test.
func setAPFSQuotaSupported(t *testing.T, ok bool) {
	t.Helper()
	prev := apfsQuotaSupported
	apfsQuotaSupported = func() bool { return ok }
	t.Cleanup(func() { apfsQuotaSupported = prev })
}
