package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

type workspaceOptions struct {
	VM, From, OS, Source, Output, SourceMode, Prepare, Retain string
	Timeout                                                   time.Duration
	ReadinessTimeout                                          time.Duration
	MinFreeGiB                                                uint64
	Args                                                      []string
}

type workspacePlan struct {
	TaskTimeoutSeconds      float64 `json:"task_timeout_seconds"`
	ReadinessTimeoutSeconds float64 `json:"readiness_timeout_seconds"`
	RuntimeMaxExecSeconds   int     `json:"runtime_max_exec_seconds"`
	recipeData              map[string][]byte
	Version                 int                  `json:"version"`
	Profile                 string               `json:"profile"`
	VM                      string               `json:"vm"`
	From                    string               `json:"from,omitempty"`
	GuestOS                 string               `json:"guest_os"`
	Source                  vzscriptPlannedMount `json:"source"`
	Output                  vzscriptPlannedMount `json:"output"`
	SourceGuestPath         string               `json:"source_guest_path"`
	OutputGuestPath         string               `json:"output_guest_path"`
	TaskExecutable          string               `json:"task_executable"`
	TaskArgCount            int                  `json:"task_arg_count"`
	GuestRoute              string               `json:"guest_route"`
	Retention               string               `json:"retention"`
	MinFreeGiB              uint64               `json:"min_free_gib"`
	Preparation             vzscriptPlan         `json:"preparation"`
	Unresolved              []string             `json:"unresolved"`
}

const workspaceSourceTag = "cove-workspace-source"
const workspaceOutputTag = "cove-workspace-output"

func runWorkspaceCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleWorkspaceCommand(env.WithDefaultIO(), args))
}

func handleWorkspaceCommand(env commandEnv, args []string) error {
	if len(args) == 0 {
		printWorkspaceUsage(env.Stderr)
		return fmt.Errorf("workspace command required")
	}
	if isHelpArg(args[0]) {
		printWorkspaceUsage(env.Stdout)
		return nil
	}
	if args[0] != "plan" && args[0] != "open" {
		return fmt.Errorf("unknown workspace command %q", args[0])
	}
	opts, asJSON, err := parseWorkspaceOptions(args[1:], env.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	plan, err := planGoWorkspace(opts)
	if err != nil {
		return err
	}
	if args[0] == "plan" {
		return writeWorkspacePlan(env.Stdout, plan, asJSON)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	taskEnv := env
	if asJSON {
		taskEnv.Stdout = env.Stderr
	}
	receipt, err := openGoWorkspace(ctx, opts, plan, defaultWorkspaceDeps(), taskEnv)
	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if e := enc.Encode(receipt); e != nil && err == nil {
			err = e
		}
	} else {
		fmt.Fprintf(env.Stdout, "Workspace %s: %s\nRun: %s\nGuest: %s\n", opts.VM, receipt.Outcome, receipt.Bundle, receipt.GuestDirectory)
		if receipt.Disposition != "" {
			fmt.Fprintf(env.Stdout, "Disposition: %s\n", receipt.Disposition)
		}
	}
	return err
}

func printWorkspaceUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove workspace plan|open -vm NAME -source DIR -output DIR [flags] [-- command args...]

Go workspace profile. Source defaults to read-only; output must be separate.
Uses an existing prepared VM, or creates a fresh fork with -from STOPPED_BASE.
Missing/unprepared base guests must first be prepared with cove up.

Flags:
  -os darwin|linux        declared target guest OS (default darwin)
  -source-mode ro|rw      explicit source access (default ro)
  -prepare recipe,...    opt-in recipes if the user Go check fails
  -retain retain|discard-success  retain failures; discard only a newly owned
                         fork on success (default retain)
  -timeout duration      task timeout (default 10m)
  -readiness-timeout duration  root/user readiness timeout (default 2m; max 30m)
  -min-free-gib N         required guest/output free GiB (default 1)
  -json                  machine-readable plan or run receipt

Default task: go test ./...
Arguments are passed directly to the user agent; no shell concatenation.`)
}

func parseWorkspaceOptions(args []string, w io.Writer) (workspaceOptions, bool, error) {
	var o workspaceOptions
	fs := flag.NewFlagSet("workspace", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.Usage = func() { printWorkspaceUsage(w) }
	fs.StringVar(&o.VM, "vm", "", "guest name")
	fs.StringVar(&o.From, "from", "", "stopped prepared base to fork")
	fs.StringVar(&o.OS, "os", "darwin", "guest OS")
	fs.StringVar(&o.Source, "source", "", "repository directory")
	fs.StringVar(&o.Output, "output", "", "writable output directory")
	fs.StringVar(&o.SourceMode, "source-mode", "ro", "source access")
	fs.StringVar(&o.Prepare, "prepare", "", "opt-in prerequisite recipes")
	fs.StringVar(&o.Retain, "retain", "retain", "retain or discard-success")
	fs.DurationVar(&o.Timeout, "timeout", 10*time.Minute, "task timeout")
	fs.DurationVar(&o.ReadinessTimeout, "readiness-timeout", 2*time.Minute, "root/user readiness timeout")
	fs.Uint64Var(&o.MinFreeGiB, "min-free-gib", 1, "free GiB minimum")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return o, false, err
	}
	o.Args = fs.Args()
	if len(o.Args) == 0 {
		o.Args = []string{"go", "test", "./..."}
	}
	return o, *asJSON, nil
}

func planGoWorkspace(o workspaceOptions) (workspacePlan, error) {
	p := workspacePlan{Version: 1, Profile: "go", VM: o.VM, From: o.From, GuestOS: o.OS, GuestRoute: "user", Retention: o.Retain, MinFreeGiB: o.MinFreeGiB, TaskTimeoutSeconds: o.Timeout.Seconds(), RuntimeMaxExecSeconds: 600}
	p.ReadinessTimeoutSeconds = o.ReadinessTimeout.Seconds()
	if !workspaceValidName(o.VM) || o.From != "" && (!workspaceValidName(o.From) || o.From == o.VM) {
		return p, fmt.Errorf("workspace requires distinct valid VM/base names")
	}
	if o.OS != "darwin" && o.OS != "linux" {
		return p, fmt.Errorf("Go workspace supports darwin or linux guests")
	}
	if o.SourceMode != "ro" && o.SourceMode != "rw" {
		return p, fmt.Errorf("source-mode must be ro or rw")
	}
	if o.Retain != "retain" && o.Retain != "discard-success" {
		return p, fmt.Errorf("retain must be retain or discard-success")
	}
	if o.Retain == "discard-success" && o.From == "" {
		return p, fmt.Errorf("discard-success requires a fresh -from fork; existing workspaces are retained")
	}
	if o.Timeout <= 0 || o.Timeout > 10*time.Minute {
		return p, fmt.Errorf("task timeout must be positive and at most 10m, the runtime execution limit")
	}
	if o.ReadinessTimeout <= 0 || o.ReadinessTimeout > 30*time.Minute {
		return p, fmt.Errorf("readiness timeout must be positive and at most 30m")
	}
	if o.MinFreeGiB == 0 || o.MinFreeGiB > 1024 {
		return p, fmt.Errorf("minimum free space must be between 1 and 1024 GiB")
	}
	if len(o.Args) == 0 || strings.TrimSpace(o.Args[0]) == "" {
		return p, fmt.Errorf("task executable required")
	}
	source, err := normalizeSharedFolderPath(o.Source)
	if err != nil {
		return p, fmt.Errorf("source: %w", err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return p, fmt.Errorf("source: %w", err)
	}
	if strings.TrimSpace(o.Output) == "" {
		return p, fmt.Errorf("output directory required")
	}
	output, err := filepath.Abs(resolvePath(o.Output))
	if err != nil {
		return p, fmt.Errorf("output: %w", err)
	}
	output, err = workspaceResolveNewPath(output)
	if err != nil {
		return p, fmt.Errorf("output: %w", err)
	}
	if workspacePathsOverlap(source, output) {
		return p, fmt.Errorf("source and output must be separate directories without ancestor overlap")
	}
	p.Source = vzscriptPlannedMount{Path: source, ReadOnly: o.SourceMode == "ro"}
	p.Output = vzscriptPlannedMount{Path: output, ReadOnly: false}
	root := "/Volumes/My Shared Files"
	if o.OS == "linux" {
		root = linuxSharedFoldersMountRoot
	}
	p.SourceGuestPath = filepath.Join(root, workspaceSourceTag)
	p.OutputGuestPath = filepath.Join(root, workspaceOutputTag)
	p.TaskExecutable = o.Args[0]
	p.TaskArgCount = len(o.Args) - 1
	p.recipeData = map[string][]byte{}
	p.Preparation = planVZScripts(splitRecipes(o.Prepare), o.OS, func(name string) ([]byte, error) {
		if data, ok := p.recipeData[name]; ok {
			return data, nil
		}
		data, err := loadVZScriptData(name)
		if err == nil {
			p.recipeData[name] = append([]byte(nil), data...)
		}
		return data, err
	})
	if len(p.Preparation.Diagnostics) > 0 {
		return p, fmt.Errorf("workspace preparation has %d static diagnostic(s); use vzscript validate for details", len(p.Preparation.Diagnostics))
	}
	for _, r := range p.Preparation.Recipes {
		if len(r.Mounts) > 0 || len(r.Injections) > 0 {
			return p, fmt.Errorf("workspace preparation with additional mounts/injections is unsupported; prepare the base with existing up/vzscript routes first")
		}
	}
	p.Unresolved = []string{"base/image identity is unknown until verified; no image digest inferred", "runtime ownership and root/user session readiness", "guest Go version and explicit preparation checks", "guest and output filesystem free space", "runtime source/output mounts and declared access", "task outcome; side effects are never retried automatically"}
	return p, nil
}

func workspaceValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func workspacePathsOverlap(a, b string) bool {
	within := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func workspaceResolveNewPath(path string) (string, error) {
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("not a directory")
		}
		return filepath.EvalSymlinks(path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", fmt.Errorf("directory has no existing parent")
	}
	resolved, err := workspaceResolveNewPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

func writeWorkspacePlan(w io.Writer, p workspacePlan, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	fmt.Fprintf(w, "Go workspace %s (%s, user route)\n", p.VM, p.GuestOS)
	fmt.Fprintf(w, "Readiness timeout: %s\nTask timeout: %s\n", time.Duration(p.ReadinessTimeoutSeconds*float64(time.Second)), time.Duration(p.TaskTimeoutSeconds*float64(time.Second)))
	mode := "rw"
	if p.Source.ReadOnly {
		mode = "ro"
	}
	fmt.Fprintf(w, "Source: %s -> %s (%s)\nOutput: %s -> %s (rw)\nTask: %s (%d arguments; values omitted)\nRetention: %s\n", p.Source.Path, p.SourceGuestPath, mode, p.Output.Path, p.OutputGuestPath, p.TaskExecutable, p.TaskArgCount, p.Retention)
	for _, r := range p.Preparation.Recipes {
		fmt.Fprintf(w, "Prepare if needed: %s (%s)\n", r.Source, r.Route)
	}
	for _, u := range p.Unresolved {
		fmt.Fprintf(w, "Unresolved: %s\n", u)
	}
	return nil
}
