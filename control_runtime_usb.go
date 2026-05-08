package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/storagehotplug"
	"github.com/tmc/apple/x/vzkit/usbpassthrough"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const runtimeUSBOperationTimeout = 10 * time.Second

type runtimeUSBAction string

const (
	runtimeUSBActionList              runtimeUSBAction = "list"
	runtimeUSBActionAttachMassStorage runtimeUSBAction = "attach-mass-storage"
	runtimeUSBActionAttachPassthrough runtimeUSBAction = "attach-passthrough"
	runtimeUSBActionDetach            runtimeUSBAction = "detach"
)

type runtimeUSBRequest struct {
	Action          string `json:"action"`
	ControllerIndex int    `json:"controller_index,omitempty"`
	DeviceIndex     *int   `json:"device_index,omitempty"`
	Path            string `json:"path,omitempty"`
	ReadOnly        bool   `json:"read_only,omitempty"`
	ServiceID       uint32 `json:"service_id,omitempty"`
	LocationID      uint32 `json:"location_id,omitempty"`
}

type runtimeUSBJSONEnvelope struct {
	Data json.RawMessage `json:"data,omitempty"`
}

type runtimeUSBDeviceInfo struct {
	Index           *int   `json:"index,omitempty"`
	Kind            string `json:"kind,omitempty"`
	UUID            string `json:"uuid,omitempty"`
	ControllerIndex int    `json:"controller_index,omitempty"`
	Description     string `json:"description,omitempty"`
	Path            string `json:"path,omitempty"`
	ReadOnly        bool   `json:"read_only,omitempty"`
	ServiceID       uint32 `json:"service_id,omitempty"`
	LocationID      uint32 `json:"location_id,omitempty"`
}

type runtimeUSBControllerInfo struct {
	Index       int                    `json:"index"`
	Kind        string                 `json:"kind,omitempty"`
	Description string                 `json:"description,omitempty"`
	DeviceCount int                    `json:"device_count"`
	Devices     []runtimeUSBDeviceInfo `json:"devices,omitempty"`
}

type runtimeUSBListResponse struct {
	Controllers []runtimeUSBControllerInfo `json:"controllers"`
}

type runtimeUSBResponse struct {
	OK      bool                    `json:"ok"`
	Action  string                  `json:"action,omitempty"`
	Message string                  `json:"message,omitempty"`
	Error   string                  `json:"error,omitempty"`
	List    *runtimeUSBListResponse `json:"list,omitempty"`
	Device  *runtimeUSBDeviceInfo   `json:"device,omitempty"`
}

func parseRuntimeUSBRequest(rawJSON []byte) (runtimeUSBRequest, error) {
	var env runtimeUSBJSONEnvelope
	if err := json.Unmarshal(rawJSON, &env); err == nil && len(env.Data) > 0 {
		rawJSON = env.Data
	}
	var req runtimeUSBRequest
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		return runtimeUSBRequest{}, fmt.Errorf("parse runtime usb request: %w", err)
	}
	req.Action = normalizeRuntimeUSBAction(req.Action)
	if req.Action == "" {
		return runtimeUSBRequest{}, fmt.Errorf("action required")
	}
	return req, nil
}

func normalizeRuntimeUSBAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "list", "status", "ls":
		return string(runtimeUSBActionList)
	case "attach-mass-storage", "attach-storage", "attach-usb-storage":
		return string(runtimeUSBActionAttachMassStorage)
	case "attach-passthrough", "attach-host-passthrough", "attach-host-usb":
		return string(runtimeUSBActionAttachPassthrough)
	case "detach", "detach-device", "remove":
		return string(runtimeUSBActionDetach)
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

func (req runtimeUSBRequest) validate() error {
	switch runtimeUSBAction(req.Action) {
	case runtimeUSBActionList:
		return nil
	case runtimeUSBActionAttachMassStorage:
		if req.ControllerIndex < 0 {
			return fmt.Errorf("controller_index must be >= 0")
		}
		if strings.TrimSpace(req.Path) == "" {
			return fmt.Errorf("path required for attach-mass-storage")
		}
		return nil
	case runtimeUSBActionAttachPassthrough:
		if req.ControllerIndex < 0 {
			return fmt.Errorf("controller_index must be >= 0")
		}
		if req.ServiceID == 0 && req.LocationID == 0 {
			return fmt.Errorf("service_id or location_id required for attach-passthrough")
		}
		if req.ServiceID != 0 && req.LocationID != 0 {
			return fmt.Errorf("service_id and location_id are mutually exclusive")
		}
		return nil
	case runtimeUSBActionDetach:
		if req.DeviceIndex == nil {
			return fmt.Errorf("device_index required for detach")
		}
		if *req.DeviceIndex < 0 {
			return fmt.Errorf("device_index must be >= 0")
		}
		if req.ControllerIndex < 0 {
			return fmt.Errorf("controller_index must be >= 0")
		}
		return nil
	default:
		return fmt.Errorf("unknown runtime usb action: %s", req.Action)
	}
}

func runtimeUSBControlResponse(resp runtimeUSBResponse) *controlpb.ControlResponse {
	data, err := json.Marshal(resp)
	if err != nil {
		return &controlpb.ControlResponse{
			Success: false,
			Error:   fmt.Sprintf("marshal runtime usb response: %v", err),
		}
	}
	return &controlpb.ControlResponse{
		Success: resp.OK,
		Error:   resp.Error,
		Data:    string(data),
	}
}

func runtimeUSBErrorResponse(action string, err error) *controlpb.ControlResponse {
	return runtimeUSBControlResponse(runtimeUSBResponse{
		OK:      false,
		Action:  action,
		Error:   err.Error(),
		Message: err.Error(),
	})
}

func (s *ControlServer) handleRuntimeUSBJSONRequest(rawJSON []byte) *controlpb.ControlResponse {
	req, err := parseRuntimeUSBRequest(rawJSON)
	if err != nil {
		return runtimeUSBErrorResponse("", err)
	}
	if err := req.validate(); err != nil {
		return runtimeUSBErrorResponse(req.Action, err)
	}

	var resp runtimeUSBResponse
	switch runtimeUSBAction(req.Action) {
	case runtimeUSBActionList:
		list, err := s.runtimeUSBList()
		if err != nil {
			return runtimeUSBErrorResponse(req.Action, err)
		}
		resp = runtimeUSBResponse{
			OK:     true,
			Action: req.Action,
			List:   &list,
		}
	case runtimeUSBActionAttachMassStorage:
		dev, err := s.runtimeUSBAttachMassStorage(req)
		if err != nil {
			return runtimeUSBErrorResponse(req.Action, err)
		}
		resp = runtimeUSBResponse{
			OK:      true,
			Action:  req.Action,
			Message: "runtime usb mass storage attached",
			Device:  &dev,
		}
	case runtimeUSBActionAttachPassthrough:
		dev, err := s.runtimeUSBAttachPassthrough(req)
		if err != nil {
			return runtimeUSBErrorResponse(req.Action, err)
		}
		resp = runtimeUSBResponse{
			OK:      true,
			Action:  req.Action,
			Message: "runtime usb passthrough attached",
			Device:  &dev,
		}
	case runtimeUSBActionDetach:
		dev, err := s.runtimeUSBDetach(req)
		if err != nil {
			return runtimeUSBErrorResponse(req.Action, err)
		}
		resp = runtimeUSBResponse{
			OK:      true,
			Action:  req.Action,
			Message: "runtime usb device detached",
			Device:  &dev,
		}
	default:
		return runtimeUSBErrorResponse(req.Action, fmt.Errorf("unknown runtime usb action: %s", req.Action))
	}
	return runtimeUSBControlResponse(resp)
}

func (s *ControlServer) runtimeUSBList() (runtimeUSBListResponse, error) {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return runtimeUSBListResponse{}, fmt.Errorf("vm not configured")
	}
	var (
		list runtimeUSBListResponse
	)
	done := make(chan struct{})
	DispatchAsyncQueue(s.vmQueue, func() {
		defer close(done)
		controllers := s.vm.UsbControllers()
		list.Controllers = make([]runtimeUSBControllerInfo, 0, len(controllers))
		for i, controller := range controllers {
			info := runtimeUSBControllerInfo{
				Index:       i,
				Kind:        runtimeUSBClassName(controller),
				Description: runtimeUSBDescription(controller),
			}
			devices := controller.UsbDevices()
			info.DeviceCount = len(devices)
			info.Devices = make([]runtimeUSBDeviceInfo, 0, len(devices))
			for j, device := range devices {
				info.Devices = append(info.Devices, runtimeUSBDescribeDevice(i, j, device))
			}
			list.Controllers = append(list.Controllers, info)
		}
	})
	select {
	case <-done:
		return list, nil
	case <-time.After(runtimeUSBOperationTimeout):
		return runtimeUSBListResponse{}, fmt.Errorf("timed out reading usb controllers")
	}
}

func (s *ControlServer) runtimeUSBAttachMassStorage(req runtimeUSBRequest) (runtimeUSBDeviceInfo, error) {
	controller, err := s.runtimeUSBControllerAtIndex(req.ControllerIndex)
	if err != nil {
		return runtimeUSBDeviceInfo{}, err
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("path required for attach-mass-storage")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("resolve path: %w", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("stat path: %w", err)
	}
	attachment, err := storagehotplug.NewDiskImageAttachment(absPath, req.ReadOnly)
	if err != nil {
		return runtimeUSBDeviceInfo{}, err
	}
	attachment.Retain()

	cfg := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if cfg.ID == 0 {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("create usb mass storage configuration")
	}
	cfg.Retain()
	pcfg := pvz.VZUSBMassStorageDeviceConfigurationFromID(cfg.GetID())

	return s.runtimeUSBCreateAndAttachDevice(req.ControllerIndex, controller, func() (objectivec.IObject, error) {
		dev := pcfg.MakeUSBDeviceWithVirtualMachine(s.vm)
		if dev == nil || dev.GetID() == 0 {
			return nil, fmt.Errorf("create usb mass storage device")
		}
		retainObjectiveCObject(dev)
		return dev, nil
	})
}

func (s *ControlServer) runtimeUSBAttachPassthrough(req runtimeUSBRequest) (runtimeUSBDeviceInfo, error) {
	controller, err := s.runtimeUSBControllerAtIndex(req.ControllerIndex)
	if err != nil {
		return runtimeUSBDeviceInfo{}, err
	}

	return s.runtimeUSBCreateAndAttachDevice(req.ControllerIndex, controller, func() (objectivec.IObject, error) {
		var cfg pvz.VZIOUSBHostPassthroughDeviceConfiguration
		switch {
		case req.LocationID != 0:
			cfg, err = usbpassthrough.NewHostPassthroughConfigurationFromLocationID(req.LocationID)
		case req.ServiceID != 0:
			cfg, err = usbpassthrough.NewHostPassthroughConfigurationFromService(req.ServiceID)
		default:
			err = fmt.Errorf("service_id or location_id required for attach-passthrough")
		}
		if err != nil {
			return nil, err
		}
		cfg.Retain()
		dev := cfg.MakeUSBDeviceWithVirtualMachine(s.vm)
		if dev == nil || dev.GetID() == 0 {
			return nil, fmt.Errorf("create usb passthrough device")
		}
		retainObjectiveCObject(dev)
		return dev, nil
	})
}

func (s *ControlServer) runtimeUSBDetach(req runtimeUSBRequest) (runtimeUSBDeviceInfo, error) {
	if req.DeviceIndex == nil {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("device_index required for detach")
	}
	controller, err := s.runtimeUSBControllerAtIndex(req.ControllerIndex)
	if err != nil {
		return runtimeUSBDeviceInfo{}, err
	}
	devices := controller.UsbDevices()
	if *req.DeviceIndex < 0 || *req.DeviceIndex >= len(devices) {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("usb device %d not found on controller %d", *req.DeviceIndex, req.ControllerIndex)
	}
	device := vz.VZUSBDeviceObjectFromID(devices[*req.DeviceIndex].GetID())
	done := make(chan error, 1)
	DispatchAsyncQueue(s.vmQueue, func() {
		controller.DetachDeviceCompletionHandler(device, func(err error) {
			done <- err
		})
	})
	select {
	case err := <-done:
		if err != nil {
			return runtimeUSBDeviceInfo{}, fmt.Errorf("detach usb device: %w", err)
		}
	case <-time.After(runtimeUSBOperationTimeout):
		return runtimeUSBDeviceInfo{}, fmt.Errorf("detach usb device timed out")
	}
	info := runtimeUSBDescribeDevice(req.ControllerIndex, *req.DeviceIndex, devices[*req.DeviceIndex])
	releaseRuntimeUSBDevice(devices[*req.DeviceIndex])
	return info, nil
}

func (s *ControlServer) runtimeUSBCreateAndAttachDevice(controllerIndex int, controller vz.VZUSBController, build func() (objectivec.IObject, error)) (runtimeUSBDeviceInfo, error) {
	if controller.ID == 0 {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("usb controller %d not found", controllerIndex)
	}
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return runtimeUSBDeviceInfo{}, fmt.Errorf("vm not configured")
	}

	type attachResult struct {
		device objectivec.IObject
		err    error
	}
	done := make(chan attachResult, 1)
	DispatchAsyncQueue(s.vmQueue, func() {
		deviceObj, err := build()
		if err != nil {
			done <- attachResult{err: err}
			return
		}
		device := vz.VZUSBDeviceObjectFromID(deviceObj.GetID())
		controller.AttachDeviceCompletionHandler(device, func(err error) {
			done <- attachResult{device: deviceObj, err: err}
		})
	})

	select {
	case result := <-done:
		if result.err != nil {
			return runtimeUSBDeviceInfo{}, fmt.Errorf("attach usb device: %w", result.err)
		}
		return runtimeUSBDescribeDevice(controllerIndex, -1, result.device), nil
	case <-time.After(runtimeUSBOperationTimeout):
		return runtimeUSBDeviceInfo{}, fmt.Errorf("attach usb device timed out")
	}
}

func (s *ControlServer) runtimeUSBControllersSnapshot() ([]vz.VZUSBController, error) {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return nil, fmt.Errorf("vm not configured")
	}
	controllers := make([]vz.VZUSBController, 0)
	done := make(chan struct{})
	DispatchAsyncQueue(s.vmQueue, func() {
		defer close(done)
		controllers = s.vm.UsbControllers()
	})
	select {
	case <-done:
		return controllers, nil
	case <-time.After(runtimeUSBOperationTimeout):
		return nil, fmt.Errorf("timed out reading usb controllers")
	}
}

func (s *ControlServer) runtimeUSBControllerAtIndex(index int) (vz.VZUSBController, error) {
	controllers, err := s.runtimeUSBControllersSnapshot()
	if err != nil {
		return vz.VZUSBController{}, err
	}
	if index < 0 || index >= len(controllers) {
		return vz.VZUSBController{}, fmt.Errorf("usb controller %d not found", index)
	}
	return controllers[index], nil
}

func runtimeUSBDescribeDevice(controllerIndex, deviceIndex int, device objectivec.IObject) runtimeUSBDeviceInfo {
	info := runtimeUSBDeviceInfo{
		ControllerIndex: controllerIndex,
		Kind:            runtimeUSBClassName(device),
		Description:     runtimeUSBDescription(device),
	}
	if deviceIndex >= 0 {
		idx := deviceIndex
		info.Index = &idx
	}
	if device == nil || device.GetID() == 0 {
		return info
	}
	if uuid := runtimeUSBUUIDString(device); uuid != "" {
		info.UUID = uuid
	}
	switch runtimeUSBClassName(device) {
	case "VZUSBMassStorageDevice", "_VZUSBMassStorageDevice":
		info.Path, info.ReadOnly = runtimeUSBMassStorageDetails(device)
	case "VZIOUSBHostPassthroughDevice", "_VZIOUSBHostPassthroughDevice":
		// no stable field beyond the class name and description
	}
	return info
}

func runtimeUSBMassStorageDetails(id objectivec.IObject) (string, bool) {
	if id == nil || id.GetID() == 0 {
		return "", false
	}
	dev := pvz.VZUSBMassStorageDeviceFromID(objc.ID(id.GetID()))
	conf := dev.Configuration()
	if conf == nil || conf.GetID() == 0 {
		return "", false
	}
	cfg := vz.VZStorageDeviceConfigurationFromID(objc.ID(conf.GetID()))
	attachment := cfg.Attachment()
	if attachment == nil || attachment.GetID() == 0 {
		return "", false
	}
	className := runtimeUSBClassName(attachment)
	if className != "VZDiskImageStorageDeviceAttachment" && className != "_VZDiskImageStorageDeviceAttachment" {
		return "", false
	}
	disk := vz.VZDiskImageStorageDeviceAttachmentFromID(attachment.GetID())
	path := ""
	if url := disk.URL(); url.GetID() != 0 {
		path = url.Path()
	}
	return path, disk.ReadOnly()
}

func runtimeUSBClassName(obj objectivec.IObject) string {
	if obj == nil || obj.GetID() == 0 {
		return ""
	}
	classID := objc.Send[objc.ID](obj.GetID(), objc.Sel("class"))
	if classID == 0 {
		return ""
	}
	nameID := objc.Send[objc.ID](classID, objc.Sel("className"))
	if nameID == 0 {
		return ""
	}
	return strings.TrimSpace(foundation.NSStringFromID(nameID).String())
}

func runtimeUSBDescription(obj objectivec.IObject) string {
	if obj == nil || obj.GetID() == 0 {
		return ""
	}
	return objectivec.ObjectFromID(obj.GetID()).Description()
}

func runtimeUSBUUIDString(obj objectivec.IObject) string {
	if obj == nil || obj.GetID() == 0 {
		return ""
	}
	uuid := vz.VZUSBDeviceObjectFromID(obj.GetID()).Uuid()
	if uuid.GetID() == 0 {
		return ""
	}
	return uuid.UUIDString()
}

func retainObjectiveCObject(obj objectivec.IObject) {
	if obj == nil || obj.GetID() == 0 {
		return
	}
	objc.Send[objc.ID](obj.GetID(), objc.Sel("retain"))
}

func releaseRuntimeUSBDevice(obj objectivec.IObject) {
	if obj == nil || obj.GetID() == 0 {
		return
	}
	className := runtimeUSBClassName(obj)
	switch className {
	case "VZIOUSBHostPassthroughDevice", "_VZIOUSBHostPassthroughDevice":
		pvz.VZIOUSBHostPassthroughDeviceFromID(obj.GetID()).ReleaseDevice()
	}
}
