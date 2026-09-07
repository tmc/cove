// vm_stale_disk.go — detect and clear stale VM disk locks.
//
// A crashed or leaked cove VM can leave an orphaned
// com.apple.Virtualization.VirtualMachine.xpc process holding a VM
// bundle's disk.img open even though cove considers the VM stopped
// (runtime.json state == stopped, no live control socket). The next
// start then fails config validation with VZErrorDomain code=2
// "The storage device attachment is invalid" (or the sibling
// "directory sharing device configuration is invalid"), because the
// disk image is already open. `cove doctor` flags this, and
// `cove doctor clear-stale-locks` terminates the holder(s) so the VM
// starts again.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

// vmDiskHolder is a process holding a VM bundle disk image open.
type vmDiskHolder struct {
	PID     int
	Command string
}

// staleDiskLease is a stopped VM whose disk image is still held open by
// one or more stale Virtualization processes.
type staleDiskLease struct {
	Name    string
	VMDir   string
	Holders []vmDiskHolder
}

func (l staleDiskLease) pidList() string {
	pids := make([]string, len(l.Holders))
	for i, h := range l.Holders {
		pids[i] = strconv.Itoa(h.PID)
	}
	if len(pids) == 1 {
		return "pid " + pids[0]
	}
	return "pids " + strings.Join(pids, ", ")
}

func (l staleDiskLease) summary() string {
	return fmt.Sprintf("%s: disk held by stale VZ process (%s) though VM is stopped", l.Name, l.pidList())
}

// Seams for testing; overridden in *_test.go so detection and recovery
// never touch real processes.
var (
	vmDiskHoldersFor       = collectVMDiskHolders
	staleDiskVMRunning     = vmConsideredRunning
	staleDiskKill          = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
	staleDiskProcessLiveFn = processLive

	// staleDiskTermPolls and staleDiskTermInterval bound how long
	// terminateStaleHolder waits for a graceful exit before SIGKILL.
	staleDiskTermPolls    = 20
	staleDiskTermInterval = 100 * time.Millisecond
)

// staleDiskLeaseFor reports a lease when a stopped VM still has
// Virtualization processes holding its disk open. It returns nil when the
// VM is running or nothing holds the disk. holders must already be filtered
// to Virtualization processes (see collectVMDiskHolders).
func staleDiskLeaseFor(name, vmDir string, running bool, holders []vmDiskHolder) *staleDiskLease {
	if running || len(holders) == 0 {
		return nil
	}
	return &staleDiskLease{Name: name, VMDir: vmDir, Holders: holders}
}

// collectVMDiskHolders returns the Virtualization processes holding the
// bundle's disk.img or aux.img open, excluding the current process.
func collectVMDiskHolders(vmDir string) ([]vmDiskHolder, error) {
	self := os.Getpid()
	seen := make(map[int]bool)
	var holders []vmDiskHolder
	for _, name := range []string{"disk.img", "aux.img"} {
		path := filepath.Join(vmDir, name)
		pids, err := openFileHolderPIDs(path)
		if err != nil {
			return holders, err
		}
		for _, pid := range pids {
			if pid == self || seen[pid] {
				continue
			}
			command := holderCommand(pid)
			if !isVirtualizationVMProcess(command) {
				continue
			}
			seen[pid] = true
			holders = append(holders, vmDiskHolder{PID: pid, Command: command})
		}
	}
	return holders, nil
}

// vmConsideredRunning reports whether cove believes the VM is running,
// either from its runtime state (run lock held by a live process) or a
// live control socket.
func vmConsideredRunning(vmDir string) bool {
	if detectRuntimeState(vmDir) != "" {
		return true
	}
	return isVMRunning(GetControlSocketPathForVM(vmDir))
}

// collectStaleDiskLeases returns every stopped VM whose disk is still held
// open by a stale Virtualization process.
func collectStaleDiskLeases() []staleDiskLease {
	vms, err := vmProcessListVMs()
	if err != nil {
		return nil
	}
	var leases []staleDiskLease
	for _, vm := range vms {
		holders, err := vmDiskHoldersFor(vm.Path)
		if err != nil {
			continue
		}
		if lease := staleDiskLeaseFor(vm.Name, vm.Path, staleDiskVMRunning(vm.Path), holders); lease != nil {
			leases = append(leases, *lease)
		}
	}
	return leases
}

func hostDoctorStaleDiskCheck() hostDoctorCheck {
	leases := collectStaleDiskLeases()
	if len(leases) == 0 {
		return hostDoctorCheck{"stale-disk-lease", "pass", "no VM disks held by stale Virtualization processes"}
	}
	parts := make([]string, len(leases))
	for i, l := range leases {
		parts[i] = l.summary()
	}
	msg := strings.Join(parts, "; ") + "; recover with: cove doctor clear-stale-locks"
	return hostDoctorCheck{"stale-disk-lease", "warn", msg}
}

// staleDiskLeaseForStart reports a stale lease for a single VM directory
// during a failed start. It does not consult runtime state (the starting
// process itself holds the run lock); it reports whatever Virtualization
// process, other than this one, still holds the disk open.
func staleDiskLeaseForStart(vmDir string) *staleDiskLease {
	holders, err := vmDiskHoldersFor(vmDir)
	if err != nil || len(holders) == 0 {
		return nil
	}
	return &staleDiskLease{Name: vmconfig.NameForPath(vmDir), VMDir: vmDir, Holders: holders}
}

func handleDoctorClearStaleLocks(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("doctor clear-stale-locks", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("n", false, "report stale locks without terminating anything")
	fs.Usage = func() { printDoctorClearStaleLocksUsage(w) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: cove doctor clear-stale-locks [vm] [-n]")
	}
	return runClearStaleDiskLocks(w, fs.Arg(0), *dryRun)
}

func printDoctorClearStaleLocksUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove doctor clear-stale-locks [vm] [-n]

Terminate orphaned Apple Virtualization processes that still hold a stopped
VM's disk image open. Such a stale holder makes the next start fail with
"the storage device attachment is invalid". With no vm argument, every VM
under the cove state root is checked.

Flags:
  -n    report stale locks without terminating anything`)
}

func runClearStaleDiskLocks(w io.Writer, filter string, dryRun bool) error {
	leases := collectStaleDiskLeases()
	if filter != "" {
		var kept []staleDiskLease
		for _, l := range leases {
			if l.Name == filter {
				kept = append(kept, l)
			}
		}
		leases = kept
	}
	if len(leases) == 0 {
		if filter != "" {
			fmt.Fprintf(w, "No stale VM disk locks found for %q.\n", filter)
		} else {
			fmt.Fprintln(w, "No stale VM disk locks found.")
		}
		return nil
	}
	for _, l := range leases {
		fmt.Fprintln(w, l.summary())
		if dryRun {
			for _, h := range l.Holders {
				fmt.Fprintf(w, "  would terminate pid %d (%s)\n", h.PID, h.Command)
			}
			continue
		}
		clearStaleDiskLease(w, l)
	}
	return nil
}

func clearStaleDiskLease(w io.Writer, l staleDiskLease) {
	for _, h := range l.Holders {
		if err := terminateStaleHolder(h.PID); err != nil {
			fmt.Fprintf(w, "  terminate pid %d: %v\n", h.PID, err)
			continue
		}
		fmt.Fprintf(w, "  terminated pid %d (%s)\n", h.PID, h.Command)
	}
	if removeStaleRunLock(l.VMDir) {
		fmt.Fprintf(w, "  removed stale %s\n", filepath.Join(l.VMDir, runLockFile))
	}
}

// terminateStaleHolder sends SIGTERM, waits for the process to exit, then
// escalates to SIGKILL if it is still alive.
func terminateStaleHolder(pid int) error {
	if err := staleDiskKill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < staleDiskTermPolls; i++ {
		if !staleDiskProcessLiveFn(pid) {
			return nil
		}
		time.Sleep(staleDiskTermInterval)
	}
	return staleDiskKill(pid, syscall.SIGKILL)
}

// removeStaleRunLock removes <vmDir>/run.lock when it exists and is no
// longer held, so a leftover lock file does not confuse later runs.
func removeStaleRunLock(vmDir string) bool {
	path := filepath.Join(vmDir, runLockFile)
	if _, err := os.Stat(path); err != nil {
		return false
	}
	if isRunLockHeld(vmDir) {
		return false
	}
	return os.Remove(path) == nil
}
