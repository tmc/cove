package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

type vmProcessRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

type execVMProcessRunner struct{}

func (execVMProcessRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

type vmProcessCollector interface {
	CollectVMProcesses(baseDir string, knownVMs []vmProcessVMInfo) ([]vmProcessInfo, error)
}

type commandVMProcessCollector struct {
	runner vmProcessRunner
}

type vmProcessInfo struct {
	PID             int
	PPID            int
	Command         string
	VMDirs          []string
	OpenFiles       []string
	Status          string
	Source          string
	OpenFilesOutput string
	OpenFilesErr    error
}

type helperProcessInfo struct {
	PID     int
	PPID    int
	CPU     string
	RSSKB   int
	Command string
}

type vmProcessVMInfo struct {
	Name       string
	Path       string
	RealPath   string
	OwnerPID   int
	SocketPath string
}

var (
	vmProcessListVMs = func() ([]vmconfig.Info, error) {
		return vmconfig.List(nil)
	}
	vmProcessServerInfo = serverInfoForVMProcess
)

func handleDoctorVMProcesses(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("doctor vm-processes", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printDoctorVMProcessesUsage(w) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove doctor vm-processes")
	}
	return runDoctorVMProcesses(w, vmconfig.BaseDir(), execVMProcessRunner{})
}

func printDoctorVMProcessesUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove doctor vm-processes

List live Apple Virtualization VM XPC processes and map visible open files
under cove VM directories. Running cove-owned VMs are also correlated through
their control socket server-info response.`)
}

func runDoctorVMProcesses(w io.Writer, baseDir string, runner vmProcessRunner) error {
	procs, err := collectVMProcesses(baseDir)
	if err != nil {
		return err
	}
	helpers, helperErr := collectHelperProcesses(runner)
	writeVMProcessReport(w, baseDir, procs, helpers, helperErr)
	return nil
}

func collectVMProcesses(baseDir string) ([]vmProcessInfo, error) {
	return collectVMProcessesWithCollector(baseDir, defaultVMProcessCollector())
}

func collectVMProcessesWithCollector(baseDir string, collector vmProcessCollector) ([]vmProcessInfo, error) {
	if collector == nil {
		return nil, errors.New("vm process collector required")
	}
	knownVMs := collectVMProcessVMs()
	procs, err := collector.CollectVMProcesses(baseDir, knownVMs)
	if err != nil {
		return nil, err
	}
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].PID < procs[j].PID
	})
	return procs, nil
}

func (c commandVMProcessCollector) CollectVMProcesses(baseDir string, knownVMs []vmProcessVMInfo) ([]vmProcessInfo, error) {
	runner := c.runner
	if runner == nil {
		return nil, errors.New("vm process runner required")
	}
	psOut, err := runner.Run("ps", "-axo", "pid=,ppid=,command=")
	if err != nil {
		return nil, fmt.Errorf("list vm processes: %w", err)
	}
	procs := parseVMProcessPS(psOut)
	for i := range procs {
		args := []string{"-nP", "-Fpcfn", "-p", strconv.Itoa(procs[i].PID)}
		out, lsofErr := runner.Run("lsof", args...)
		procs[i].OpenFilesOutput = string(out)
		procs[i].OpenFilesErr = lsofErr
		procs[i].OpenFiles = vmProcessOpenFiles(baseDir, knownVMs, parseLsofPaths(out))
		procs[i].VMDirs = vmDirsForOpenFiles(baseDir, knownVMs, procs[i].OpenFiles)
		if len(procs[i].VMDirs) > 0 {
			procs[i].Source = "open-files"
		}
		if len(procs[i].VMDirs) == 0 {
			if vm := vmProcessVMForOwnerPID(knownVMs, procs[i].PPID); vm != nil {
				procs[i].VMDirs = []string{vm.Path}
				procs[i].Source = "server-info"
			}
		}
		procs[i].Status = vmProcessStatus(procs[i].VMDirs)
	}
	return procs, nil
}

func collectHelperProcesses(runner vmProcessRunner) ([]helperProcessInfo, error) {
	if runner == nil {
		return nil, errors.New("vm process runner required")
	}
	out, err := runner.Run("ps", "-axo", "pid=,ppid=,pcpu=,rss=,command=")
	if err != nil {
		return nil, fmt.Errorf("list helper processes: %w", err)
	}
	helpers := parseHelperProcessPS(out)
	sort.Slice(helpers, func(i, j int) bool {
		return helpers[i].PID < helpers[j].PID
	})
	return helpers, nil
}

func parseHelperProcessPS(out []byte) []helperProcessInfo {
	var helpers []helperProcessInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		rss, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		command := strings.Join(fields[4:], " ")
		if !isCoveHelperProcess(command) {
			continue
		}
		helpers = append(helpers, helperProcessInfo{
			PID:     pid,
			PPID:    ppid,
			CPU:     fields[2],
			RSSKB:   rss,
			Command: command,
		})
	}
	return helpers
}

func isCoveHelperProcess(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	base := strings.Trim(filepath.Base(fields[0]), "()")
	if base == "cove-helper" || strings.Contains(command, "/cove-helper") {
		return true
	}
	if !strings.Contains(base, "cove") {
		return false
	}
	for i := 1; i+1 < len(fields); i++ {
		if fields[i] == "helper" && fields[i+1] == "daemon" {
			return true
		}
	}
	return false
}

func collectVMProcessVMs() []vmProcessVMInfo {
	vms, err := vmProcessListVMs()
	if err != nil {
		return nil
	}
	out := make([]vmProcessVMInfo, 0, len(vms))
	for _, vm := range vms {
		path := filepath.Clean(vm.Path)
		item := vmProcessVMInfo{
			Name:     vm.Name,
			Path:     path,
			RealPath: vmProcessRealPath(path),
		}
		if info, ok := vmProcessServerInfo(GetControlSocketPathForVM(path)); ok {
			item.OwnerPID = info.PID
			if info.VMDir != "" {
				item.Path = filepath.Clean(info.VMDir)
				item.RealPath = vmProcessRealPath(info.VMDir)
			}
			item.SocketPath = info.SocketPath
		}
		out = append(out, item)
	}
	return out
}

func serverInfoForVMProcess(sock string) (RuntimeServerInfo, bool) {
	resp, err := ctlSendJSON(sock, map[string]interface{}{"type": "server-info"}, 500*time.Millisecond)
	if err != nil || resp == nil || !resp.Success || resp.Data == "" {
		return RuntimeServerInfo{}, false
	}
	var info RuntimeServerInfo
	if err := json.Unmarshal([]byte(resp.Data), &info); err != nil {
		return RuntimeServerInfo{}, false
	}
	if info.PID == 0 {
		return RuntimeServerInfo{}, false
	}
	enrichRuntimeServerInfo(&info)
	return info, true
}

func parseVMProcessPS(out []byte) []vmProcessInfo {
	var procs []vmProcessInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid := 0
		commandField := 1
		if len(fields) >= 3 {
			if n, err := strconv.Atoi(fields[1]); err == nil {
				ppid = n
				commandField = 2
			}
		}
		command := strings.Join(fields[commandField:], " ")
		if !isVirtualizationVMProcess(command) {
			continue
		}
		procs = append(procs, vmProcessInfo{PID: pid, PPID: ppid, Command: command})
	}
	return procs
}

func isVirtualizationVMProcess(command string) bool {
	return strings.Contains(command, "com.apple.Virtualization.VirtualMachine.xpc") ||
		strings.Contains(command, "Virtual Machine Service")
}

func parseLsofPaths(out []byte) []string {
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 2 || line[0] != 'n' {
			continue
		}
		path := strings.TrimSpace(line[1:])
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func vmProcessOpenFiles(baseDir string, knownVMs []vmProcessVMInfo, paths []string) []string {
	baseDir = filepath.Clean(baseDir)
	seen := make(map[string]bool)
	var files []string
	for _, path := range paths {
		path = filepath.Clean(path)
		if !vmProcessPathInKnownRoots(baseDir, knownVMs, path) || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	sort.Strings(files)
	return files
}

func vmDirsForOpenFiles(baseDir string, knownVMs []vmProcessVMInfo, files []string) []string {
	baseDir = filepath.Clean(baseDir)
	seen := make(map[string]bool)
	var dirs []string
	for _, file := range files {
		file = filepath.Clean(file)
		if vm := vmProcessVMForPath(knownVMs, file); vm != nil {
			if !seen[vm.Path] {
				seen[vm.Path] = true
				dirs = append(dirs, vm.Path)
			}
			continue
		}
		if !pathWithinBase(baseDir, file) {
			continue
		}
		rel, err := filepath.Rel(baseDir, file)
		if err != nil || rel == "." {
			continue
		}
		name := rel
		if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
			name = rel[:i]
		}
		dir := filepath.Join(baseDir, name)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func vmProcessPathInKnownRoots(baseDir string, knownVMs []vmProcessVMInfo, path string) bool {
	if pathWithinBase(baseDir, path) {
		return true
	}
	return vmProcessVMForPath(knownVMs, path) != nil
}

func vmProcessVMForPath(knownVMs []vmProcessVMInfo, path string) *vmProcessVMInfo {
	realPath := vmProcessRealPath(path)
	for i := range knownVMs {
		vm := &knownVMs[i]
		if pathWithinBase(vm.Path, path) || (vm.RealPath != "" && pathWithinBase(vm.RealPath, realPath)) {
			return vm
		}
	}
	return nil
}

func vmProcessVMForOwnerPID(knownVMs []vmProcessVMInfo, ppid int) *vmProcessVMInfo {
	if ppid == 0 {
		return nil
	}
	for i := range knownVMs {
		if knownVMs[i].OwnerPID == ppid {
			return &knownVMs[i]
		}
	}
	return nil
}

func vmProcessRealPath(path string) string {
	realPath, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(realPath)
}

func pathWithinBase(baseDir, path string) bool {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func vmProcessStatus(dirs []string) string {
	if len(dirs) == 0 {
		return "unmanaged"
	}
	for _, dir := range dirs {
		if !vmconfig.Validate(dir) {
			return "orphan"
		}
	}
	return "managed"
}

func writeVMProcessReport(w io.Writer, baseDir string, procs []vmProcessInfo, helpers []helperProcessInfo, helperErr error) {
	fmt.Fprintln(w, "Apple Virtualization VM processes")
	fmt.Fprintf(w, "Base dir: %s\n", baseDir)
	if len(procs) == 0 {
		fmt.Fprintln(w, "No Apple Virtualization VM XPC processes found.")
	} else {
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PID\tSTATUS\tVM\tSOURCE\tOPEN FILE")
		for _, proc := range procs {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", proc.PID, proc.Status, vmProcessLabel(proc.VMDirs), vmProcessSourceLabel(proc), vmProcessOpenFileLabel(proc))
		}
		tw.Flush()
	}
	writeHelperProcessReport(w, helpers, helperErr)

	needsReview := false
	for _, proc := range procs {
		if proc.Status == "managed" && proc.OpenFilesErr == nil {
			continue
		}
		needsReview = true
		break
	}
	if !needsReview {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Review:")
	for _, proc := range procs {
		switch proc.Status {
		case "managed":
			if proc.OpenFilesErr != nil {
				fmt.Fprintf(w, "  - PID %d: open-file inspection returned %v after reporting cove VM files; verify process ownership before stopping it.\n", proc.PID, proc.OpenFilesErr)
			}
		case "unmanaged":
			fmt.Fprintf(w, "  - PID %d: no open files under the cove VM directory were visible; confirm the owner with `ps -p %d -o pid,ppid,user,command` before stopping anything.\n", proc.PID, proc.PID)
		case "orphan":
			fmt.Fprintf(w, "  - PID %d: open files point at an invalid cove VM directory; stop the owning cove process first, then inspect the directory before moving or deleting it.\n", proc.PID)
		}
	}
}

func writeHelperProcessReport(w io.Writer, helpers []helperProcessInfo, helperErr error) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "cove-helper process")
	plist, binary, socket := helperInstallState()
	fmt.Fprintf(w, "Install state: plist=%s binary=%s socket=%s\n", yesNo(plist), yesNo(binary), yesNo(socket))
	if helperErr != nil {
		fmt.Fprintf(w, "Process state: unknown (%v)\n", helperErr)
		fmt.Fprintln(w, "Next step: run: cove helper status. Do not stop helper processes until ownership is clear.")
		return
	}
	if len(helpers) == 0 {
		fmt.Fprintln(w, "Process state: not running")
		if plist || binary || socket {
			fmt.Fprintln(w, "Next step: run: cove helper status. Refresh with: sudo cove helper install, if the installed helper is stale or stopped.")
			return
		}
		fmt.Fprintln(w, "Next step: no helper action is required unless you want to avoid future admin prompts. Install with: sudo cove helper install.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PID\tPPID\tCPU%\tRSS(KiB)\tCOMMAND")
	for _, helper := range helpers {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%d\t%s\n", helper.PID, helper.PPID, helper.CPU, helper.RSSKB, helper.Command)
	}
	tw.Flush()
	if len(helpers) > 1 {
		fmt.Fprintln(w, "Next step: multiple helper-like processes are visible; confirm with `ps -p <pid> -o pid,ppid,user,command` before stopping anything.")
		return
	}
	fmt.Fprintf(w, "Next step: run: cove helper status. Helper is active and not owned by a specific VM; inspect ownership with: ps -p %d -o pid,ppid,user,command before stopping it.\n", helpers[0].PID)
}

func helperInstallState() (plist, binary, socket bool) {
	return pathExists(helperPlistPath), pathExists(helperBinaryPath), pathExists(helperSocketPath)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func vmProcessLabel(dirs []string) string {
	if len(dirs) == 0 {
		return "-"
	}
	labels := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		labels = append(labels, filepath.Base(dir))
	}
	return strings.Join(labels, ",")
}

func vmProcessOpenFileLabel(proc vmProcessInfo) string {
	if len(proc.OpenFiles) == 0 {
		if proc.OpenFilesErr != nil {
			return fmt.Sprintf("not visible (%v)", proc.OpenFilesErr)
		}
		return "none under base dir"
	}
	return proc.OpenFiles[0]
}

func vmProcessSourceLabel(proc vmProcessInfo) string {
	if proc.Source != "" {
		return proc.Source
	}
	return "-"
}
