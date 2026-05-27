package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	storagex "github.com/tmc/apple/x/vzkit/storage"
	"github.com/tmc/apple/x/vzkit/storagehotplug"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"

	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/vmconfig"
	agentpb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

const macOSResizeAPFSTimeout = 5 * time.Minute

const macOSResizeAPFSManualCommand = "find the APFS Container with `/usr/sbin/diskutil info /`, then run `/usr/sbin/diskutil apfs resizeContainer <container> 0` as root"

// RuntimeDiskInfo describes one live storage device attached to a VM.
type RuntimeDiskInfo struct {
	Index         int    `json:"index"`
	Kind          string `json:"kind,omitempty"`
	Path          string `json:"path,omitempty"`
	ReadOnly      bool   `json:"read_only,omitempty"`
	FileSizeBytes uint64 `json:"file_size_bytes,omitempty"`
	ModTime       string `json:"mod_time,omitempty"`
	Description   string `json:"description,omitempty"`
}

// RuntimeDiskActionRequest is the flat JSON request shape for runtime disk
// commands. It accepts both snake_case and camelCase aliases for convenience.
type RuntimeDiskActionRequest struct {
	Action       string   `json:"action,omitempty"`
	Type         string   `json:"type,omitempty"`
	Index        *int     `json:"index,omitempty"`
	StorageIndex *int     `json:"storage_index,omitempty"`
	Path         string   `json:"path,omitempty"`
	PathAlt      string   `json:"file_path,omitempty"`
	ReadOnly     *bool    `json:"read_only,omitempty"`
	ReadOnlyAlt  *bool    `json:"readOnly,omitempty"`
	SizeBytes    *uint64  `json:"size_bytes,omitempty"`
	SizeBytesAlt *uint64  `json:"sizeBytes,omitempty"`
	SizeMB       *uint64  `json:"size_mb,omitempty"`
	SizeMBAlt    *uint64  `json:"sizeMB,omitempty"`
	SizeGB       *float64 `json:"size_gb,omitempty"`
	SizeGBAlt    *float64 `json:"sizeGB,omitempty"`
}

// RuntimeDiskListResponse is returned by list commands.
type RuntimeDiskListResponse struct {
	Action  string            `json:"action"`
	Count   int               `json:"count"`
	Summary string            `json:"summary,omitempty"`
	Disks   []RuntimeDiskInfo `json:"disks"`
}

// RuntimeDiskMutationResponse is returned by swap/resize commands.
type RuntimeDiskMutationResponse struct {
	Action      string                  `json:"action"`
	Index       int                     `json:"index"`
	Disk        RuntimeDiskInfo         `json:"disk"`
	GuestResize *RuntimeDiskGuestResize `json:"guest_resize,omitempty"`
	Message     string                  `json:"message"`
}

// RuntimeDiskGuestResize describes a guest-side filesystem expansion performed
// after the live backing disk grew.
type RuntimeDiskGuestResize struct {
	Platform                  string `json:"platform"`
	Attempted                 bool   `json:"attempted"`
	Expanded                  bool   `json:"expanded"`
	Container                 string `json:"container,omitempty"`
	PhysicalStore             string `json:"physical_store,omitempty"`
	ContainerTotalBytesBefore uint64 `json:"container_total_bytes_before,omitempty"`
	ContainerTotalBytesAfter  uint64 `json:"container_total_bytes_after,omitempty"`
	Stdout                    string `json:"stdout,omitempty"`
	Stderr                    string `json:"stderr,omitempty"`
}

type runtimeDiskEntry struct {
	Index  int
	Device pvz.VZStorageDevice
	Info   RuntimeDiskInfo
}

type runtimeDiskGuestAgent interface {
	ResizeMacOSAPFS(ctx context.Context, preflightOnly bool) (*agentpb.ResizeMacOSAPFSResponse, error)
}

type runtimeDiskJSONEnvelope struct {
	Data json.RawMessage `json:"data,omitempty"`
}

func (r RuntimeDiskActionRequest) actionName() string {
	action := strings.TrimSpace(r.Action)
	if action == "" {
		action = strings.TrimSpace(r.Type)
	}
	return strings.ToLower(action)
}

func (r RuntimeDiskActionRequest) targetIndex() (int, bool) {
	if r.Index != nil {
		return *r.Index, true
	}
	if r.StorageIndex != nil {
		return *r.StorageIndex, true
	}
	return 0, false
}

func (r RuntimeDiskActionRequest) targetPath() string {
	if p := strings.TrimSpace(r.Path); p != "" {
		return p
	}
	return strings.TrimSpace(r.PathAlt)
}

func (r RuntimeDiskActionRequest) readOnlyValue() bool {
	if r.ReadOnly != nil {
		return *r.ReadOnly
	}
	if r.ReadOnlyAlt != nil {
		return *r.ReadOnlyAlt
	}
	return false
}

func (r RuntimeDiskActionRequest) requestedSizeBytes() (uint64, error) {
	var (
		size uint64
		set  bool
	)
	use := func(v uint64) error {
		if set {
			return fmt.Errorf("specify only one of size_bytes, size_mb, or size_gb")
		}
		size = v
		set = true
		return nil
	}
	if r.SizeBytes != nil && *r.SizeBytes > 0 {
		if err := use(*r.SizeBytes); err != nil {
			return 0, err
		}
	}
	if r.SizeBytesAlt != nil && *r.SizeBytesAlt > 0 {
		if err := use(*r.SizeBytesAlt); err != nil {
			return 0, err
		}
	}
	if r.SizeMB != nil && *r.SizeMB > 0 {
		if err := use(*r.SizeMB * 1024 * 1024); err != nil {
			return 0, err
		}
	}
	if r.SizeMBAlt != nil && *r.SizeMBAlt > 0 {
		if err := use(*r.SizeMBAlt * 1024 * 1024); err != nil {
			return 0, err
		}
	}
	if r.SizeGB != nil && *r.SizeGB > 0 {
		bytes := uint64(math.Round(*r.SizeGB * 1024 * 1024 * 1024))
		if err := use(bytes); err != nil {
			return 0, err
		}
	}
	if r.SizeGBAlt != nil && *r.SizeGBAlt > 0 {
		bytes := uint64(math.Round(*r.SizeGBAlt * 1024 * 1024 * 1024))
		if err := use(bytes); err != nil {
			return 0, err
		}
	}
	if !set {
		return 0, fmt.Errorf("size required")
	}
	return size, nil
}

func parseRuntimeDiskActionRequest(rawJSON []byte) (RuntimeDiskActionRequest, error) {
	if len(strings.TrimSpace(string(rawJSON))) == 0 {
		return RuntimeDiskActionRequest{}, fmt.Errorf("empty request")
	}

	var env runtimeDiskJSONEnvelope
	if err := json.Unmarshal(rawJSON, &env); err == nil && len(env.Data) > 0 {
		rawJSON = env.Data
	}

	var req RuntimeDiskActionRequest
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		return RuntimeDiskActionRequest{}, fmt.Errorf("parse runtime disk request: %w", err)
	}
	return req, nil
}

func (s *ControlServer) handleDiskJSONRequest(rawJSON []byte) *controlpb.ControlResponse {
	req, err := parseRuntimeDiskActionRequest(rawJSON)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	switch req.actionName() {
	case "list", "inspect":
		return s.handleDiskList()
	case "swap":
		if _, ok := req.targetIndex(); !ok {
			return &controlpb.ControlResponse{Error: "disk swap requires index"}
		}
		if strings.TrimSpace(req.targetPath()) == "" {
			return &controlpb.ControlResponse{Error: "disk swap requires path"}
		}
		return s.handleDiskSwap(req)
	case "resize":
		if _, ok := req.targetIndex(); !ok {
			return &controlpb.ControlResponse{Error: "disk resize requires index"}
		}
		return s.handleDiskResize(req)
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown disk action: %s (use list, swap, or resize)", req.actionName())}
	}
}

func (s *ControlServer) handleDiskList() *controlpb.ControlResponse {
	entries, err := s.runtimeDiskEntries()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	disks := make([]RuntimeDiskInfo, 0, len(entries))
	for _, entry := range entries {
		disks = append(disks, entry.Info)
	}
	return statusControlResponse(RuntimeDiskListResponse{
		Action:  "list",
		Count:   len(disks),
		Summary: runtimeDiskListSummary(disks),
		Disks:   disks,
	})
}

func (s *ControlServer) handleDiskSwap(req RuntimeDiskActionRequest) *controlpb.ControlResponse {
	idx, ok := req.targetIndex()
	if !ok {
		return &controlpb.ControlResponse{Error: "disk swap requires index"}
	}
	entry, err := s.runtimeDiskEntryByIndex(idx)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	path := req.targetPath()
	attachment, err := newRuntimeDiskImageAttachment(path, req.readOnlyValue())
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("create disk attachment: %v", err)}
	}

	ctx, cancel := s.timeoutContext(30 * time.Second)
	defer cancel()

	if err := storagehotplug.SetAttachment(ctx, vmruntime.WrapQueue(s.vmQueue), entry.Device, attachment.VZStorageDeviceAttachment); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("swap disk attachment: %v", err)}
	}

	updated, err := s.runtimeDiskEntryByIndex(entry.Index)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	msg := fmt.Sprintf("swapped disk %d", entry.Index)
	if updated.Info.Path != "" {
		msg = fmt.Sprintf("%s to %s", msg, updated.Info.Path)
	}
	return statusControlResponse(RuntimeDiskMutationResponse{
		Action:  "swap",
		Index:   entry.Index,
		Disk:    updated.Info,
		Message: msg,
	})
}

func newRuntimeDiskImageAttachment(path string, readOnly bool) (pvz.VZDiskImageStorageDeviceAttachment, error) {
	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return pvz.VZDiskImageStorageDeviceAttachment{}, fmt.Errorf("create file url")
	}
	attachment, err := newDiskAttachment(url, readOnly, storagex.CacheDurable)
	if err != nil {
		return pvz.VZDiskImageStorageDeviceAttachment{}, fmt.Errorf("create disk image attachment: %w", err)
	}
	attachment.Retain()
	return pvz.VZDiskImageStorageDeviceAttachmentFromID(attachment.ID), nil
}

func (s *ControlServer) handleDiskResize(req RuntimeDiskActionRequest) *controlpb.ControlResponse {
	idx, ok := req.targetIndex()
	if !ok {
		return &controlpb.ControlResponse{Error: "disk resize requires index"}
	}
	entry, err := s.runtimeDiskEntryByIndex(idx)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	sizeBytes, err := req.requestedSizeBytes()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	if current, ok := runtimeDiskCurrentSize(s.effectiveVMDir(), entry); ok && sizeBytes < current {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("disk resize can only grow disk %d: current size is %d bytes, requested %d bytes", entry.Index, current, sizeBytes)}
	}
	alreadySized := false
	if current, ok := runtimeDiskCurrentSize(s.effectiveVMDir(), entry); ok && sizeBytes == current {
		alreadySized = true
	}
	if entry.Info.ReadOnly {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("disk %d is read-only", entry.Index)}
	}

	attachment, ok := runtimeDiskDiskImageAttachment(entry.Device)
	diskPath := entry.Info.Path
	if diskPath == "" {
		diskPath = runtimeDiskConfiguredPath(s.effectiveVMDir(), entry)
	}
	if !ok {
		if diskPath == "" || entry.Index != 0 {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("disk %d is not backed by a disk image", entry.Index)}
		}
	}

	var macAgent runtimeDiskGuestAgent
	expandMacOSAPFS := runtimeDiskShouldExpandMacOSRootAPFS(s.effectiveVMDir(), entry)
	if expandMacOSAPFS {
		a, err := s.getAgent()
		if err != nil {
			return &controlpb.ControlResponse{Error: macOSResizeAgentUnavailableError(entry.Index, sizeBytes, err).Error()}
		}
		macAgent = a
		if err := s.preflightMacOSRootAPFS(macAgent); err != nil {
			return &controlpb.ControlResponse{Error: macOSResizePreflightFailedError(entry.Index, sizeBytes, alreadySized, err).Error()}
		}
	}

	if !alreadySized {
		if ok {
			if err := storagehotplug.UpdateDiskSize(attachment, sizeBytes); err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("resize disk attachment: %v", err)}
			}
		} else {
			newAttachment, err := newRuntimeDiskImageAttachment(diskPath, false)
			if err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("create disk attachment for %s: %v", diskPath, err)}
			}
			ctx, cancel := s.timeoutContext(30 * time.Second)
			defer cancel()
			if err := storagehotplug.SetAttachment(ctx, vmruntime.WrapQueue(s.vmQueue), entry.Device, newAttachment.VZStorageDeviceAttachment); err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("attach disk image %s before resize: %v", diskPath, err)}
			}
			if err := storagehotplug.UpdateDiskSize(newAttachment, sizeBytes); err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("resize disk attachment: %v", err)}
			}
		}
	}

	updated, err := s.runtimeDiskEntryByIndex(entry.Index)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("resized disk %d to %d bytes, but could not inspect updated disk: %v; next action: run `cove ctl disk list` and retry `cove ctl disk resize %d %dB` if the guest filesystem did not grow", entry.Index, sizeBytes, err, entry.Index, sizeBytes)}
	}
	if current, ok := runtimeDiskCurrentSize(s.effectiveVMDir(), updated); !ok || current < sizeBytes {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("resize disk %d did not reach requested size %d bytes", entry.Index, sizeBytes)}
	}

	var guestResize *RuntimeDiskGuestResize
	if expandMacOSAPFS {
		guestResize, err = s.expandMacOSRootAPFS(macAgent)
		if err != nil {
			return &controlpb.ControlResponse{Error: macOSResizeGuestFailedError(entry.Index, sizeBytes, err).Error()}
		}
	}

	msg := fmt.Sprintf("resized disk %d to %d bytes", entry.Index, sizeBytes)
	if alreadySized {
		msg = fmt.Sprintf("disk %d is already %d bytes", entry.Index, sizeBytes)
	}
	if guestResize != nil && guestResize.Expanded {
		msg += " and expanded macOS APFS container"
	} else if agentstate.Platform(s.effectiveVMDir()) == agentstate.PlatformMacOS && entry.Index != 0 {
		msg += "; guest filesystem expansion is automatic only for macOS primary disk 0"
	}
	return statusControlResponse(RuntimeDiskMutationResponse{
		Action:      "resize",
		Index:       entry.Index,
		Disk:        updated.Info,
		GuestResize: guestResize,
		Message:     msg,
	})
}

func runtimeDiskShouldExpandMacOSRootAPFS(vmDirectory string, entry runtimeDiskEntry) bool {
	return entry.Index == 0 && strings.EqualFold(vmconfig.DetectOSType(vmDirectory), "macOS")
}

func runtimeDiskCurrentSize(vmDirectory string, entry runtimeDiskEntry) (uint64, bool) {
	if entry.Info.FileSizeBytes > 0 {
		return entry.Info.FileSizeBytes, true
	}
	path := runtimeDiskConfiguredPath(vmDirectory, entry)
	if path == "" {
		return 0, false
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() < 0 {
		return 0, false
	}
	return uint64(info.Size()), true
}

func runtimeDiskConfiguredPath(vmDirectory string, entry runtimeDiskEntry) string {
	if entry.Info.Path != "" {
		return entry.Info.Path
	}
	if entry.Index != 0 {
		return ""
	}
	path := vmPrimaryDiskPath(vmDirectory)
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return path
	}
	return ""
}

func (s *ControlServer) expandMacOSRootAPFS(agent runtimeDiskGuestAgent) (*RuntimeDiskGuestResize, error) {
	if agent == nil {
		return nil, fmt.Errorf("guest agent unavailable")
	}
	ctx, cancel := s.timeoutContext(macOSResizeAPFSTimeout)
	defer cancel()

	res, err := agent.ResizeMacOSAPFS(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("run diskutil: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("run diskutil: missing response")
	}

	guestResize := &RuntimeDiskGuestResize{
		Platform:                  agentstate.PlatformMacOS,
		Attempted:                 true,
		Expanded:                  res.GetExpanded(),
		Container:                 res.GetContainer(),
		PhysicalStore:             res.GetPhysicalStore(),
		ContainerTotalBytesBefore: res.GetContainerTotalBytesBefore(),
		ContainerTotalBytesAfter:  res.GetContainerTotalBytesAfter(),
		Stdout:                    strings.TrimSpace(res.GetStdout()),
		Stderr:                    strings.TrimSpace(res.GetStderr()),
	}
	return guestResize, nil
}

func (s *ControlServer) preflightMacOSRootAPFS(agent runtimeDiskGuestAgent) error {
	if agent == nil {
		return fmt.Errorf("guest agent unavailable")
	}
	ctx, cancel := s.timeoutContext(30 * time.Second)
	defer cancel()

	res, err := agent.ResizeMacOSAPFS(ctx, true)
	if err != nil {
		return fmt.Errorf("run diskutil preflight: %w", err)
	}
	if res == nil {
		return fmt.Errorf("run diskutil preflight: missing response")
	}
	return nil
}

func macOSResizeAgentUnavailableError(index int, sizeBytes uint64, err error) error {
	return fmt.Errorf("macOS disk resize requires the root guest agent so cove can expand APFS after growing the backing image; no host disk changes made; next action: start the VM, wait for `cove ctl agent-ping` to succeed, then rerun `cove ctl disk resize %d %dB`: %w", index, sizeBytes, err)
}

func macOSResizePreflightFailedError(index int, sizeBytes uint64, alreadySized bool, err error) error {
	if alreadySized {
		return fmt.Errorf("macOS APFS expansion preflight failed for disk %d at %d bytes: %v; host disk is already grown, so recovery only needs APFS expansion; next action: fix the guest partition layout, then rerun `cove ctl disk resize %d %dB`", index, sizeBytes, err, index, sizeBytes)
	}
	return fmt.Errorf("macOS APFS expansion preflight failed for disk %d at %d bytes: %v; no host disk changes made; next action: fix the guest partition layout, then rerun `cove ctl disk resize %d %dB`", index, sizeBytes, err, index, sizeBytes)
}

func macOSResizeGuestFailedError(index int, sizeBytes uint64, err error) error {
	return fmt.Errorf("resized disk %d to %d bytes, but macOS APFS expansion failed: %v; host disk is already grown, so recovery only needs APFS expansion; next action: rerun `cove ctl disk resize %d %dB` after the root guest agent is ready, or run inside the guest as root: %s", index, sizeBytes, err, index, sizeBytes, macOSResizeAPFSManualCommand)
}

func runtimeDiskListSummary(disks []RuntimeDiskInfo) string {
	if len(disks) == 0 {
		return "no disks"
	}
	var total uint64
	var sized, readOnly int
	for _, disk := range disks {
		if disk.FileSizeBytes > 0 {
			total += disk.FileSizeBytes
			sized++
		}
		if disk.ReadOnly {
			readOnly++
		}
	}
	summary := fmt.Sprintf("%d disk", len(disks))
	if len(disks) != 1 {
		summary += "s"
	}
	if sized > 0 {
		summary += fmt.Sprintf(", %s backing files", runtimeDiskFormatBytes(total))
	}
	if readOnly > 0 {
		summary += fmt.Sprintf(", %d read-only", readOnly)
	}
	return summary
}

func runtimeDiskFormatBytes(bytes uint64) string {
	const maxFormatBytes = uint64(1<<63 - 1)
	if bytes <= maxFormatBytes {
		return bytefmt.Size(int64(bytes))
	}
	return fmt.Sprintf("%d bytes", bytes)
}

func (s *ControlServer) runtimeDiskEntries() ([]runtimeDiskEntry, error) {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return nil, fmt.Errorf("vm not configured")
	}

	var entries []runtimeDiskEntry

	DispatchSync(uintptr(s.vmQueue.Handle()), func() {
		machine := pvz.VZVirtualMachineFromID(s.vm.ID)
		if !objc.RespondsToSelector(machine.ID, objc.Sel("_storageDevices")) {
			entries = nil
			return
		}
		devices := objc.Send[[]objc.ID](machine.ID, objc.Sel("_storageDevices"))
		if devices == nil {
			entries = nil
			return
		}

		count := len(devices)
		entries = make([]runtimeDiskEntry, 0, count)
		for i, id := range devices {
			if id == 0 {
				continue
			}
			device := pvz.VZStorageDeviceFromID(id)
			entries = append(entries, runtimeDiskEntry{
				Index:  i,
				Device: device,
				Info:   runtimeDiskInfoForDevice(i, device),
			})
		}
	})

	for i := range entries {
		augmentRuntimeDiskEntryInfo(s.effectiveVMDir(), &entries[i])
	}
	return entries, nil
}

func augmentRuntimeDiskEntryInfo(vmDirectory string, entry *runtimeDiskEntry) {
	if entry == nil || entry.Info.Path != "" {
		return
	}
	path := runtimeDiskConfiguredPath(vmDirectory, *entry)
	if path == "" {
		return
	}
	entry.Info.Kind = "disk-image"
	runtimeDiskSetPathInfo(&entry.Info, path)
}

func (s *ControlServer) runtimeDiskEntryByIndex(index int) (runtimeDiskEntry, error) {
	entries, err := s.runtimeDiskEntries()
	if err != nil {
		return runtimeDiskEntry{}, err
	}
	for _, entry := range entries {
		if entry.Index == index {
			return entry, nil
		}
	}
	return runtimeDiskEntry{}, fmt.Errorf("disk %d not found", index)
}

func runtimeDiskInfoForDevice(index int, device pvz.VZStorageDevice) RuntimeDiskInfo {
	info := RuntimeDiskInfo{
		Index:       index,
		Kind:        "storage-device",
		Description: strings.TrimSpace(device.Description()),
	}

	attachment, err := device.Attachment()
	if err != nil {
		return info
	}
	if attachment == nil || attachment.GetID() == 0 {
		return info
	}

	if diskAttachment, ok := runtimeDiskDiskImageAttachmentFromObject(attachment); ok {
		info.Kind = "disk-image"
		publicAttachment := vz.VZDiskImageStorageDeviceAttachmentFromID(diskAttachment.ID)
		info.ReadOnly = publicAttachment.IsReadOnly()
		if url := publicAttachment.URL(); url.GetID() != 0 {
			if path := strings.TrimSpace(url.Path()); path != "" {
				runtimeDiskSetPathInfo(&info, path)
			}
		}
		return info
	}

	info.Description = strings.TrimSpace(device.Description())
	return info
}

func runtimeDiskDiskImageAttachmentFromObject(obj objectivec.IObject) (pvz.VZDiskImageStorageDeviceAttachment, bool) {
	if obj == nil || obj.GetID() == 0 {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	switch runtimeUSBClassName(obj) {
	case "VZDiskImageStorageDeviceAttachment", "_VZDiskImageStorageDeviceAttachment":
	default:
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	if !objc.RespondsToSelector(obj.GetID(), objc.Sel("URL")) {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	return pvz.VZDiskImageStorageDeviceAttachmentFromID(obj.GetID()), true
}

func runtimeDiskSetPathInfo(info *RuntimeDiskInfo, path string) {
	info.Path = path
	if stat, err := os.Stat(path); err == nil {
		info.FileSizeBytes = uint64(stat.Size())
		info.ModTime = stat.ModTime().UTC().Format(time.RFC3339)
	}
}

func runtimeDiskDiskImageAttachment(device pvz.VZStorageDevice) (pvz.VZDiskImageStorageDeviceAttachment, bool) {
	attachment, err := device.Attachment()
	if err != nil {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	return runtimeDiskDiskImageAttachmentFromObject(attachment)
}
