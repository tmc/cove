package guibench

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorpusV0NoOpInvariantAcrossSeeds proves the load-bearing slice-4 invariant
// — a config-only (no-op) run scores 0, a config+solution (good) run scores 1 —
// holds for EVERY materialized parameter value of the two defaults-toggle tasks,
// not just the value the default seed happens to pick.
//
// The slice-4 review caught a seed-dependent miscalibration here: settings-
// appearance and settings-dock-autohide wrote a fixed baseline in config that,
// for one of the two THEME/STATE values, already equaled the goal — so the no-op
// scored 1 at the seed that materialized that value (seed 2), while the default
// seed (1) masked it. The fix derives a per-task BASELINE param that is the
// opposite of the goal, so the config baseline never equals the goal. This test
// runs the full self-check (fork -> config [-> solution] -> evaluate) against a
// stateful fake guest that emulates `defaults` over seeds 1-3, the range the
// review asked to re-verify; a regression to a fixed baseline re-breaks it at the
// seed whose value equals that baseline.
func TestCorpusV0NoOpInvariantAcrossSeeds(t *testing.T) {
	tasks := loadCorpusV0(t)
	byID := make(map[string]*Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	for _, id := range []string{"settings-appearance", "settings-dock-autohide"} {
		task := byID[id]
		if task == nil {
			t.Fatalf("corpus is missing task %q", id)
		}
		for seed := uint64(1); seed <= 3; seed++ {
			env := &defaultsEnv{}
			r := SelfCheck(env, task, seed)
			if r.Err != nil {
				t.Fatalf("%s seed %d: self-check error: %v", id, seed, r.Err)
			}
			if r.Good != 1 {
				t.Errorf("%s seed %d: good run scored %v, want 1 (gold solution must satisfy the verifier)", id, seed, r.Good)
			}
			if r.NoOp != 0 {
				t.Errorf("%s seed %d: NO-OP scored %v, want 0 (config baseline must differ from the goal for this materialized value)", id, seed, r.NoOp)
			}
		}
	}
}

// defaultsEnv hands out a fresh defaultsGuest per Acquire, so the no-op run never
// inherits the good run's mutated preferences (design 047 §6: one fresh fork per
// run). This is what makes the no-op a true no-op against an unmodified baseline.
type defaultsEnv struct{}

func (defaultsEnv) Acquire(string) (SelfCheckSession, error) {
	return &defaultsGuest{prefs: map[string]string{}}, nil
}

// defaultsGuest is a stateful fake guest that emulates the macOS `defaults`
// CLI semantics the two toggle tasks depend on: write sets a key, delete removes
// it, read returns the value or exits nonzero when absent. It interprets the
// `sh -c` conditionals the tasks use (BASELINE/THEME/STATE branches) by shelling
// out to the host /bin/sh for the control flow while substituting `defaults`
// invocations against its in-memory store. cfprefsd flushes are no-ops here (the
// store is already coherent).
type defaultsGuest struct {
	// prefs keys are "<domain>\x00<key>"; com.apple.dock and the global domain
	// (-g/-globalDomain/NSGlobalDomain) are the only domains the tasks touch.
	prefs map[string]string
}

func (g *defaultsGuest) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	// The tasks wrap their logic in `sh -c "<script>"`; resolve the script's
	// control flow on the host shell, but route `defaults`/`killall` lines back
	// through this guest by expanding them to their effect first.
	if len(args) == 3 && args[0] == "sh" && args[1] == "-c" {
		return g.runScript(args[2])
	}
	if len(args) >= 1 && args[0] == "defaults" {
		return g.runDefaults(args[1:])
	}
	if len(args) >= 1 && args[0] == "killall" {
		return 0, "", "", nil // cfprefsd flush: no-op against a coherent store
	}
	return 0, "", "", nil
}

func (g *defaultsGuest) ReadFile(string) ([]byte, error) { return nil, nil }
func (g *defaultsGuest) OCRAllText() (string, error)     { return "", nil }
func (g *defaultsGuest) Probe() Probe                    { return g }
func (g *defaultsGuest) Close() error                    { return nil }

// runScript evaluates a `sh -c` body. The tasks use only `if [ X = Y ]` branches
// over already-materialized literals plus `defaults` and `killall` statements;
// rather than reimplement the shell, run the body under /bin/sh with `defaults`
// shadowed by a function backed by a writable scratch file that mirrors the
// in-memory store. The function reads return the live value (so `|| echo Light`
// works) and writes update the file; on return the file's mutations are folded
// back into the store. `killall` is a no-op (cfprefsd flush against a coherent
// store).
func (g *defaultsGuest) runScript(body string) (int, string, string, error) {
	dir, err := os.MkdirTemp("", "guibench-defaults")
	if err != nil {
		return 1, "", err.Error(), nil
	}
	defer os.RemoveAll(dir)
	store := filepath.Join(dir, "store")
	if err := g.dumpStore(store); err != nil {
		return 1, "", err.Error(), nil
	}

	// The shadow `defaults` resolves global-domain aliases, reads echo the value
	// or exit 1 when absent, writes/deletes rewrite the scratch store. Keyed by a
	// canonical "domain\tkey" line so the host shell never parses plist types.
	preamble := `STORE="` + store + `"
defaults() {
  sub="$1"; shift
  case "$1" in -g|-globalDomain|NSGlobalDomain) dom=NSGlobalDomain; shift;; *) dom="$1"; shift;; esac
  key="$1"; shift
  k="$dom	$key"
  case "$sub" in
    read)
      v=$(grep -F "$k	" "$STORE" 2>/dev/null | head -1 | cut -f3-)
      if grep -qF "$k	" "$STORE" 2>/dev/null; then printf '%s\n' "$v"; return 0; else return 1; fi ;;
    write)
      last=""; for a in "$@"; do last="$a"; done
      grep -vF "$k	" "$STORE" > "$STORE.tmp" 2>/dev/null; mv "$STORE.tmp" "$STORE"
      printf '%s	%s\n' "$k" "$last" >> "$STORE" ;;
    delete)
      grep -vF "$k	" "$STORE" > "$STORE.tmp" 2>/dev/null; mv "$STORE.tmp" "$STORE" ;;
  esac
}
killall() { :; }
`
	out, runErr := exec.Command("/bin/sh", "-c", preamble+body).Output()
	exit := 0
	var stderr string
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
			stderr = string(ee.Stderr)
		} else {
			return 1, "", runErr.Error(), nil
		}
	}
	if err := g.loadStore(store); err != nil {
		return 1, "", err.Error(), nil
	}
	return exit, string(out), stderr, nil
}

// dumpStore writes the in-memory prefs to a tab-separated scratch file the shell
// shadow reads; loadStore folds the shell's mutations back in.
func (g *defaultsGuest) dumpStore(path string) error {
	var b strings.Builder
	for dk, v := range g.prefs {
		dom, key, _ := strings.Cut(dk, "\x00")
		b.WriteString(dom + "\t" + key + "\t" + v + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func (g *defaultsGuest) loadStore(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	g.prefs = map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		g.prefs[parts[0]+"\x00"+parts[1]] = boolNormalize(parts[2])
	}
	return nil
}

// runDefaults handles a bare `defaults ...` argv (the dock task's evaluator reads
// via the "defaults" getter kind, which execs `defaults read <domain> <key>`).
func (g *defaultsGuest) runDefaults(args []string) (int, string, string, error) {
	if len(args) >= 1 && args[0] == "read" {
		return g.readDefaults(args[1:])
	}
	g.applyDefaults(args)
	return 0, "", "", nil
}

// applyDefaults mutates the store for `write`/`delete` argvs (the leading
// `defaults` token already stripped).
func (g *defaultsGuest) applyDefaults(args []string) {
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "write":
		domain, key, rest := parseDomainKey(args[1:])
		if key == "" {
			return
		}
		// rest is e.g. ["-string","Dark"] or ["-bool","true"]; the last token is
		// the value the getter will read back.
		if len(rest) > 0 {
			g.prefs[domain+"\x00"+key] = boolNormalize(rest[len(rest)-1])
		}
	case "delete":
		domain, key, _ := parseDomainKey(args[1:])
		if key == "" {
			return
		}
		delete(g.prefs, domain+"\x00"+key)
	}
}

// readDefaults emulates `defaults read <domain> <key>`: stdout+exit 0 when set,
// nonzero when absent (matching the real CLI the getters rely on).
func (g *defaultsGuest) readDefaults(args []string) (int, string, string, error) {
	domain, key, _ := parseDomainKey(args)
	v, ok := g.prefs[domain+"\x00"+key]
	if !ok {
		return 1, "", "domain/default pair does not exist", nil
	}
	return 0, v + "\n", "", nil
}

// parseDomainKey extracts the domain and key from a `defaults` argv tail,
// canonicalizing the global-domain aliases (-g, -globalDomain, NSGlobalDomain)
// so a write via -g and a read via -g hit the same store entry.
func parseDomainKey(args []string) (domain, key string, rest []string) {
	if len(args) == 0 {
		return "", "", nil
	}
	domain = canonicalDomain(args[0])
	if len(args) >= 2 {
		key = args[1]
	}
	if len(args) > 2 {
		rest = args[2:]
	}
	return domain, key, rest
}

func canonicalDomain(d string) string {
	switch d {
	case "-g", "-globalDomain", "NSGlobalDomain":
		return "NSGlobalDomain"
	default:
		return d
	}
}

// boolNormalize maps the boolean spellings `defaults write -bool` accepts to the
// "1"/"0" the dock task's EXPECTED derive uses, leaving non-boolean string values
// (e.g. the appearance task's "Dark") untouched.
func boolNormalize(v string) string {
	switch strings.ToLower(v) {
	case "true", "yes", "1":
		return "1"
	case "false", "no", "0":
		return "0"
	default:
		return v
	}
}
