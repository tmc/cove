package guibench

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// corpusV1Dir is the parity-floor expansion corpus: cove-original native-macOS
// tasks that deliberately exercise the fidelity layer (AX-tree, before/after
// SQLite integrity, image-SSIM, URL normalization) and broaden app coverage to
// Mail/Calendar/Reminders/Contacts/Maps/TextEdit/Calculator. Like corpus-v0 it
// loads and validates without a VM, so this whole file is part of the gate on
// non-Apple-Silicon CI.
const corpusV1Dir = "testdata/corpus-v1"

func loadCorpusV1(t *testing.T) []*Task {
	t.Helper()
	tasks, err := Load(corpusV1Dir)
	if err != nil {
		t.Fatalf("Load(%s): %v", corpusV1Dir, err)
	}
	return tasks
}

func TestCorpusV1LoadsAndValidates(t *testing.T) {
	tasks := loadCorpusV1(t)
	// A strong expansion batch toward the 116-154 parity floor (design 047 §9);
	// corpus scale continues beyond this run, so only a floor is asserted.
	if len(tasks) < 25 {
		t.Fatalf("corpus-v1 has %d tasks, want at least 25", len(tasks))
	}
	// Load already validates each record (unknown metric, missing conj, unknown
	// getter kind); reaching here means every record is structurally sound.
}

func TestCorpusV1MetricsRegistered(t *testing.T) {
	known := metricNames()
	for _, task := range loadCorpusV1(t) {
		for _, name := range task.Evaluator.Func {
			if !known[name] {
				t.Errorf("task %s uses unregistered metric %q", task.ID, name)
			}
		}
	}
}

func TestCorpusV1TiersDeclared(t *testing.T) {
	valid := map[Tier]bool{TierA: true, TierB: true, TierC: true}
	for _, task := range loadCorpusV1(t) {
		if tier := task.Evaluator.Result.Tier(); !valid[tier] {
			t.Errorf("task %s: result getter kind %q has undeclared tier %q",
				task.ID, task.Evaluator.Result.Kind, tier)
		}
		if task.Evaluator.Expected != nil {
			if et := task.Evaluator.Expected.Tier(); !valid[et] {
				t.Errorf("task %s: expected getter kind %q has undeclared tier %q",
					task.ID, task.Evaluator.Expected.Kind, et)
			}
		}
	}
}

func TestCorpusV1NoAppleIDTasks(t *testing.T) {
	// v1 still excludes iCloud/Keychain/Apple-ID tasks (shared-SEP hazard,
	// design 047 §6).
	forbidden := []string{"icloud", "keychain", "apple id", "apple-id", "find my", "fairplay"}
	for _, task := range loadCorpusV1(t) {
		lower := strings.ToLower(task.Instruction)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("task %s instruction mentions forbidden surface %q (shared-SEP hazard, §6)", task.ID, f)
			}
		}
	}
}

func TestCorpusV1SelfCheckable(t *testing.T) {
	for _, task := range loadCorpusV1(t) {
		if err := task.CheckSelfCheckable(); err != nil {
			t.Errorf("task %s is not self-checkable: %v", task.ID, err)
		}
		if task.Infeasible && len(task.Solution) != 0 {
			t.Errorf("task %s is infeasible but carries a solution", task.ID)
		}
	}
}

func TestCorpusV1Parameterized(t *testing.T) {
	for _, task := range loadCorpusV1(t) {
		const seed = 3
		a := task.Params(seed)
		b := task.Params(seed)
		for k, v := range a {
			if b[k] != v {
				t.Errorf("task %s: Params(%d)[%q] not deterministic: %q vs %q", task.ID, seed, k, v, b[k])
			}
		}
		for _, p := range task.Schema {
			if a[p.Name] == "" {
				t.Errorf("task %s: param %q resolved to empty for seed %d", task.ID, p.Name, seed)
			}
		}
	}
}

func TestCorpusV1NewCapabilitiesExercised(t *testing.T) {
	// The expansion's reason to exist is the fidelity layer: assert the new
	// metrics each appear on at least one task, so a regression that drops a
	// fidelity task is caught here rather than in a silent coverage gap.
	want := []string{
		"accessibility_match",
		"rows_added_integrity",
		"rows_removed_integrity",
		"image_similar",
		"url_match_normalized",
		"pdf_contains",
	}
	seen := map[string]bool{}
	for _, task := range loadCorpusV1(t) {
		for _, name := range task.Evaluator.Func {
			seen[name] = true
		}
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("corpus-v1 exercises no task with the %q metric", name)
		}
	}
}

func TestCorpusV1HasComposite(t *testing.T) {
	// At least two AND/OR composite tasks (multi-metric evaluator with a conj).
	composites := 0
	for _, task := range loadCorpusV1(t) {
		if len(task.Evaluator.Func) > 1 && task.Evaluator.Conj != "" {
			composites++
		}
	}
	if composites < 2 {
		t.Fatalf("corpus-v1 has %d AND/OR composite tasks, want at least 2", composites)
	}
}

func TestCorpusV1AppCoverage(t *testing.T) {
	// The expansion adds native first-party apps the v0 corpus lacked.
	want := []string{"Reminders", "Calendar", "Contacts", "Mail", "Maps"}
	have := make(map[string]bool)
	for _, task := range loadCorpusV1(t) {
		if task.Domain == "" {
			t.Errorf("task %s has no domain", task.ID)
		}
		have[task.Domain] = true
	}
	for _, d := range want {
		if !have[d] {
			t.Errorf("corpus-v1 is missing the %q domain", d)
		}
	}
}

// hostSeedInvariantTasks are the corpus-v1 tasks whose Config, Solution, and
// getters route entirely through host-available CLIs (sqlite3, sips, defaults,
// stat, chmod, ditto, textutil, file ops) and so can be self-checked end-to-end
// against a host-shell fake guest WITHOUT a VM. AppleScript/AX tasks (live apps)
// are proven by the operator's live selfcheck on hardware, not here.
var hostSeedInvariantTasks = map[string]bool{
	"terminal-sqlite-add-integrity":    true,
	"terminal-sqlite-remove-integrity": true,
	"terminal-plist-write":             true,
	"terminal-chmod-bits":              true,
	"settings-natural-scroll":          true,
	"preview-grayscale-ssim":           true,
	"finder-zip-archive":               true,
	"textedit-create-rtf":              true,
}

// TestCorpusV1HostSeedInvariant proves the load-bearing slice-4 invariant —
// gold solution scores 1, no-op scores 0 — for every materialized parameter
// value of the host-runnable tasks, across seeds 1-4. It runs the full
// self-check (fork -> config [-> solution] -> evaluate) against a stateful
// host-shell fake guest, so a seed whose baseline accidentally equals the goal
// (the v0 regression this discipline guards) re-breaks it. This is the
// before/after integrity proof the brief asks for: deleting/adding the wrong
// rows, or a no-op, scores 0.
func TestCorpusV1HostSeedInvariant(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not on host PATH")
	}
	tasks := loadCorpusV1(t)
	byID := make(map[string]*Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	for id := range hostSeedInvariantTasks {
		task := byID[id]
		if task == nil {
			t.Fatalf("corpus-v1 is missing host-runnable task %q", id)
		}
		for seed := uint64(1); seed <= 4; seed++ {
			env := &hostEnv{}
			r := SelfCheck(env, task, seed)
			if r.Err != nil {
				t.Fatalf("%s seed %d: self-check error: %v", id, seed, r.Err)
			}
			if r.Good != 1 {
				t.Errorf("%s seed %d: good run scored %v, want 1 (gold solution must satisfy the verifier)", id, seed, r.Good)
			}
			if r.NoOp != 0 {
				t.Errorf("%s seed %d: NO-OP scored %v, want 0 (config baseline must differ from the goal)", id, seed, r.NoOp)
			}
		}
	}
}

// hostEnv hands out a fresh hostGuest per Acquire (a fresh HOME temp dir per
// run), so the no-op run never inherits the good run's filesystem or db state —
// the per-fork isolation design 047 §6 requires for a true no-op.
type hostEnv struct{}

func (hostEnv) Acquire(string) (SelfCheckSession, error) {
	dir, err := os.MkdirTemp("", "guibench-v1-home")
	if err != nil {
		return nil, err
	}
	return &hostGuest{home: dir}, nil
}

// hostGuest runs a task's `sh -c` Config/Solution scripts and exec/file/sqlite/
// defaults getters on the host, with $HOME pinned to a per-run scratch dir so
// real sqlite3/sips/stat/etc. operate on isolated state. The `defaults` CLI is
// shadowed by a shell function over a scratch store (the v0 pattern), so a host
// without the real preferences system still gives coherent read-after-write.
type hostGuest struct{ home string }

func (g *hostGuest) Probe() Probe { return g }
func (g *hostGuest) Close() error { return os.RemoveAll(g.home) }

func (g *hostGuest) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(g.resolve(path))
}
func (g *hostGuest) OCRAllText() (string, error) { return "", nil }

// resolve rewrites a guest absolute path under /Users/tmc to the scratch HOME so
// a getter's literal /Users/tmc/... path reads the file the script wrote.
func (g *hostGuest) resolve(path string) string {
	if rest, ok := strings.CutPrefix(path, "/Users/tmc/"); ok {
		return filepath.Join(g.home, rest)
	}
	return path
}

func (g *hostGuest) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	switch {
	case len(args) == 3 && args[0] == "sh" && args[1] == "-c":
		return g.runShell(args[2])
	case len(args) >= 1 && args[0] == "sqlite3":
		// Rewrite the literal db path under HOME, run real sqlite3.
		rw := append([]string{}, args...)
		rw[1] = g.resolve(rw[1])
		return g.runShell(shellJoin(rw))
	case len(args) >= 1 && args[0] == "defaults":
		return g.runShell(shellJoin(args))
	case len(args) >= 1 && args[0] == "killall":
		return 0, "", "", nil
	default:
		return g.runShell(shellJoin(args))
	}
}

// runShell executes a script body under /bin/sh with HOME pinned and `defaults`
// shadowed against a scratch store, so read-after-write is coherent host-side.
func (g *hostGuest) runShell(body string) (int, string, string, error) {
	store := filepath.Join(g.home, ".defaults-store")
	preamble := `export HOME="` + g.home + `"
STORE="` + store + `"
touch "$STORE"
defaults() {
  if [ "$1" = -currentHost ]; then shift; fi
  sub="$1"; shift
  if [ "$1" = -currentHost ]; then shift; fi
  case "$1" in -g|-globalDomain|NSGlobalDomain) dom=NSGlobalDomain; shift;; *) dom="$1"; shift;; esac
  key="$1"; shift
  k="$dom	$key"
  case "$sub" in
    read)
      if grep -qF "$k	" "$STORE" 2>/dev/null; then grep -F "$k	" "$STORE" | head -1 | cut -f3-; return 0; else return 1; fi ;;
    write)
      last=""; for a in "$@"; do last="$a"; done
      grep -vF "$k	" "$STORE" > "$STORE.tmp" 2>/dev/null; mv "$STORE.tmp" "$STORE"
      case "$last" in true|yes|TRUE|YES) last=1;; false|no|FALSE|NO) last=0;; esac
      printf '%s	%s\n' "$k" "$last" >> "$STORE" ;;
    delete)
      grep -vF "$k	" "$STORE" > "$STORE.tmp" 2>/dev/null; mv "$STORE.tmp" "$STORE" ;;
  esac
}
killall() { :; }
`
	cmd := exec.Command("/bin/sh", "-c", preamble+body)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return 1, "", err.Error(), nil
		}
	}
	return exit, stdout.String(), stderr.String(), nil
}

// shellJoin single-quotes each argv entry so a bare `defaults`/`sqlite3` argv
// runs verbatim under /bin/sh -c with the shadowed builtins.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}
