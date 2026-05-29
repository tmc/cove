package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/configcodec"
	"github.com/tmc/apple/x/vzkit/storagehotplug"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"
	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/vmconfig"
	"golang.org/x/sys/unix"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

type PITSnapshotInfo struct {
	Name                  string                    `json:"name"`
	Created               time.Time                 `json:"created"`
	FilePath              string                    `json:"filePath"`
	DiskFileName          string                    `json:"diskFileName"`
	DiskPath              string                    `json:"diskPath"`
	DiskSize              int64                     `json:"diskSize,omitempty"`
	VMStatePath           string                    `json:"vmStatePath"`
	VMStateSize           int64                     `json:"vmStateSize,omitempty"`
	FrameworkConfigPath   string                    `json:"frameworkConfigPath,omitempty"`
	FrameworkConfigFormat uint64                    `json:"frameworkConfigFormat,omitempty"`
	FrameworkConfigBytes  int                       `json:"frameworkConfigBytes,omitempty"`
	FrameworkConfigError  string                    `json:"frameworkConfigError,omitempty"`
	SuspendConfig         *suspendConfigFingerprint `json:"suspendConfig,omitempty"`
	StateDescription      string                    `json:"stateDescription,omitempty"`
}

type PITSnapshotManager struct {
	vmDir string
}

type PITSaveHooks struct {
	Now                  func() time.Time
	Pause                func() (bool, error)
	Resume               func() error
	SaveState            func(string) error
	CloneDisk            func(string) (int64, error)
	CurrentConfiguration func() (objectivec.IObject, error)
	EncodeConfiguration  func(string, objectivec.IObject) ([]byte, configcodec.Format, error)
	Fingerprint          func() suspendConfigFingerprint
	StateDescription     func() string
}

type PITActionRequest struct {
	Action string `json:"action,omitempty"`
	Name   string `json:"name,omitempty"`
	RAM    bool   `json:"ram,omitempty"`
}

type PITActionResponse struct {
	Action         string `json:"action"`
	Snapshot       string `json:"snapshot"`
	Path           string `json:"path,omitempty"`
	DiskPath       string `json:"diskPath,omitempty"`
	AttachmentMode string `json:"attachmentMode,omitempty"`
	Message        string `json:"message"`
}

type pitJSONEnvelope struct {
	Data json.RawMessage `json:"data,omitempty"`
}

func NewPITSnapshotManager(vmDir string) *PITSnapshotManager {
	return &PITSnapshotManager{vmDir: vmDir}
}

func (m *PITSnapshotManager) baseDir() string {
	return filepath.Join(m.vmDir, "pit")
}

func (m *PITSnapshotManager) snapshotDir(name string) string {
	return filepath.Join(m.baseDir(), name)
}

func (m *PITSnapshotManager) manifestPath(name string) string {
	return filepath.Join(m.snapshotDir(name), "manifest.json")
}

func (m *PITSnapshotManager) statePath(name string) string {
	return filepath.Join(m.snapshotDir(name), "state.vmstate")
}

func (m *PITSnapshotManager) diskFileName() string {
	if vmconfig.DetectOSType(m.vmDir) == "Linux" {
		return "linux-disk.img"
	}
	return "disk.img"
}

func (m *PITSnapshotManager) diskPath(name string) string {
	return filepath.Join(m.snapshotDir(name), m.diskFileName())
}

func (m *PITSnapshotManager) frameworkConfigPath(name string) string {
	return filepath.Join(m.snapshotDir(name), vmFrameworkConfigFileName)
}

func (m *PITSnapshotManager) ensureDir() error {
	return os.MkdirAll(m.baseDir(), 0755)
}

func (m *PITSnapshotManager) Save(name string, hooks PITSaveHooks) (err error) {
	if err := validateSnapshotName(name); err != nil {
		return err
	}
	if hooks.SaveState == nil {
		return fmt.Errorf("save state hook is required")
	}
	if hooks.CloneDisk == nil {
		return fmt.Errorf("clone disk hook is required")
	}
	if err := m.ensureDir(); err != nil {
		return fmt.Errorf("create pit snapshot directory: %w", err)
	}

	dir := m.snapshotDir(name)
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		return fmt.Errorf("pit snapshot %q already exists", name)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create pit snapshot %q: %w", name, err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()

	now := hooks.Now
	if now == nil {
		now = time.Now
	}
	info := PITSnapshotInfo{
		Name:         name,
		Created:      now(),
		FilePath:     dir,
		DiskFileName: m.diskFileName(),
		DiskPath:     m.diskPath(name),
		VMStatePath:  m.statePath(name),
	}

	if hooks.Pause != nil {
		resumeNeeded, pauseErr := hooks.Pause()
		if pauseErr != nil {
			return pauseErr
		}
		if resumeNeeded && hooks.Resume != nil {
			defer func() {
				if resumeErr := hooks.Resume(); resumeErr != nil {
					if err == nil {
						err = fmt.Errorf("resume vm: %w", resumeErr)
						return
					}
					err = fmt.Errorf("%v; resume vm: %w", err, resumeErr)
				}
			}()
		}
	}

	if err := hooks.SaveState(info.VMStatePath); err != nil {
		return fmt.Errorf("save vm state: %w", err)
	}
	if stat, statErr := os.Stat(info.VMStatePath); statErr == nil {
		info.VMStateSize = stat.Size()
	}

	diskSize, err := hooks.CloneDisk(info.DiskPath)
	if err != nil {
		return fmt.Errorf("snapshot disk: %w", err)
	}
	if diskSize > 0 {
		info.DiskSize = diskSize
	} else if stat, statErr := os.Stat(info.DiskPath); statErr == nil {
		info.DiskSize = stat.Size()
	}

	if hooks.StateDescription != nil {
		info.StateDescription = hooks.StateDescription()
	}
	if hooks.Fingerprint != nil {
		fp := hooks.Fingerprint()
		info.SuspendConfig = &fp
	}

	if hooks.CurrentConfiguration != nil {
		cfg, cfgErr := hooks.CurrentConfiguration()
		switch {
		case cfgErr != nil:
			info.FrameworkConfigError = cfgErr.Error()
		case cfg != nil && cfg.GetID() != 0:
			encodeFn := hooks.EncodeConfiguration
			if encodeFn == nil {
				encodeFn = encodePITConfigurationSnapshot
			}
			encoded, format, encodeErr := encodeFn(dir, cfg)
			if encodeErr != nil {
				info.FrameworkConfigError = encodeErr.Error()
			} else {
				snapshot := marshalFrameworkConfigSnapshot(format, encoded)
				path := m.frameworkConfigPath(name)
				if writeErr := writeFrameworkConfigBytes(path, snapshot); writeErr != nil {
					info.FrameworkConfigError = writeErr.Error()
				} else {
					info.FrameworkConfigPath = path
					info.FrameworkConfigFormat = uint64(format)
					info.FrameworkConfigBytes = len(snapshot)
				}
			}
		}
	}

	data, marshalErr := json.MarshalIndent(info, "", "  ")
	if marshalErr != nil {
		return fmt.Errorf("marshal pit snapshot manifest: %w", marshalErr)
	}
	if writeErr := os.WriteFile(m.manifestPath(name), append(data, '\n'), 0644); writeErr != nil {
		return fmt.Errorf("write pit snapshot manifest: %w", writeErr)
	}
	return nil
}

func (m *PITSnapshotManager) Load(name string) (PITSnapshotInfo, error) {
	if err := validateSnapshotName(name); err != nil {
		return PITSnapshotInfo{}, err
	}

	data, err := os.ReadFile(m.manifestPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return PITSnapshotInfo{}, fmt.Errorf("pit snapshot %q not found", name)
		}
		return PITSnapshotInfo{}, fmt.Errorf("read pit snapshot manifest: %w", err)
	}

	var info PITSnapshotInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return PITSnapshotInfo{}, fmt.Errorf("parse pit snapshot manifest: %w", err)
	}
	if info.Name == "" {
		info.Name = name
	}
	if info.FilePath == "" {
		info.FilePath = m.snapshotDir(name)
	}
	if info.DiskFileName == "" {
		info.DiskFileName = m.diskFileName()
	}
	if info.DiskPath == "" {
		info.DiskPath = filepath.Join(info.FilePath, info.DiskFileName)
	}
	if info.VMStatePath == "" {
		info.VMStatePath = filepath.Join(info.FilePath, "state.vmstate")
	}
	if info.FrameworkConfigPath == "" {
		path := filepath.Join(info.FilePath, vmFrameworkConfigFileName)
		if _, err := os.Stat(path); err == nil {
			info.FrameworkConfigPath = path
		}
	}
	if stat, err := os.Stat(info.DiskPath); err == nil && info.DiskSize == 0 {
		info.DiskSize = stat.Size()
	}
	if stat, err := os.Stat(info.VMStatePath); err == nil && info.VMStateSize == 0 {
		info.VMStateSize = stat.Size()
	}
	return info, nil
}

func (m *PITSnapshotManager) List() ([]PITSnapshotInfo, error) {
	if _, err := os.Stat(m.baseDir()); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(m.baseDir())
	if err != nil {
		return nil, fmt.Errorf("read pit snapshot directory: %w", err)
	}

	snapshots := make([]PITSnapshotInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := m.Load(entry.Name())
		if err != nil {
			return nil, err
		}
		if info.Created.IsZero() {
			if stat, statErr := entry.Info(); statErr == nil {
				info.Created = stat.ModTime()
			}
		}
		snapshots = append(snapshots, info)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Created.After(snapshots[j].Created)
	})
	return snapshots, nil
}

func (m *PITSnapshotManager) Restore(name string) error {
	if isVMRunningAt(m.vmDir) {
		return fmt.Errorf("vm must be stopped before restoring a pit snapshot")
	}

	info, err := m.Load(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(info.DiskPath); err != nil {
		return fmt.Errorf("pit disk image missing: %w", err)
	}
	if _, err := os.Stat(info.VMStatePath); err != nil {
		return fmt.Errorf("pit vm state missing: %w", err)
	}

	dstDisk := filepath.Join(m.vmDir, info.DiskFileName)
	if err := os.Remove(dstDisk); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove active disk: %w", err)
	}
	if err := cloneFileWithCopyFallback(info.DiskPath, dstDisk); err != nil {
		return fmt.Errorf("restore disk: %w", err)
	}

	stateDst := suspendStatePathForVM(m.vmDir)
	if err := os.Remove(stateDst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove active suspend state: %w", err)
	}
	if err := cloneFileWithCopyFallback(info.VMStatePath, stateDst); err != nil {
		return fmt.Errorf("restore vm state: %w", err)
	}

	configDst := suspendConfigPathForVM(m.vmDir)
	if err := os.Remove(configDst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove saved suspend config: %w", err)
	}
	if info.SuspendConfig != nil {
		data, err := json.MarshalIndent(info.SuspendConfig, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal suspend config: %w", err)
		}
		if err := os.WriteFile(configDst, append(data, '\n'), 0644); err != nil {
			return fmt.Errorf("write suspend config: %w", err)
		}
	}

	if info.FrameworkConfigPath != "" {
		if _, err := os.Stat(info.FrameworkConfigPath); err == nil {
			if err := cloneFileWithCopyFallback(info.FrameworkConfigPath, filepath.Join(m.vmDir, vmFrameworkConfigFileName)); err != nil {
				return fmt.Errorf("restore framework config snapshot: %w", err)
			}
		}
	}

	return nil
}

func (m *PITSnapshotManager) Delete(name string) error {
	if err := validateSnapshotName(name); err != nil {
		return err
	}
	path := m.snapshotDir(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("pit snapshot %q not found", name)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete pit snapshot: %w", err)
	}
	return nil
}

func encodePITConfigurationSnapshot(basePath string, configuration objectivec.IObject) ([]byte, configcodec.Format, error) {
	formats := []configcodec.Format{
		configcodec.DefaultFormat,
		100,
		200,
	}
	var errs []string
	for _, format := range formats {
		encoded, err := configcodec.EncodeAtBasePath(basePath, configuration, format)
		if err == nil && len(encoded) > 0 {
			return encoded, format, nil
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("format %d: %v", format, err))
			continue
		}
		errs = append(errs, fmt.Sprintf("format %d: empty data", format))
	}
	return nil, configcodec.DefaultFormat, fmt.Errorf("%s", strings.Join(errs, "; "))
}

func cloneFileWithCopyFallback(src, dst string) error {
	if err := unix.Clonefile(src, dst, 0); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

func parsePITActionRequest(rawJSON []byte) (PITActionRequest, error) {
	if len(strings.TrimSpace(string(rawJSON))) == 0 {
		return PITActionRequest{}, fmt.Errorf("empty request")
	}

	var env pitJSONEnvelope
	if err := json.Unmarshal(rawJSON, &env); err == nil && len(env.Data) > 0 {
		rawJSON = env.Data
	}

	var req PITActionRequest
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		return PITActionRequest{}, fmt.Errorf("parse pit request: %w", err)
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Name = strings.TrimSpace(req.Name)
	return req, nil
}

func (s *ControlServer) handlePITJSONRequest(rawJSON []byte) *controlpb.ControlResponse {
	req, err := parsePITActionRequest(rawJSON)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if req.Action == "" {
		return &controlpb.ControlResponse{Error: "pit action required"}
	}

	switch req.Action {
	case "save":
		return s.handlePITSave(req.Name)
	case "swap":
		return s.handlePITSwap(req.Name, req.RAM)
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown pit action: %s (use save or swap)", req.Action)}
	}
}

func (s *ControlServer) handlePITSave(name string) *controlpb.ControlResponse {
	if name == "" {
		return &controlpb.ControlResponse{Error: "pit snapshot name required"}
	}
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}
	if runtimeSystemDiskAttachment == systemDiskAttachmentTemporaryRAM {
		return &controlpb.ControlResponse{Error: "pit save is not supported while booted from a temporary RAM disk attachment"}
	}

	queue := vmruntime.WrapQueue(s.vmQueue)
	manager := NewPITSnapshotManager(s.vmDir)
	hooks := PITSaveHooks{
		Pause: func() (bool, error) {
			state := vmruntime.State(queue, s.vm)
			switch state {
			case vz.VZVirtualMachineStateRunning:
				done := make(chan error, 1)
				queue.Sync(func() {
					s.vm.PauseWithCompletionHandler(func(err error) {
						done <- snapshotNSError(err)
					})
				})
				if err := <-done; err != nil {
					return false, fmt.Errorf("pause vm: %w", err)
				}
				return true, nil
			case vz.VZVirtualMachineStatePaused:
				return false, nil
			default:
				return false, fmt.Errorf("VM must be running or paused to save PIT snapshot (current state: %s)", state.String())
			}
		},
		Resume: func() error {
			if vmruntime.State(queue, s.vm) != vz.VZVirtualMachineStatePaused {
				return nil
			}
			done := make(chan error, 1)
			queue.Sync(func() {
				s.vm.ResumeWithCompletionHandler(func(err error) {
					done <- snapshotNSError(err)
				})
			})
			return <-done
		},
		SaveState: func(path string) error {
			saveURL := foundation.NewURLFileURLWithPath(path)
			saveURL.Retain()
			done := make(chan error, 1)
			s.mu.Lock()
			rc := s.runConfig
			s.mu.Unlock()
			queue.Sync(func() {
				saveMachineStateWithRunConfig(s.vm, saveURL, rc, func(err error) {
					done <- snapshotNSError(err)
				})
			})
			select {
			case err := <-done:
				if err != nil {
					return err
				}
				return nil
			case <-time.After(120 * time.Second):
				return fmt.Errorf("save state timed out")
			}
		},
		CloneDisk: func(dst string) (int64, error) {
			src, err := currentVMFrameworkDiskPath()
			if err != nil {
				return 0, err
			}
			if err := cloneFileWithCopyFallback(src, dst); err != nil {
				return 0, err
			}
			if stat, err := os.Stat(src); err == nil {
				return stat.Size(), nil
			}
			return 0, nil
		},
		Fingerprint: func() suspendConfigFingerprint {
			s.mu.Lock()
			rc := s.runConfig
			hc := s.hostConfig
			s.mu.Unlock()
			if hc.VMDir == "" {
				hc.VMDir = s.vmDir
			}
			return currentConfigFingerprintForRun(rc, hc)
		},
		StateDescription: func() string {
			var desc string
			queue.Sync(func() {
				id := objc.Send[objc.ID](s.vm.ID, objc.Sel("_stateDescription"))
				if id != 0 {
					desc = foundation.NSStringFromID(id).String()
				}
			})
			return desc
		},
	}
	if err := manager.Save(name, hooks); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	info, err := manager.Load(name)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return statusControlResponse(PITActionResponse{
		Action:   "save",
		Snapshot: info.Name,
		Path:     info.FilePath,
		Message:  fmt.Sprintf("saved pit snapshot %q", info.Name),
	})
}

func (s *ControlServer) handlePITSwap(name string, ram bool) *controlpb.ControlResponse {
	if name == "" {
		return &controlpb.ControlResponse{Error: "pit snapshot name required"}
	}
	entry, err := s.runtimeDiskEntryByIndex(0)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	manager := NewPITSnapshotManager(s.vmDir)
	info, err := manager.Load(name)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	mode := systemDiskAttachmentDiskImage
	if ram {
		mode = systemDiskAttachmentTemporaryRAM
	}
	attachment, err := createRuntimeStorageDeviceAttachment(info.DiskPath, false, mode)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	ctx, cancel := s.timeoutContext(30 * time.Second)
	defer cancel()
	if err := storagehotplug.SetAttachment(ctx, vmruntime.WrapQueue(s.vmQueue), entry.Device, attachment); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("swap pit disk attachment: %v", err)}
	}

	return statusControlResponse(PITActionResponse{
		Action:         "swap",
		Snapshot:       info.Name,
		DiskPath:       info.DiskPath,
		AttachmentMode: mode.String(),
		Message:        fmt.Sprintf("swapped live disk 0 to pit snapshot %q", info.Name),
	})
}

func handlePITCommand(args []string) error {
	if len(args) == 0 {
		printPITUsage()
		return nil
	}

	manager := NewPITSnapshotManager(vmDir)
	switch args[0] {
	case "help", "-h", "--help":
		printPITUsage()
		return nil
	case "save":
		return handlePITSaveCommand(args[1:])
	case "list":
		return handlePITListCommand(manager)
	case "restore":
		return handlePITRestoreCommand(manager, args[1:])
	case "delete":
		return handlePITDeleteCommand(manager, args[1:])
	case "run":
		return handlePITRunCommand(manager, args[1:])
	case "swap":
		return handlePITSwapCommand(args[1:])
	default:
		printPITUsage()
		return fmt.Errorf("unknown pit command: %s\nRun 'cove help pit' for usage.", args[0])
	}
}

func printPITUsage() {
	fmt.Println(`Usage: cove pit <command> [options]

Experimental point-in-time save and recovery.

Commands:
  save <name>
      Pause the running VM, save VM state, clone the system disk, and capture
      the effective framework configuration

  list
      List saved PIT snapshots

  restore <name>
      Restore the selected PIT snapshot into disk.img + suspend.vmstate so the
      next 'cove run' resumes from that point

  run <name> [-ram]
      Boot a disposable clone from the PIT disk; -ram uses a temporary RAM
      storage attachment for the system disk

  swap <name> [-ram]
      Live-swap disk 0 in the running VM to the PIT disk; -ram preserves the
      saved disk by switching to a temporary RAM attachment

  delete <name>
      Delete a PIT snapshot

Examples:
  cove pit save checkpoint1
  cove pit restore checkpoint1
  cove pit run checkpoint1 -ram
  cove pit swap checkpoint1 -ram`)
}

func handlePITSaveCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pit snapshot name required")
	}
	resp, err := pitActionViaControlSocket(map[string]any{
		"action": "save",
		"name":   args[0],
	}, 5*time.Minute)
	if err != nil {
		return err
	}
	return ctlPrintResponse(resp, "pit", false, "")
}

func handlePITListCommand(manager *PITSnapshotManager) error {
	snapshots, err := manager.List()
	if err != nil {
		return err
	}
	if len(snapshots) == 0 {
		fmt.Println("No PIT snapshots found.")
		return nil
	}

	fmt.Println("PIT snapshots:")
	fmt.Printf("  %-20s  %-10s  %-10s  %-6s  %s\n", "NAME", "VMSTATE", "DISK", "CFG", "CREATED")
	for _, snapshot := range snapshots {
		cfgStatus := "-"
		switch {
		case snapshot.FrameworkConfigPath != "":
			cfgStatus = "ok"
		case snapshot.FrameworkConfigError != "":
			cfgStatus = "err"
		}
		fmt.Printf("  %-20s  %-10s  %-10s  %-6s  %s\n",
			snapshot.Name,
			formatPITSize(snapshot.VMStateSize),
			formatPITSize(snapshot.DiskSize),
			cfgStatus,
			snapshot.Created.Format("2006-01-02 15:04"))
	}
	return nil
}

func handlePITRestoreCommand(manager *PITSnapshotManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pit snapshot name required")
	}
	name := args[0]
	fmt.Printf("Restoring PIT snapshot %q...\n", name)
	if err := manager.Restore(name); err != nil {
		return err
	}
	fmt.Printf("PIT snapshot %q restored into %s\n", name, vmDir)
	fmt.Println("Next run will resume from the restored point-in-time state.")
	return nil
}

func handlePITDeleteCommand(manager *PITSnapshotManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pit snapshot name required")
	}
	name := args[0]
	ok, err := confirmDeletef("Delete PIT snapshot %q? This cannot be undone. [y/N] ", name)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := manager.Delete(name); err != nil {
		return err
	}
	fmt.Printf("PIT snapshot %q deleted\n", name)
	return nil
}

func handlePITRunCommand(manager *PITSnapshotManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pit snapshot name required")
	}
	name := args[0]
	ram := false
	for _, arg := range args[1:] {
		switch arg {
		case "-ram":
			ram = true
		default:
			return fmt.Errorf("unknown pit run option: %s", arg)
		}
	}

	info, err := manager.Load(name)
	if err != nil {
		return err
	}
	fmt.Printf("Running disposable clone from PIT snapshot %q...\n", name)
	if ram {
		fmt.Println("Using temporary RAM system-disk attachment.")
	}
	return runDisposableCloneFromDiskPath(selectedVMSourceName(), info.DiskPath, pitRunAttachmentMode(ram))
}

func handlePITSwapCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pit snapshot name required")
	}
	name := args[0]
	ram := false
	for _, arg := range args[1:] {
		switch arg {
		case "-ram":
			ram = true
		default:
			return fmt.Errorf("unknown pit swap option: %s", arg)
		}
	}
	resp, err := pitActionViaControlSocket(map[string]any{
		"action": "swap",
		"name":   name,
		"ram":    ram,
	}, 30*time.Second)
	if err != nil {
		return err
	}
	return ctlPrintResponse(resp, "pit", false, "")
}

func pitActionViaControlSocket(data map[string]any, timeout time.Duration) (*controlpb.ControlResponse, error) {
	sock := GetControlSocketPath()
	resp, err := ctlSendJSON(sock, map[string]any{
		"type": "pit",
		"data": data,
	}, timeout)
	if err != nil {
		return nil, fmt.Errorf("%w\n\nNote: PIT save/swap require a running VM with the control socket active", err)
	}
	return resp, nil
}

func pitRunAttachmentMode(ram bool) systemDiskAttachmentMode {
	if ram {
		return systemDiskAttachmentTemporaryRAM
	}
	return systemDiskAttachmentDiskImage
}

func formatPITSize(size int64) string {
	if size <= 0 {
		return "-"
	}
	return bytefmt.Size(size)
}
