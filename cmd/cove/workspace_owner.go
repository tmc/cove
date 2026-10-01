package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

func parseWorkspaceOwner(value string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("intended guest user identity is unavailable or privileged")
	}
	return uint32(n), nil
}

func configureWorkspaceGuestOwner(ctx context.Context, p workspacePlan, dir string) (bool, error) {
	if p.GuestOS != "darwin" {
		return false, nil
	}
	var ids [2]uint32
	for i, flag := range []string{"-u", "-g"} {
		r, err := workspaceExec(ctx, dir, "agent-user-exec", []string{"/usr/bin/id", flag}, nil, "", 5*time.Second)
		if err != nil {
			return false, fmt.Errorf("discover workspace user identity: %w", err)
		}
		if r == nil || r.ExitCode != 0 {
			return false, fmt.Errorf("discover workspace user identity: id failed")
		}
		ids[i], err = parseWorkspaceOwner(r.Stdout)
		if err != nil {
			return false, err
		}
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		return false, fmt.Errorf("load workspace user mapping: %w", err)
	}
	if cfg.GuestUserUID == ids[0] && cfg.GuestUserGID == ids[1] {
		r, err := workspaceExec(ctx, dir, "agent-user-exec", []string{"/usr/bin/stat", "-f", "%u", p.OutputGuestPath}, nil, "", 5*time.Second)
		if err != nil || r == nil || r.ExitCode != 0 {
			return true, nil
		}
		owner, err := parseWorkspaceOwner(r.Stdout)
		return err != nil || owner != ids[0], nil
	}
	if err := vmconfig.SetGuestUser(dir, ids[0], ids[1]); err != nil {
		return false, fmt.Errorf("save workspace user mapping: %w", err)
	}
	return true, nil
}
