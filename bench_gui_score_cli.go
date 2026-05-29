package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tmc/cove/internal/guibench"
)

// runBenchGUIRun scores a corpus across one or more providers and writes the
// citable score.json + Markdown report (design 047 §9 slice 5). The aggregation,
// variance flagging, subset selection, and report rendering are pure transforms
// (unit-tested in internal/guibench); the only step that needs live hardware and
// provider credentials is the per-provider scoring backend, which this command
// constructs through [liveBackend].
func runBenchGUIRun(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui run", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	corpus := fs.String("corpus", "", "task corpus directory (required)")
	providersCSV := fs.String("providers", "", "comma-separated provider list, e.g. anthropic,openai,gemini (required)")
	runs := fs.Int("runs", 3, "attempts per task per provider (pass@1 over N, design 047 §7)")
	report := fs.String("report", "", "write the score report to this path; score.json and .md are written alongside")
	subset := fs.String("subset", "", "restrict to a named task subset, e.g. test_small (CI)")
	image := fs.String("image", "", "override every task's base image with this cove image ref")
	model := fs.String("model", "", "model id recorded in the report metadata")
	seed := fs.Uint64("seed", 1, "deterministic parameter-materialization seed")
	host := fs.String("host", "", "host hardware description for provenance (default: auto-detected)")
	checkpointDir := fs.String("checkpoint-dir", "", "persist per-task results here so a re-run resumes and skips completed tasks")
	var taskIDs stringList
	fs.Var(&taskIDs, "task-id", "restrict to this task id (repeatable); overrides -subset selection")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui run: unexpected arguments: %v", fs.Args())
	}
	if *corpus == "" {
		return fmt.Errorf("bench gui run: -corpus is required")
	}
	if *providersCSV == "" {
		return fmt.Errorf("bench gui run: -providers is required")
	}
	if *runs < 1 {
		return fmt.Errorf("bench gui run: -runs must be at least 1")
	}
	providers := splitProviders(*providersCSV)
	if len(providers) == 0 {
		return fmt.Errorf("bench gui run: -providers lists no provider")
	}

	tasks, err := guibench.Load(*corpus)
	if err != nil {
		return fmt.Errorf("bench gui run: %w", err)
	}
	if len(taskIDs) > 0 {
		tasks, err = selectTaskIDs(tasks, []string(taskIDs))
	} else {
		tasks, err = guibench.SelectSubset(tasks, *subset)
	}
	if err != nil {
		return fmt.Errorf("bench gui run: %w", err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("bench gui run: corpus %s has no tasks", *corpus)
	}

	meta := guibench.Meta{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		CoveCommit:    gitHead("."),
		HostHardware:  hostHardware(*host),
		CorpusVersion: guibench.CorpusVersion(tasks),
		VerifierHash:  guibench.VerifierVersion(),
		Model:         *model,
	}

	fmt.Fprintf(env.Stdout, "corpus %s: %d task(s), subset %q, %d run(s) x %d provider(s)\n",
		*corpus, len(tasks), subsetLabel(*subset), *runs, len(providers))
	fmt.Fprintf(env.Stdout, "corpus version: %s\n", meta.CorpusVersion)
	fmt.Fprintf(env.Stdout, "verifier hash:  %s\n", meta.VerifierHash)

	// The base image must carry the grants the corpus's getters need; the
	// operator certifies that level by provisioning the image (verified by
	// `cove doctor` before save, design 047 §5). The backend is sized to the
	// corpus's MaxTier so CanRun refuses a Tier-B/C getter on an under-granted
	// image rather than reading denied or stale state.
	imageTier := guibench.MaxTier(tasks)

	var checkpoint *guibench.Checkpoint
	if *checkpointDir != "" {
		checkpoint, err = guibench.OpenCheckpoint(*checkpointDir)
		if err != nil {
			return fmt.Errorf("bench gui run: %w", err)
		}
		defer checkpoint.Close()
		fmt.Fprintf(env.Stdout, "checkpoint: %s (resuming %d completed cell(s))\n",
			*checkpointDir, len(checkpoint.Outcomes()))
	}

	ctx := context.Background()
	var outcomes []guibench.Outcome
	for _, p := range providers {
		backend, err := liveBackend(p, imageTier, env.Stderr)
		if err != nil {
			return fmt.Errorf("bench gui run: %w", err)
		}
		provOutcomes, err := guibench.Run(ctx, backend, guibench.RunConfig{
			Tasks:      tasks,
			Provider:   p,
			Model:      *model,
			Runs:       *runs,
			Image:      *image,
			ParamSeed:  *seed,
			Checkpoint: checkpoint,
		})
		if err != nil {
			return fmt.Errorf("bench gui run: provider %s: %w", p, err)
		}
		outcomes = append(outcomes, provOutcomes...)
	}

	rep, err := guibench.Aggregate(outcomes, *runs, meta, nil)
	if err != nil {
		return fmt.Errorf("bench gui run: aggregate: %w", err)
	}
	return writeScoreReport(env, rep, *report)
}

// runBenchGUIReport renders an existing score.json into the citable Markdown +
// JSON table (design 047 §9 slice 5). It is a pure transform: it reads a score
// report and re-emits it, so a result produced on hardware can be re-rendered
// anywhere without a VM.
func runBenchGUIReport(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui report", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	in := fs.String("in", "", "score.json to render (required)")
	markdown := fs.String("markdown", "", "write the Markdown table to this path (default: stdout)")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui report: unexpected arguments: %v", fs.Args())
	}
	if *in == "" {
		return fmt.Errorf("bench gui report: -in is required")
	}
	f, err := os.Open(*in)
	if err != nil {
		return fmt.Errorf("bench gui report: %w", err)
	}
	defer f.Close()
	rep, err := guibench.ReadReport(f)
	if err != nil {
		return fmt.Errorf("bench gui report: %w", err)
	}
	if *markdown == "" {
		return guibench.RenderMarkdown(env.Stdout, rep)
	}
	out, err := os.Create(*markdown)
	if err != nil {
		return fmt.Errorf("bench gui report: %w", err)
	}
	defer out.Close()
	if err := guibench.RenderMarkdown(out, rep); err != nil {
		return fmt.Errorf("bench gui report: %w", err)
	}
	fmt.Fprintf(env.Stdout, "report: %s\n", *markdown)
	return nil
}

// writeScoreReport writes the report to score.json (and a sibling .md) under the
// -report path, or prints the Markdown to stdout when no path is given.
func writeScoreReport(env commandEnv, rep *guibench.ScoreReport, reportPath string) error {
	if reportPath == "" {
		return guibench.RenderMarkdown(env.Stdout, rep)
	}
	jsonPath := scoreJSONPath(reportPath)
	mdPath := scoreMarkdownPath(reportPath)
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return fmt.Errorf("bench gui run: %w", err)
	}
	jf, err := os.Create(jsonPath)
	if err != nil {
		return fmt.Errorf("bench gui run: %w", err)
	}
	if err := rep.WriteJSON(jf); err != nil {
		jf.Close()
		return err
	}
	jf.Close()
	mf, err := os.Create(mdPath)
	if err != nil {
		return fmt.Errorf("bench gui run: %w", err)
	}
	if err := guibench.RenderMarkdown(mf, rep); err != nil {
		mf.Close()
		return err
	}
	mf.Close()
	fmt.Fprintf(env.Stdout, "score json: %s\n", jsonPath)
	fmt.Fprintf(env.Stdout, "report:     %s\n", mdPath)
	return nil
}

// liveBackend constructs the VZ-fork scoring backend for one provider: it forks
// one ephemeral RAM-overlay cove VM per task and drives the provider's
// computer-use loop against it (design 047 §6, §9 slice 2). It is the only part
// of `bench gui run` that needs live Apple-Silicon hardware, the pre-granted
// base image, and provider API credentials; the aggregation and report
// rendering it feeds are pure transforms, unit-tested without a VM. maxTier is
// the grant level the base image carries (verified by `cove doctor` before
// save); the engine's CanRun refuses a corpus that needs more.
//
// Fork chatter and the provider loop's own output go to progress (stderr), so
// the command's stdout stays clean for the score report.
func liveBackend(provider string, maxTier guibench.Tier, progress io.Writer) (guibench.Backend, error) {
	return newVZForkBackend(provider, maxTier, progress, progress)
}

// selectTaskIDs filters tasks to the requested ids, preserving corpus order. An
// id matching no task is an error so a typo fails loudly rather than silently
// scoring fewer tasks (the §9 validator discipline).
func selectTaskIDs(tasks []*guibench.Task, ids []string) ([]*guibench.Task, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []*guibench.Task
	for _, t := range tasks {
		if want[t.ID] {
			out = append(out, t)
			delete(want, t.ID)
		}
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for id := range want {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("task id(s) not in corpus: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// splitProviders parses a comma-separated provider list, trimming and dropping
// empties, lowercasing each entry.
func splitProviders(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// subsetLabel returns a human label for an empty subset.
func subsetLabel(name string) string {
	if name == "" {
		return "(full corpus)"
	}
	return name
}

// scoreJSONPath returns the score.json path for a -report value: if the value
// ends in .json it is used as-is; otherwise score.json is placed inside it (the
// value is treated as a directory).
func scoreJSONPath(reportPath string) string {
	if strings.HasSuffix(reportPath, ".json") {
		return reportPath
	}
	if strings.HasSuffix(reportPath, ".md") {
		return strings.TrimSuffix(reportPath, ".md") + ".json"
	}
	return filepath.Join(reportPath, "score.json")
}

// scoreMarkdownPath returns the Markdown path paired with the score.json.
func scoreMarkdownPath(reportPath string) string {
	if strings.HasSuffix(reportPath, ".md") {
		return reportPath
	}
	if strings.HasSuffix(reportPath, ".json") {
		return strings.TrimSuffix(reportPath, ".json") + ".md"
	}
	return filepath.Join(reportPath, "report.md")
}

// gitHead returns the short commit at HEAD for provenance, or "unknown".
func gitHead(root string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// hostHardware returns the operator-supplied host description, or a coarse
// auto-detected fallback. The fallback names the architecture and CPU count
// only; an operator should pass -host for a citable result (the bench/README.md
// rule records exact hardware).
func hostHardware(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if model := sysctlBrand(); model != "" {
		return model
	}
	return fmt.Sprintf("%s/%s (%d cpu)", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

// sysctlBrand reads the CPU brand string on macOS, empty elsewhere or on error.
func sysctlBrand() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
