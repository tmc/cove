package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type workspaceRepairExec func(context.Context, string, []string, string) (*controlpb.AgentExecResponse, error)

func handleWorkspaceRepair(ctx context.Context, o workspaceOptions, p workspacePlan, env commandEnv) error {
	if o.From != "" || o.Prepare != "" || p.GuestOS != "darwin" {
		return fmt.Errorf("repair requires an existing macOS workspace without preparation or fork")
	}
	dir, exists, err := resolveWorkspaceGuest(p)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("repair guest does not exist")
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		return err
	}
	if cfg.GuestUserUID == 0 {
		return fmt.Errorf("saved intended user identity unavailable")
	}
	folders := LoadSharedFolders(dir)
	for _, want := range []SharedFolderEntry{{Path: p.Source.Path, Tag: workspaceSourceTag, ReadOnly: p.Source.ReadOnly}, {Path: p.Output.Path, Tag: workspaceOutputTag, ReadOnly: false}} {
		found := false
		for _, have := range folders {
			if have == want {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("repair requires unchanged saved workspace shares")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, o.ReadinessTimeout)
	defer cancel()
	client := NewControlClient(GetControlSocketPathForVM(dir))
	resp, err := client.SendRequestCtx(ctx, &controlpb.ControlRequest{Type: "agent-ui-status"})
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success || resp.GetAgentUi() == nil {
		return fmt.Errorf("user session observation unavailable; no repair performed")
	}
	session := resp.GetAgentUi().GetStatus().GetSessionState()
	if session != "active" && session != "unlocked" {
		return fmt.Errorf("repair requires an active console session; observed %q; no repair performed", session)
	}
	if session == "active" {
		fmt.Fprintln(env.Stdout, "Console session active; lock state unknown; explicit service repair authorized")
	}
	call := func(ctx context.Context, route string, args []string, cwd string) (*controlpb.AgentExecResponse, error) {
		return workspaceExec(ctx, dir, route, args, nil, cwd, o.ReadinessTimeout)
	}
	changed, err := repairWorkspaceUserSession(ctx, p, cfg.GuestUserUID, call)
	if err != nil {
		if changed {
			return fmt.Errorf("user service restarted once but verification failed; do not retry automatically: %w", err)
		}
		return err
	}
	if changed {
		fmt.Fprintln(env.Stdout, "User service restarted once; fresh source working directory verified; no task replayed")
	} else {
		fmt.Fprintln(env.Stdout, "User source working directory is healthy; no service restarted")
	}
	return nil
}

func repairWorkspaceUserSession(ctx context.Context, p workspacePlan, uid uint32, call workspaceRepairExec) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root, err := call(ctx, "agent-exec", []string{"/bin/sh", "-c", `test "$(/usr/bin/id -u)" = 0 && /bin/pwd -P`}, p.SourceGuestPath)
	if err != nil {
		return false, err
	}
	if root == nil || root.ExitCode != 0 || strings.TrimSpace(root.Stdout) == "" {
		return false, fmt.Errorf("root source working directory unavailable; filesystem cause unknown")
	}
	target := "gui/" + strconv.FormatUint(uint64(uid), 10) + "/" + agentLaunchAgentLabel
	observe := func() (int, error) {
		r, e := call(ctx, "agent-exec", []string{"/bin/sh", "-c", `set -e; /usr/bin/stat -f %u /dev/console; /bin/launchctl print "$1" | /usr/bin/awk '$1 == "pid" && $2 == "=" {print $3}'`, "cove-repair", target}, "")
		if e != nil {
			return 0, e
		}
		if r == nil || r.ExitCode != 0 {
			return 0, fmt.Errorf("user service identity unavailable")
		}
		fields := strings.Fields(r.Stdout)
		if len(fields) != 2 || fields[0] != strconv.FormatUint(uint64(uid), 10) {
			return 0, fmt.Errorf("console user or service identity changed")
		}
		pid, e := strconv.Atoi(fields[1])
		if e != nil || pid <= 1 {
			return 0, fmt.Errorf("user service pid unavailable")
		}
		return pid, nil
	}
	pid, err := observe()
	if err != nil {
		return false, err
	}
	probe := func(pid int) (bool, error) {
		r, e := call(ctx, "workspace-user-observe", []string{"/bin/sh", "-c", `/usr/bin/id -u; printf '%s\n' "$PPID"; /bin/pwd -P`}, p.SourceGuestPath)
		if e != nil {
			return false, e
		}
		if r == nil {
			return false, fmt.Errorf("user cwd observation missing")
		}
		fields := strings.Fields(r.Stdout)
		if len(fields) < 2 || fields[0] != strconv.FormatUint(uint64(uid), 10) || fields[1] != strconv.Itoa(pid) {
			return false, fmt.Errorf("user rpc identity does not match intended service")
		}
		if r.ExitCode == 0 && len(fields) > 2 {
			return true, nil
		}
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, "No such file or directory") {
			return false, fmt.Errorf("user cwd failure is not a qualified repair candidate; filesystem cause unknown")
		}
		return false, nil
	}
	healthy, err := probe(pid)
	if err != nil || healthy {
		return false, err
	}
	current, err := observe()
	if err != nil {
		return false, err
	}
	if current != pid {
		return false, fmt.Errorf("user service changed before restart")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r, err := call(ctx, "agent-exec", []string{"/bin/sh", "-c", `set -e; test "$(/usr/bin/stat -f %u /dev/console)" = "$2"; actual=$(/bin/launchctl print "$1" | /usr/bin/awk '$1 == "pid" && $2 == "=" {print $3}'); test "$actual" = "$3"; /bin/launchctl kickstart -k -p "$1"`, "cove-repair", target, strconv.FormatUint(uint64(uid), 10), strconv.Itoa(pid)}, "")
	if err != nil {
		return false, fmt.Errorf("user restart outcome unknown; do not retry: %w", err)
	}
	if r == nil || r.ExitCode != 0 {
		return false, fmt.Errorf("user restart failed or outcome unavailable; do not retry")
	}
	restartPID, parseErr := strconv.Atoi(strings.TrimSpace(r.Stdout))
	if parseErr != nil || restartPID <= 1 || restartPID == pid {
		return true, fmt.Errorf("restart identity unknown; do not retry")
	}
	newPID, err := observe()
	if err != nil {
		return true, err
	}
	if newPID != restartPID {
		return true, fmt.Errorf("user service pid does not match restart receipt")
	}
	healthy, err = probe(newPID)
	if err != nil {
		return true, err
	}
	if !healthy {
		return true, fmt.Errorf("user source working directory remains unavailable; filesystem cause unknown; no task replayed")
	}
	return true, nil
}
