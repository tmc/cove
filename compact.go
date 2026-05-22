package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

const compactTimeout = 30 * time.Minute

type compactClient interface {
	AgentPingTyped() (string, error)
	AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error)
}

type compactResult struct {
	Platform string
	Stdout   string
	Stderr   string
}

func handleCompact(args []string) error {
	fs, target := newCompactFlagSet(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	pos := fs.Args()
	if len(pos) > 1 {
		return fmt.Errorf("usage: cove compact [options] [vm]")
	}
	if len(pos) == 1 {
		if *target != "" {
			return fmt.Errorf("usage: cove compact [options] [vm]")
		}
		*target = pos[0]
	}

	vmDirectory := vmDir
	if *target != "" {
		vmDirectory = vmconfig.Path(*target)
	}
	if !vmconfig.Validate(vmDirectory) {
		return fmt.Errorf("vm not found or invalid: %s", vmDirectory)
	}

	result, err := compactVM(vmDirectory)
	if err != nil {
		return err
	}
	printCompactResult(os.Stdout, result)
	return nil
}

func newCompactFlagSet(w io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("compact", flag.ContinueOnError)
	fs.SetOutput(w)
	target := fs.String("vm", "", "target VM name")
	fs.Usage = func() { printCompactUsage(w) }
	return fs, target
}

func compactVM(vmDirectory string) (*compactResult, error) {
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(5 * time.Second)
	return compactVMWithClient(vmDirectory, client)
}

func compactVMWithClient(vmDirectory string, client compactClient) (*compactResult, error) {
	if _, err := client.AgentPingTyped(); err != nil {
		return nil, fmt.Errorf("guest agent unavailable: %w", err)
	}

	platform := agentstate.Platform(vmDirectory)
	if err := precheckCompactCapacity(vmDirectory, platform); err != nil {
		return nil, err
	}
	args, err := compactCommand(platform)
	if err != nil {
		return nil, err
	}

	res, err := client.AgentExecTypedTimeout(args, nil, "", compactTimeout)
	if err != nil {
		return nil, fmt.Errorf("compact guest: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("compact guest: missing response")
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("compact guest: exit %d: %s", res.ExitCode, msg)
	}

	return &compactResult{
		Platform: platform,
		Stdout:   strings.TrimSpace(res.Stdout),
		Stderr:   strings.TrimSpace(res.Stderr),
	}, nil
}

// macOSCompactZeroPath is the location of the throw-away zero-fill file used
// to coax APFS into freeing blocks. It lives on the writable Data volume
// (`/` is read-only on Big Sur+), under `/private/var/tmp` so it is hidden,
// auto-cleaned, and on a path that doesn't show up in Spotlight.
const macOSCompactZeroPath = "/System/Volumes/Data/private/var/tmp/.cove-zero"

// macOSCompactScript is the guest-side zero-fill pipeline. APFS rejects
// `diskutil secureErase freespace` outright (-69489: "makes no sense due to
// its possibly-unbounded size"), so the standard recipe is to dd /dev/zero
// to a throwaway file until the volume runs out of space, sync, then unlink.
// The unused blocks are then zero-content and virtio-blk's discard/unmap
// forwards a TRIM to the host disk image, which APFS turns into a
// `F_PUNCHHOLE` to reclaim physical sectors.
//
// `|| true` covers the expected ENOSPC at the end of dd. `sync` flushes
// pending writes before the unlink so blocks aren't reclaimed by the
// volume before they hit the discard path.
const macOSCompactScript = `set -u; ` +
	`dd if=/dev/zero of=` + macOSCompactZeroPath + ` bs=1m 2>/dev/null || true; ` +
	`sync; ` +
	`rm -f ` + macOSCompactZeroPath

func compactCommand(platform string) ([]string, error) {
	switch platform {
	case agentstate.PlatformLinux:
		return []string{"fstrim", "-v", "/"}, nil
	case agentstate.PlatformMacOS:
		return []string{"sh", "-c", macOSCompactScript}, nil
	default:
		return nil, fmt.Errorf("unsupported guest platform %q", platform)
	}
}

// precheckCompactCapacity verifies the host has enough free space to hold
// a fully-inflated copy of the guest disk image before launching the
// guest-side zero-fill. APFS only reclaims sectors at the end of the dd, and
// during the run the sparse disk image grows toward its logical cap. Failing
// the host's volume mid-write would be confusing; bail early with a clear
// message instead. Linux guests skip this check — fstrim is in-place.
func precheckCompactCapacity(vmDirectory, platform string) error {
	if platform != agentstate.PlatformMacOS {
		return nil
	}
	diskPath := filepath.Join(vmDirectory, "disk.img")
	st, err := os.Stat(diskPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", diskPath, err)
	}
	logical := st.Size()
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("stat %s: unexpected sys type %T", diskPath, st.Sys())
	}
	// Stat_t.Blocks is in 512-byte units regardless of the underlying fs.
	physical := int64(stat.Blocks) * 512
	need := logical - physical
	if need <= 0 {
		// Image is already fully materialized (rare). Nothing to inflate.
		return nil
	}
	var fsstat syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(diskPath), &fsstat); err != nil {
		return fmt.Errorf("statfs %s: %w", filepath.Dir(diskPath), err)
	}
	free := int64(fsstat.Bavail) * int64(fsstat.Bsize)
	if free >= need {
		return nil
	}
	return fmt.Errorf(
		"thorough compaction temporarily inflates the sparse disk image to its full logical size (~%dG) before TRIM punches holes; host has only ~%dG free. Run `cove compact -compact targeted` instead, or free disk space and retry",
		(logical+(1<<30)-1)>>30,
		(free+(1<<30)-1)>>30,
	)
}

func printCompactResult(w io.Writer, result *compactResult) {
	fmt.Fprintf(w, "Compacted %s guest free space\n", result.Platform)
	if result.Stdout != "" {
		fmt.Fprintln(w, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprintln(w, result.Stderr)
	}
}
