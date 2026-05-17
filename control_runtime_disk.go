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
	"github.com/tmc/apple/x/vzkit/storagehotplug"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	agentpb "github.com/tmc/vz-macos/proto/agentpb"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const macOSResizeAPFSTimeout = 5 * time.Minute

const macOSResizeAPFSManualCommand = `/usr/sbin/diskutil apfs resizeContainer "$(/usr/sbin/diskutil info / | /usr/bin/awk -F: '/APFS Container/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')" 0`

const macOSResizeAPFSScript = `set -eu
PATH=/usr/sbin:/usr/bin:/bin:/sbin
container=$(/usr/sbin/diskutil info / | /usr/bin/awk -F: '/APFS Container/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')
if [ -z "$container" ]; then
	echo "could not find APFS container for /" >&2
	/usr/sbin/diskutil info / >&2 || true
	exit 64
fi
before=$(/usr/sbin/diskutil info / | /usr/bin/awk -F'[()]' '/Container Total Space:/ {print $2; exit}' || true)
/usr/sbin/diskutil apfs resizeContainer "$container" 0
after=$(/usr/sbin/diskutil info / | /usr/bin/awk -F'[()]' '/Container Total Space:/ {print $2; exit}' || true)
printf 'expanded APFS container %s\n' "$container"
if [ -n "$before" ]; then
	printf 'before: %s\n' "$before"
fi
if [ -n "$after" ]; then
	printf 'after: %s\n' "$after"
fi`

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
	Action string            `json:"action"`
	Count  int               `json:"count"`
	Disks  []RuntimeDiskInfo `json:"disks"`
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
	Platform  string `json:"platform"`
	Attempted bool   `json:"attempted"`
	Expanded  bool   `json:"expanded"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
}

type runtimeDiskEntry struct {
	Index  int
	Device pvz.VZStorageDevice
	Info   RuntimeDiskInfo
}

type runtimeDiskGuestAgent interface {
	Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*agentpb.ExecResponse, error)
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
		Action: "list",
		Count:  len(disks),
		Disks:  disks,
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

	attachment, ok := runtimeDiskDiskImageAttachment(entry.Device)
	if !ok {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("disk %d is not backed by a disk image", entry.Index)}
	}

	var macAgent runtimeDiskGuestAgent
	expandMacOSAPFS := runtimeDiskShouldExpandMacOSRootAPFS(s.effectiveVMDir(), entry)
	if expandMacOSAPFS {
		a, err := s.getAgent()
		if err != nil {
			return &controlpb.ControlResponse{Error: macOSResizeAgentUnavailableError(entry.Index, sizeBytes, err).Error()}
		}
		macAgent = a
	}

	if err := storagehotplug.UpdateDiskSize(attachment, sizeBytes); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("resize disk attachment: %v", err)}
	}

	updated, err := s.runtimeDiskEntryByIndex(entry.Index)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("resized disk %d to %d bytes, but could not inspect updated disk: %v; next action: run `cove ctl disk list` and retry `cove ctl disk resize %d %dB` if the guest filesystem did not grow", entry.Index, sizeBytes, err, entry.Index, sizeBytes)}
	}

	var guestResize *RuntimeDiskGuestResize
	if expandMacOSAPFS {
		guestResize, err = s.expandMacOSRootAPFS(macAgent)
		if err != nil {
			return &controlpb.ControlResponse{Error: macOSResizeGuestFailedError(entry.Index, sizeBytes, err).Error()}
		}
	}

	msg := fmt.Sprintf("resized disk %d to %d bytes", entry.Index, sizeBytes)
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
	return entry.Index == 0 && agentstate.Platform(vmDirectory) == agentstate.PlatformMacOS
}

func (s *ControlServer) expandMacOSRootAPFS(agent runtimeDiskGuestAgent) (*RuntimeDiskGuestResize, error) {
	if agent == nil {
		return nil, fmt.Errorf("guest agent unavailable")
	}
	ctx, cancel := s.timeoutContext(macOSResizeAPFSTimeout)
	defer cancel()

	res, err := agent.Exec(ctx, []string{"/bin/sh", "-c", macOSResizeAPFSScript}, nil, "")
	if err != nil {
		return nil, fmt.Errorf("run diskutil: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("run diskutil: missing response")
	}

	guestResize := &RuntimeDiskGuestResize{
		Platform:  agentstate.PlatformMacOS,
		Attempted: true,
		Stdout:    strings.TrimSpace(responseText(res.GetStdout())),
		Stderr:    strings.TrimSpace(responseText(res.GetStderr())),
	}
	if res.GetExitCode() != 0 {
		detail := pickReadyDetail(guestResize.Stdout, guestResize.Stderr, int(res.GetExitCode()))
		return guestResize, fmt.Errorf("diskutil exit %d: %s", res.GetExitCode(), detail)
	}
	guestResize.Expanded = true
	return guestResize, nil
}

func macOSResizeAgentUnavailableError(index int, sizeBytes uint64, err error) error {
	return fmt.Errorf("macOS disk resize requires the guest agent so cove can expand APFS after growing the backing image; no changes made; next action: start the VM, wait for `cove ctl agent-ping` to succeed, then rerun `cove ctl disk resize %d %dB`: %w", index, sizeBytes, err)
}

func macOSResizeGuestFailedError(index int, sizeBytes uint64, err error) error {
	return fmt.Errorf("resized disk %d to %d bytes, but macOS APFS expansion failed: %v; next action: rerun `cove ctl disk resize %d %dB` after the guest agent is ready, or run inside the guest as root: %s", index, sizeBytes, err, index, sizeBytes, macOSResizeAPFSManualCommand)
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

	return entries, nil
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
		info.ReadOnly = diskAttachment.ReadOnly()
		if objc.RespondsToSelector(diskAttachment.ID, objc.Sel("URL")) {
			urlID := objc.Send[objc.ID](diskAttachment.ID, objc.Sel("URL"))
			if path := strings.TrimSpace(foundation.NSURLFromID(urlID).Path()); path != "" {
				info.Path = path
				if stat, err := os.Stat(path); err == nil {
					info.FileSizeBytes = uint64(stat.Size())
					info.ModTime = stat.ModTime().UTC().Format(time.RFC3339)
				}
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
	if !objc.RespondsToSelector(obj.GetID(), objc.Sel("readOnly")) {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	if !objc.RespondsToSelector(obj.GetID(), objc.Sel("URL")) {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	return pvz.VZDiskImageStorageDeviceAttachmentFromID(obj.GetID()), true
}

func runtimeDiskDiskImageAttachment(device pvz.VZStorageDevice) (pvz.VZDiskImageStorageDeviceAttachment, bool) {
	attachment, err := device.Attachment()
	if err != nil {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	return runtimeDiskDiskImageAttachmentFromObject(attachment)
}
