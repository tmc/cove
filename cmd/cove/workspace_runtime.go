package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/runs"
	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type workspaceReceipt struct {
	RunID          string   `json:"run_id"`
	Bundle         string   `json:"bundle"`
	GuestDirectory string   `json:"guest_directory"`
	Outcome        string   `json:"outcome"`
	FailedStep     string   `json:"failed_step,omitempty"`
	Disposition    string   `json:"disposition"`
	CleanupErrors  []string `json:"cleanup_errors,omitempty"`
}

type workspaceDeps struct {
	SourceDirectory      func(string) (string, error)
	IdentifyGuest        func(string) (taskGuestIdentity, error)
	PinOwned             func(taskDisposition, string) error
	Resolve              func(workspacePlan) (string, bool, error)
	Fork                 func(string, string) error
	ForkOwned            func(string, string, taskGuestIdentity) (taskGuestIdentity, error)
	HostStorage          func(workspacePlan) error
	ConfigureShares      func(workspacePlan, string) error
	Start                func(workspacePlan, string) error
	StartOwned           func(workspacePlan, string, string) error
	Ready                func(context.Context, workspacePlan, string) ([]byte, error)
	GuestStorage         func(workspacePlan, string) error
	GoCheck              func(context.Context, workspacePlan, string) (string, error)
	Prepare              func(context.Context, workspacePlan, string) error
	Mount                func(context.Context, workspacePlan, string) error
	Task                 func(context.Context, workspaceOptions, workspacePlan, string, commandEnv) (*controlpb.AgentExecResponse, error)
	Capture              func(context.Context, workspacePlan, string) ([]byte, error)
	Discard              func(workspacePlan, string) error
	DiscardOwned         func(context.Context, taskDisposition, string) error
	FinalizeDiscardOwned func(taskDisposition, string) error
	RunsRoot             string
}

func openGoWorkspace(ctx context.Context, o workspaceOptions, p workspacePlan, d workspaceDeps, env commandEnv) (receipt workspaceReceipt, primary error) {
	b, err := NewRunBundle(d.RunsRoot, p.VM, p.From)
	if err != nil {
		return receipt, err
	}
	receipt = workspaceReceipt{RunID: b.ID(), Bundle: b.Dir(), Disposition: "retained"}
	planData, _ := json.Marshal(p)
	planHash := sha256.Sum256(planData)
	if err := b.ConfigureTask(runs.Task{PlanDigest: "sha256:" + hex.EncodeToString(planHash[:]), Kind: "go-workspace", GuestRoute: "user", Backend: "virtualization", Retention: p.Retention, Inputs: []runs.Input{{Name: "source"}, {Name: "output"}, {Name: "task-argv", Secret: true}}}); err != nil {
		return receipt, err
	}
	source := ""
	if p.From != "" {
		if d.SourceDirectory == nil {
			return receipt, fmt.Errorf("workspace source identity dependency unavailable")
		}
		source, err = d.SourceDirectory(p.From)
		if err != nil {
			return receipt, fmt.Errorf("resolve workspace source identity: %w", err)
		}
	}
	if err := b.AppendTaskEvent(runs.TaskEvent{Kind: "disposition", Status: "started"}); err != nil {
		return receipt, fmt.Errorf("initialize workspace receipt: %w", err)
	}
	journal, err := newTaskDispositionJournal(b.Dir(), b.ID(), source, p.Retention)
	if err != nil {
		return receipt, fmt.Errorf("create task disposition: %w", err)
	}
	defer journal.Close()
	var guest *taskGuestIdentity
	taskSucceeded := false
	journalFailed := false
	var dir string
	created := false
	persist := func(state string) error {
		if journalFailed {
			return fmt.Errorf("task disposition persistence unavailable")
		}
		if err := journal.transition(state, guest, created && guest != nil, taskSucceeded); err != nil {
			journalFailed = true
			receipt.CleanupErrors = append(receipt.CleanupErrors, "task disposition could not be persisted; guest retained")
			return fmt.Errorf("persist task disposition: %w", err)
		}
		return nil
	}
	step := func(name string, fn func() error) error {
		if err := ctx.Err(); err != nil {
			receipt.FailedStep = name
			return err
		}
		started := time.Now()
		if env.Stderr != nil {
			fmt.Fprintf(env.Stderr, "workspace: %s\n", name)
		}
		if err := b.AppendTaskEvent(runs.TaskEvent{Kind: "step", StepID: name, Status: "started"}); err != nil {
			receipt.FailedStep = name
			return err
		}
		err := fn()
		status := "success"
		if err != nil {
			status = "failed"
			receipt.FailedStep = name
		}
		if recordErr := b.AppendTaskEvent(runs.TaskEvent{Kind: "step", StepID: name, Status: status, DurationMS: time.Since(started).Milliseconds()}); recordErr != nil {
			if err == nil {
				err = recordErr
				receipt.FailedStep = name
			} else {
				receipt.CleanupErrors = append(receipt.CleanupErrors, "could not record failed step")
			}
		}
		return err
	}
	defer func() {
		receipt.GuestDirectory = dir
		if receipt.FailedStep == "task" || primary == nil {
			provenance := workspaceSourceProvenance(context.Background(), p.Source.Path)
			if e := b.RecordArtifact(runs.Artifact{Name: "source-after", Status: "present", Path: "artifacts/source-after.json", ContentType: "application/json"}, provenance); e != nil {
				receipt.CleanupErrors = append(receipt.CleanupErrors, "final source provenance could not be recorded")
			}
		}
		outcome := "success"
		if primary != nil {
			outcome = "prerequisite_failure"
			if receipt.FailedStep == "task" {
				outcome = "task_failure"
			}
			if errors.Is(primary, context.Canceled) {
				outcome = "canceled"
			}
			if errors.Is(primary, context.DeadlineExceeded) {
				outcome = "timed_out"
			}
		}
		if primary != nil && dir != "" {
			captureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			data, captureErr := d.Capture(captureCtx, p, dir)
			cancel()
			a := runs.Artifact{Name: "failure-state", Status: "present", Path: "artifacts/failure-state.json", ContentType: "application/json"}
			if captureErr != nil {
				a.Status = "failed"
				a.Path = ""
				a.Reason = "failure state unavailable within capture budget"
				data = nil
			}
			if e := b.RecordArtifact(a, data); e != nil {
				receipt.CleanupErrors = append(receipt.CleanupErrors, "failure evidence could not be recorded")
			}
		}
		if !journalFailed {
			if e := persist("collecting"); e != nil && primary == nil {
				primary = e
			}
		}
		terminal := "retained"
		if primary == nil && len(receipt.CleanupErrors) == 0 && created && p.Retention == "discard-success" {
			if e := persist("stopping"); e != nil {
				primary = e
			} else if e := func() error {
				if d.DiscardOwned != nil {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					defer cancel()
					return d.DiscardOwned(cleanupCtx, journal.state, journal.state.Generation)
				}
				return d.Discard(p, dir)
			}(); e != nil {
				receipt.CleanupErrors = append(receipt.CleanupErrors, "owned guest cleanup could not be verified; inspect the disposition receipt")
				receipt.Disposition = "cleanup_unknown"
				terminal = "cleanup_unknown"
			} else {
				terminal = "discarded"
				receipt.Disposition = "discarded"
			}
		}
		if !journalFailed {
			if e := persist(terminal); e != nil {
				if primary == nil {
					primary = e
				}
				receipt.Disposition = "cleanup_unknown"
			} else if terminal == "discarded" && d.FinalizeDiscardOwned != nil {
				if e := d.FinalizeDiscardOwned(journal.state, journal.state.Generation); e != nil {
					receipt.CleanupErrors = append(receipt.CleanupErrors, "guest discarded but task protection could not be released")
				}
			}
		}
		if len(receipt.CleanupErrors) > 0 && outcome == "success" {
			outcome = "cleanup_incomplete"
		}
		receipt.Outcome = outcome
		if e := b.FinalizeTask(outcome, primary, receipt.CleanupErrors); e != nil {
			if primary == nil {
				primary = fmt.Errorf("finalize workspace receipt: %w", e)
			}
			receipt.Outcome = "cleanup_incomplete"
		}
		if primary == nil && len(receipt.CleanupErrors) > 0 {
			primary = fmt.Errorf("workspace task completed but cleanup is incomplete; inspect the disposition receipt")
		}
	}()
	if err := step("resolve", func() error {
		var exists bool
		var e error
		dir, exists, e = d.Resolve(p)
		if e != nil {
			return e
		}
		if exists && p.From != "" {
			return fmt.Errorf("workspace VM already exists; omit -from to reuse it or choose a fresh name")
		}
		if !exists {
			if p.From == "" {
				return fmt.Errorf("workspace VM does not exist; prepare a base with cove up, then use -from or an existing guest")
			}
			if d.ForkOwned != nil {
				if journal.state.SourceGuest == nil {
					return fmt.Errorf("recorded workspace source identity unavailable")
				}
				identity, err := d.ForkOwned(p.From, p.VM, *journal.state.SourceGuest)
				if err != nil {
					return fmt.Errorf("create owned workspace fork: %w", err)
				}
				if !validTaskGuestIdentity(&identity) {
					return fmt.Errorf("created workspace guest identity unavailable")
				}
				guest = &identity
			} else if e = d.Fork(p.From, p.VM); e != nil {
				return fmt.Errorf("create workspace fork: %w", e)
			}
			created = true
			dir, exists, e = d.Resolve(workspacePlan{VM: p.VM, GuestOS: p.GuestOS})
			if e != nil {
				return e
			}
			if !exists {
				return fmt.Errorf("created workspace guest is unavailable")
			}
		}
		identify := d.IdentifyGuest
		if identify == nil {
			identify = identifyTaskGuest
		}
		identity, e := identify(dir)
		if e != nil {
			return fmt.Errorf("identify workspace guest: %w", e)
		}
		if guest != nil && identity != *guest {
			return fmt.Errorf("created workspace guest identity changed before preparation")
		}
		if guest == nil {
			guest = &identity
		}
		if err := persist("preparing"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return receipt, err
	}
	if created && d.PinOwned != nil {
		if err := step("protect", func() error {
			return d.PinOwned(journal.state, b.Dir())
		}); err != nil {
			return receipt, fmt.Errorf("protect owned workspace guest: %w", err)
		}
	}
	if err := step("host-storage", func() error { return d.HostStorage(p) }); err != nil {
		return receipt, err
	}
	if err := step("configure-shares", func() error { return d.ConfigureShares(p, dir) }); err != nil {
		return receipt, err
	}
	if err := step("start", func() error {
		if created && d.StartOwned != nil {
			return d.StartOwned(p, dir, journal.state.Generation)
		}
		return d.Start(p, dir)
	}); err != nil {
		return receipt, err
	}
	if err := step("readiness", func() error {
		data, e := d.Ready(ctx, p, dir)
		if e != nil {
			return e
		}
		if e := b.RecordArtifact(runs.Artifact{Name: "readiness", Status: "present", Path: "artifacts/readiness.json", ContentType: "application/json"}, data); e != nil {
			return e
		}
		return persist("ready")
	}); err != nil {
		return receipt, err
	}
	if err := step("guest-storage", func() error { return d.GuestStorage(p, dir) }); err != nil {
		return receipt, err
	}
	if err := step("go", func() error {
		version, e := d.GoCheck(ctx, p, dir)
		if e != nil {
			if len(p.Preparation.Recipes) == 0 {
				return fmt.Errorf("user go readiness failed: %w", e)
			}
			if e := d.Prepare(ctx, p, dir); e != nil {
				return fmt.Errorf("workspace preparation: %w", e)
			}
			version, e = d.GoCheck(ctx, p, dir)
			if e != nil {
				return e
			}
		}
		data, _ := json.Marshal(map[string]string{"go_version": version, "route": "user"})
		return b.RecordArtifact(runs.Artifact{Name: "tool-versions", Status: "present", Path: "artifacts/tool-versions.json", ContentType: "application/json"}, data)
	}); err != nil {
		return receipt, err
	}

	if err := step("mount", func() error { return d.Mount(ctx, p, dir) }); err != nil {
		return receipt, err
	}
	if err := step("source-provenance", func() error {
		return b.RecordArtifact(runs.Artifact{Name: "source-before", Status: "present", Path: "artifacts/source-before.json", ContentType: "application/json"}, workspaceSourceProvenance(ctx, p.Source.Path))
	}); err != nil {
		return receipt, err
	}
	if err := step("task", func() error {
		if err := persist("executing"); err != nil {
			return err
		}
		var result *controlpb.AgentExecResponse
		var e error
		result, e = d.Task(ctx, o, p, dir, env)
		var taskErr error
		if e != nil {
			switch {
			case ctx.Err() != nil:
				taskErr = fmt.Errorf("task observation canceled; guest command may still be running and is retained: %w", ctx.Err())
			case errors.Is(e, context.DeadlineExceeded):
				taskErr = fmt.Errorf("task observation timed out; guest command may still be running and is retained: %w", e)
			default:
				taskErr = fmt.Errorf("task result unavailable; outcome unknown and guest retained; do not retry without checking guest state")
			}
		} else if result == nil {
			taskErr = fmt.Errorf("task result unavailable; guest retained")
		} else if result.ExitCode != 0 {
			taskErr = fmt.Errorf("workspace task exited with status %d", result.ExitCode)
		}
		taskSucceeded = e == nil && result != nil && result.ExitCode == 0
		var captureErr error
		if result != nil {
			var exitCode *int32
			if e == nil {
				exitCode = &result.ExitCode
			}
			data, _ := json.Marshal(struct {
				ExitCode            *int32 `json:"exit_code,omitempty"`
				FinalResultReceived bool   `json:"final_result_received"`
				OutputTruncated     bool   `json:"output_truncated"`
			}{exitCode, e == nil, len(result.Stdout) > 2<<20 || len(result.Stderr) > 2<<20})
			for _, item := range []struct{ name, content string }{{"stdout", result.Stdout}, {"stderr", result.Stderr}} {
				content := []byte(item.content)
				if len(content) > 2<<20 {
					content = content[:2<<20]
				}
				if e := b.RecordArtifact(runs.Artifact{Name: item.name, Status: "present", Path: "artifacts/task-" + item.name + ".log", ContentType: "text/plain"}, content); e != nil {
					captureErr = e
				}
			}
			if e := b.RecordArtifact(runs.Artifact{Name: "task-result", Status: "present", Path: "artifacts/task-result.json", ContentType: "application/json"}, data); e != nil {
				captureErr = e
			}
		}
		if captureErr != nil {
			if taskErr != nil {
				receipt.CleanupErrors = append(receipt.CleanupErrors, "task output evidence could not be recorded")
			} else {
				return fmt.Errorf("task completed but result capture failed")
			}
		}
		return taskErr
	}); err != nil {
		return receipt, err
	}

	return receipt, nil
}

func pinOwnedWorkspaceGuest(state taskDisposition, runDir string) error {
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		return err
	}
	defer guard.Release()
	if state.Guest == nil {
		return fmt.Errorf("owned workspace identity unavailable")
	}
	guest, err := identifyTaskGuest(state.Guest.Path)
	if err != nil || guest != *state.Guest {
		return fmt.Errorf("owned workspace identity changed")
	}
	run, err := identifyTaskGuest(runDir)
	if err != nil {
		return fmt.Errorf("identify workspace receipt: %w", err)
	}
	return addWorkspaceTaskPinsWithGuard(coveRoot(), guard, state, state.Generation, []workspaceTaskPinTarget{
		{Category: "vm", ID: vmconfig.NameForPath(guest.Path), Identity: guest},
		{Category: "run", ID: state.RunID, Identity: run},
	})
}

func defaultWorkspaceDeps() workspaceDeps {
	return workspaceDeps{
		PinOwned: pinOwnedWorkspaceGuest,
		StartOwned: func(p workspacePlan, dir, generation string) error {
			return startWorkspaceGuestWithGeneration(p, dir, generation, true)
		},
		DiscardOwned: func(ctx context.Context, state taskDisposition, generation string) error {
			return cleanupWorkspaceGuest(ctx, state, generation, productionWorkspaceCleanupDeps())
		},
		FinalizeDiscardOwned: finalizeDiscardedWorkspaceGuest,
		SourceDirectory: func(name string) (string, error) {
			dir, exists := vmconfig.ExistingPath(name)
			if !exists {
				return "", fmt.Errorf("base guest missing")
			}
			identity, err := identifyTaskGuest(dir)
			return identity.Path, err
		}, IdentifyGuest: identifyTaskGuest,
		Resolve: resolveWorkspaceGuest, Fork: forkWorkspaceGuest, ForkOwned: forkOwnedWorkspaceGuest, HostStorage: workspaceHostStorage, ConfigureShares: configureWorkspaceShares, Start: startWorkspaceGuest, Ready: waitWorkspaceReady, GuestStorage: workspaceGuestStorage, GoCheck: checkWorkspaceGo, Prepare: prepareWorkspaceGo, Mount: mountWorkspaceShares, Task: executeWorkspaceTask, Capture: captureWorkspaceState, Discard: discardWorkspaceGuest, RunsRoot: filepath.Join(coveRoot(), "runs"),
	}
}

func resolveWorkspaceGuest(p workspacePlan) (string, bool, error) {
	dir, ok := vmconfig.ExistingPath(p.VM)
	if !ok {
		return vmconfig.Path(p.VM), false, nil
	}
	osName := vzscriptGuestOSFromPlatform(vmconfig.DetectOSType(dir))
	if osName != p.GuestOS {
		return dir, true, fmt.Errorf("existing guest OS conflicts with profile; select a matching guest without replacing it")
	}
	return dir, true, nil
}

func forkWorkspaceGuest(from, to string) error {
	dir, ok := vmconfig.ExistingPath(from)
	if !ok {
		return fmt.Errorf("base guest missing; prepare it with cove up")
	}
	identity, err := identifyTaskGuest(dir)
	if err != nil {
		return err
	}
	_, err = forkOwnedWorkspaceGuest(from, to, identity)
	return err
}

func forkOwnedWorkspaceGuest(from, to string, expectedSource taskGuestIdentity) (taskGuestIdentity, error) {
	var none taskGuestIdentity
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		return none, fmt.Errorf("guard workspace fork: %w", err)
	}
	defer guard.Release()
	dir, ok := vmconfig.ExistingPath(from)
	if !ok {
		return none, fmt.Errorf("base guest missing; prepare it with cove up")
	}
	identity, err := identifyTaskGuest(dir)
	if err != nil || identity != expectedSource {
		return none, fmt.Errorf("workspace source identity changed before fork")
	}
	lock, err := AcquireRunLock(dir)
	if err != nil {
		return none, fmt.Errorf("base must be stopped: %w", err)
	}
	defer lock.Release()
	if _, active, e := liveVMProcessForDirectory(dir, defaultVMProcessCollector()); e != nil {
		return none, e
	} else if active {
		return none, fmt.Errorf("base guest has a live runtime; stop it before forking")
	}
	holders, err := openFileHolderPIDs(vmPrimaryDiskPath(dir))
	if err != nil {
		return none, err
	}
	if len(holders) > 0 {
		return none, fmt.Errorf("base disk is open; stop its owner before forking")
	}
	if err := forkVMLocked(guard, from, to); err != nil {
		return none, err
	}
	child, exists := vmconfig.ExistingPath(to)
	if !exists {
		return none, fmt.Errorf("created workspace guest is unavailable")
	}
	identity, err = identifyTaskGuest(child)
	if err != nil {
		return none, fmt.Errorf("identify created workspace guest: %w", err)
	}
	return identity, nil
}

func workspaceHostStorage(p workspacePlan) error {
	existing := p.Output.Path
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(existing)
		if next == existing {
			return fmt.Errorf("output filesystem unavailable")
		}
		existing = next
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(existing, &st); err != nil {
		return fmt.Errorf("inspect output capacity: %w", err)
	}
	if uint64(st.Bavail)*uint64(st.Bsize) < p.MinFreeGiB<<30 {
		return fmt.Errorf("output filesystem has less than %d GiB free", p.MinFreeGiB)
	}
	return os.MkdirAll(p.Output.Path, 0700)
}

func configureWorkspaceShares(p workspacePlan, dir string) error {
	desired := []SharedFolderEntry{{Path: p.Source.Path, Tag: workspaceSourceTag, ReadOnly: p.Source.ReadOnly}, {Path: p.Output.Path, Tag: workspaceOutputTag, ReadOnly: false}}
	folders := LoadSharedFolders(dir)
	for _, want := range desired {
		for _, have := range folders {
			if have.Tag == want.Tag || have.Path == want.Path {
				if have != want {
					return fmt.Errorf("workspace share conflicts with saved path, tag, or access; resolve it explicitly before opening")
				}
			}
		}
	}
	for _, want := range desired {
		present := false
		for _, have := range folders {
			if have == want {
				present = true
				break
			}
		}
		if !present {
			folders = append(folders, want)
		}
	}
	return saveSharedFolders(dir, folders)
}

func startWorkspaceGuest(p workspacePlan, dir string) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Errorf("create workspace runtime generation: %w", err)
	}
	return startWorkspaceGuestWithGeneration(p, dir, hex.EncodeToString(token[:]), false)
}

func startWorkspaceGuestWithGeneration(p workspacePlan, dir, generation string, fresh bool) error {
	if controlSocketResponds(dir) {
		if fresh {
			return fmt.Errorf("owned workspace runtime already exists; guest retained")
		}
		return nil
	}
	if _, active, err := liveVMProcessForDirectory(dir, defaultVMProcessCollector()); err != nil {
		return err
	} else if active {
		return fmt.Errorf("guest runtime already exists but control is unavailable; recover readiness without starting another owner")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__workspace-runtime", p.VM, dir, generation)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start workspace guest: %w", err)
	}
	return cmd.Process.Release()
}

func workspaceExec(ctx context.Context, dir, route string, args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := NewControlClient(GetControlSocketPathForVM(dir))
	resp, err := client.SendRequestCtx(ctx, &controlpb.ControlRequest{Type: route, Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: args, Env: env, WorkingDir: workDir}}})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("guest execution route unavailable")
	}
	r := resp.GetAgentExecResult()
	if r == nil {
		return nil, fmt.Errorf("guest returned no execution result")
	}
	return r, nil
}

func waitWorkspaceReady(ctx context.Context, p workspacePlan, dir string) ([]byte, error) {
	deadline, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var last error
	for {
		root, err := workspaceExec(deadline, dir, "agent-exec", []string{"true"}, nil, "", 3*time.Second)
		if err == nil && root.ExitCode == 0 {
			user, e := workspaceExec(deadline, dir, "agent-user-exec", []string{"true"}, nil, "", 3*time.Second)
			if e == nil && user.ExitCode == 0 {
				return captureWorkspaceState(deadline, p, dir)
			}
			last = fmt.Errorf("root is ready; user-session execution is unavailable; log in to the intended guest user")
		} else {
			last = fmt.Errorf("root-agent execution is unavailable")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-deadline.Done():
			timer.Stop()
			return nil, fmt.Errorf("workspace readiness: %v: %w", last, deadline.Err())
		case <-timer.C:
		}
	}
}

func workspaceGuestStorage(p workspacePlan, dir string) error {
	report, err := collectDiskUsage(p.VM, dir, false, "")
	if err != nil {
		return err
	}
	if report.Host.FreeBytes < p.MinFreeGiB<<30 {
		return fmt.Errorf("host VM storage has less than %d GiB free", p.MinFreeGiB)
	}
	if report.Guest == nil {
		return fmt.Errorf("guest disk capacity unavailable")
	}
	if report.Guest.FreeBytes < p.MinFreeGiB<<30 {
		return fmt.Errorf("guest has less than %d GiB free; inspect cove disk usage and preview disk clean before applying cleanup", p.MinFreeGiB)
	}
	return nil
}

func checkWorkspaceGo(ctx context.Context, p workspacePlan, dir string) (string, error) {
	r, err := workspaceExec(ctx, dir, "agent-user-exec", workspaceUserArgs(resolveReadyCheck("go").Args), nil, "", 10*time.Second)
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", fmt.Errorf("go version check returned no result in the user session")
	}
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "go version go") {
		detail := strings.TrimSpace(r.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(r.Stdout)
		}
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
		return "", fmt.Errorf("go version check failed in the user session (exit %d): %s", r.ExitCode, detail)
	}
	return strings.TrimSpace(r.Stdout), nil
}

func prepareWorkspaceGo(ctx context.Context, p workspacePlan, dir string) error {
	for _, r := range p.Preparation.Recipes {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, ok := p.recipeData[r.Source]
		if !ok {
			return fmt.Errorf("planned preparation source is unavailable; create a fresh profile plan")
		}
		cfg := cfgForRecipe(vzscriptConfig{socketPath: GetControlSocketPathForVM(dir), guestOS: p.GuestOS, execTimeout: 30 * time.Minute}, parseScriptMeta(data))
		if err := runVZScriptContext(ctx, data, r.Source, cfg); err != nil {
			return err
		}
	}
	return nil
}

func mountWorkspaceShares(ctx context.Context, p workspacePlan, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	changed, err := configureWorkspaceGuestOwner(ctx, p, dir)
	if err != nil {
		return err
	}
	client := NewControlClient(GetControlSocketPathForVM(dir))
	client.SetTimeout(15 * time.Second)
	status, err := client.SharedFoldersRuntimeStatus()
	if err != nil {
		return err
	}
	if !status.VirtioFS {
		return fmt.Errorf("guest lacks its shared-folder device; shares are saved; stop and restart explicitly before retrying")
	}
	if _, err := client.SharedFoldersApply(); err != nil {
		return fmt.Errorf("apply workspace shares: %w", err)
	}
	if _, err := refreshSharedFoldersInGuest(dir, defaultSharedFoldersMountRoot(dir), defaultSharedFolderMountTimeouts(), changed); err != nil {
		return fmt.Errorf("mount workspace shares for intended user: %w; stop and restart the guest explicitly before retrying", err)
	}
	for _, want := range []struct {
		path     string
		readOnly bool
	}{{p.SourceGuestPath, p.Source.ReadOnly}, {p.OutputGuestPath, false}} {
		result := probeSharedFolder(client, want.path, want.readOnly, 10*time.Second)
		if !result.Readable || (!want.readOnly && !result.Writable) {
			return fmt.Errorf("workspace share %s is unavailable or has incorrect access: %s", want.path, result.Detail)
		}
	}
	return nil
}

func captureWorkspaceState(ctx context.Context, p workspacePlan, dir string) ([]byte, error) {
	status := map[string]any{"daemon": "unknown", "user": "unknown"}
	client := NewControlClient(GetControlSocketPathForVM(dir))
	resp, err := client.SendRequestCtx(ctx, &controlpb.ControlRequest{Type: "agent-status"})
	observation := "unavailable"
	if err == nil && resp != nil && resp.Success {
		resp = ctlEnrichResponseForPrint(GetControlSocketPathForVM(dir), resp, "agent-status")
		if len(resp.Data) <= 1<<20 && json.Unmarshal([]byte(resp.Data), &status) == nil && status != nil {
			observation = "present"
		} else {
			status = map[string]any{"daemon": "unknown", "user": "unknown"}
			observation = "invalid"
		}
	}
	status["lifecycle"] = workspaceLifecycleObservation(p.VM, dir)
	status["agentObservation"] = observation
	return json.MarshalIndent(status, "", "  ")
}

func discardWorkspaceGuest(p workspacePlan, dir string) error {
	return fmt.Errorf("owned guest cleanup requires verified identity, live owner, and pin gates; guest retained")
}

func workspaceUserArgs(args []string) []string {
	// Arguments remain separate positional parameters, including shell punctuation.
	wrapper := []string{"/bin/sh", "-c", `PATH="$HOME/go/bin:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"; export PATH; exec "$@"`, "cove-workspace"}
	return append(wrapper, args...)
}

func workspaceTaskArgs(p workspacePlan, args []string) []string {
	command := []string{"/usr/bin/env", "GOCACHE=" + filepath.Join(p.OutputGuestPath, "gocache"), "GOMODCACHE=" + filepath.Join(p.OutputGuestPath, "gomodcache"), "GOTMPDIR=" + filepath.Join(p.OutputGuestPath, "tmp"), "TMPDIR=" + filepath.Join(p.OutputGuestPath, "tmp"), "GOBIN=" + filepath.Join(p.OutputGuestPath, "bin"), "GOFLAGS=-mod=readonly", "COVE_WORKSPACE_OUTPUT=" + p.OutputGuestPath}
	return workspaceUserArgs(append(command, args...))
}

func workspaceSourceProvenance(ctx context.Context, path string) []byte {
	record := map[string]any{"source_path": path, "qualification": "Git commit and dirty state are observations, not an immutable source snapshot", "identity": "unknown"}
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := runHostInspection(commandCtx, 2*time.Second, "git", "-C", path, "rev-parse", "HEAD")
	commit := strings.TrimSpace(string(out))
	if err != nil || len(commit) != 40 && len(commit) != 64 {
		data, _ := json.Marshal(record)
		return data
	}
	if _, err := hex.DecodeString(commit); err != nil {
		data, _ := json.Marshal(record)
		return data
	}
	status, err := runHostInspection(commandCtx, 2*time.Second, "git", "-C", path, "-c", "core.fsmonitor=false", "--no-optional-locks", "status", "--porcelain", "--untracked-files=normal")
	if err == nil {
		record["identity"] = "git"
		record["commit"] = commit
		record["dirty"] = len(bytes.TrimSpace(status)) > 0
	}
	data, _ := json.Marshal(record)
	return data
}

func executeWorkspaceTask(ctx context.Context, o workspaceOptions, p workspacePlan, dir string, env commandEnv) (*controlpb.AgentExecResponse, error) {
	r, err := workspaceExec(ctx, dir, "agent-user-exec", []string{"mkdir", "-p", filepath.Join(p.OutputGuestPath, "gocache"), filepath.Join(p.OutputGuestPath, "gomodcache"), filepath.Join(p.OutputGuestPath, "tmp"), filepath.Join(p.OutputGuestPath, "bin")}, nil, "", 10*time.Second)
	if err != nil {
		return nil, err
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("writable Go cache directories unavailable")
	}
	return streamWorkspaceTask(ctx, GetControlSocketPathForVM(dir), workspaceTaskArgs(p, o.Args), p.SourceGuestPath, o.Timeout, env.Stdout, env.Stderr)
}

func streamWorkspaceTask(ctx context.Context, socket string, args []string, workDir string, timeout time.Duration, stdout, stderr io.Writer) (*controlpb.AgentExecResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := &controlpb.AgentExecResponse{}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	req := &controlpb.ControlRequest{Type: "agent-user-exec-stream", AuthToken: resolveControlTokenForSocket(socket), Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: args, WorkingDir: workDir}}}
	data, err := protojsonMarshaler.Marshal(req)
	if err != nil {
		return result, err
	}
	if _, err = conn.Write(append(data, '\n')); err != nil {
		return result, err
	}
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 64<<10), 1<<20)
	var out, errOut bytes.Buffer
	finish := func(e error) (*controlpb.AgentExecResponse, error) {
		result.Stdout = out.String()
		result.Stderr = errOut.String()
		if ctx.Err() != nil {
			e = ctx.Err()
		} else if n, ok := e.(net.Error); ok && n.Timeout() {
			if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				e = context.DeadlineExceeded
			}
		}
		return result, e
	}
	for scan.Scan() {
		var response controlpb.ControlResponse
		if err := protojsonUnmarshaler.Unmarshal(scan.Bytes(), &response); err != nil {
			return finish(fmt.Errorf("invalid task stream response"))
		}
		if !response.Success {
			return finish(fmt.Errorf("task stream execution unavailable"))
		}
		var event struct {
			Stream   string `json:"stream"`
			Data     string `json:"data"`
			Done     bool   `json:"done"`
			ExitCode *int32 `json:"exitCode"`
		}
		if err := json.Unmarshal([]byte(response.Data), &event); err != nil {
			return finish(fmt.Errorf("invalid task stream event"))
		}
		if event.Done {
			if event.ExitCode == nil {
				return finish(fmt.Errorf("task stream ended without exit status"))
			}
			result.ExitCode = *event.ExitCode
			return finish(nil)
		}
		if event.Data == "" {
			return finish(fmt.Errorf("task stream event has no output or final status"))
		}
		chunk, err := base64.StdEncoding.DecodeString(event.Data)
		if err != nil {
			return finish(fmt.Errorf("invalid task stream output"))
		}
		writer := stdout
		buffer := &out
		switch event.Stream {
		case "stdout":
		case "stderr":
			writer = stderr
			buffer = &errOut
		default:
			return finish(fmt.Errorf("unknown task output stream"))
		}
		if remain := (2 << 20) + 1 - buffer.Len(); remain > 0 {
			if len(chunk) > remain {
				_, _ = buffer.Write(chunk[:remain])
			} else {
				_, _ = buffer.Write(chunk)
			}
		}
		if writer != nil {
			n, err := writer.Write(chunk)
			if err == nil && n != len(chunk) {
				err = io.ErrShortWrite
			}
			if err != nil {
				return finish(fmt.Errorf("write task output: %w", err))
			}
		}
	}
	if err := scan.Err(); err != nil {
		return finish(err)
	}
	return finish(fmt.Errorf("task stream closed before final exit status"))
}
