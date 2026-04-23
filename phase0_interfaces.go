package main

import (
	"fmt"
	"image"
	"net"
	"path/filepath"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type vmScreenshotProvider interface {
	captureDisplayImage() (image.Image, string)
}

type vmWindowStateSink interface {
	SetVMViewWithWindow(vz.VZVirtualMachineView, appkit.NSWindow)
}

type vmGUIBindings interface {
	vmScreenshotProvider
	vmWindowStateSink
}

type guestPortConnector interface {
	ConnectToGuestPort(port uint32) (net.Conn, error)
}

type runtimeAgentAvailabilityTarget interface {
	currentVMState() (vz.VZVirtualMachineState, error)
	getAgent() (*AgentClient, error)
	effectiveVMDir() string
	vmHintFlag() string
}

type vmSelection struct {
	Directory string
	Name      string
}

type controlServerGuestConnector struct {
	vm    vz.VZVirtualMachine
	queue dispatch.Queue
}

type controlServerAgentAvailabilityTarget struct {
	server *ControlServer
	flag   string
}

func newControlServerGuestConnector(s *ControlServer) controlServerGuestConnector {
	if s == nil {
		return controlServerGuestConnector{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return controlServerGuestConnector{
		vm:    s.vm,
		queue: s.vmQueue,
	}
}

func newControlServerAgentAvailabilityTarget(s *ControlServer, vmName string) controlServerAgentAvailabilityTarget {
	return controlServerAgentAvailabilityTarget{
		server: s,
		flag:   vmHintFlag(vmName),
	}
}

func (c controlServerGuestConnector) ConnectToGuestPort(port uint32) (net.Conn, error) {
	if c.vm.ID == 0 {
		return nil, fmt.Errorf("vm not initialized")
	}
	if c.queue.Handle() == 0 {
		return nil, fmt.Errorf("vm queue not initialized")
	}
	mgr, err := NewVsockDeviceManager(c.vm, c.queue)
	if err != nil {
		return nil, err
	}
	return mgr.ConnectToAgent(port)
}

func (c controlServerAgentAvailabilityTarget) currentVMState() (vz.VZVirtualMachineState, error) {
	if c.server == nil {
		return 0, fmt.Errorf("agent availability target unavailable")
	}
	return c.server.currentVMState()
}

func (c controlServerAgentAvailabilityTarget) getAgent() (*AgentClient, error) {
	if c.server == nil {
		return nil, fmt.Errorf("agent availability target unavailable")
	}
	return c.server.getAgent()
}

func (c controlServerAgentAvailabilityTarget) effectiveVMDir() string {
	if c.server == nil {
		return ""
	}
	return c.server.effectiveVMDir()
}

func (c controlServerAgentAvailabilityTarget) vmHintFlag() string {
	return c.flag
}

func currentVMSelection() vmSelection {
	return vmSelection{
		Directory: vmDir,
		Name:      vmName,
	}
}

func (s vmSelection) controlSocketPath() string {
	return GetControlSocketPathForVM(s.Directory)
}

func (s vmSelection) diskPath() string {
	return filepath.Join(s.Directory, "disk.img")
}

func (s vmSelection) linuxDiskPath() string {
	return filepath.Join(s.Directory, "linux-disk.img")
}

func (s vmSelection) provisionStagingDir() string {
	return filepath.Join(s.Directory, ".provision")
}

func (s vmSelection) injectSucceededMarker() string {
	return filepath.Join(s.Directory, ".inject-succeeded")
}

func (s vmSelection) elevationLabel() string {
	if s.Name != "" {
		return s.Name
	}
	return "default"
}

func (s vmSelection) hintFlag() string {
	return vmHintFlag(s.Name)
}

func vmHintFlag(vmName string) string {
	if vmName == "" || vmName == "default" {
		return ""
	}
	return " -vm " + vmName
}

type setupAssistantTransport interface {
	WaitForConnection(timeout time.Duration) error
	Screenshot() (image.Image, error)
	ScreenshotScaled(scale float64) (image.Image, error)
	MouseClick(x, y float64) error
	KeyPress(keyCode uint16) error
	KeyPressWithModifiers(keyCode uint16, modifiers uint) error
	TypeText(text string) error
	OCRClickText(ocr *OCRService, text string, timeout time.Duration) error
	OCRClickTextWithOptions(ocr *OCRService, text string, timeout time.Duration, opts OCRSearchOptions) error
	OCRDetectPage(ocr *OCRService) string
	OCRWaitForPageChange(ocr *OCRService, currentPage string, timeout time.Duration) error
	InputBackendName() string
}

type setupAssistantServer interface {
	captureDisplayImage() (image.Image, string)
	sendMouseEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse
	sendKeyEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse
	typeText(cmd *controlpb.TextCommand) *controlpb.ControlResponse
	OCRClickText(ocr *OCRService, text string, timeout time.Duration) error
	OCRClickTextWithOptions(ocr *OCRService, text string, timeout time.Duration, opts OCRSearchOptions) error
	OCRDetectPage(ocr *OCRService) string
	OCRWaitForPageChange(ocr *OCRService, currentPage string, timeout time.Duration) error
	inputBackend() automationBackendMode
}

type inProcessSetupAssistantTransport struct {
	server setupAssistantServer
}

func (t inProcessSetupAssistantTransport) WaitForConnection(timeout time.Duration) error {
	return nil
}

func (t inProcessSetupAssistantTransport) Screenshot() (image.Image, error) {
	if t.server == nil {
		return nil, fmt.Errorf("setup assistant transport unavailable")
	}
	img, errMsg := t.server.captureDisplayImage()
	if errMsg != "" {
		return nil, fmt.Errorf("%s", errMsg)
	}
	return img, nil
}

func (t inProcessSetupAssistantTransport) ScreenshotScaled(scale float64) (image.Image, error) {
	img, err := t.Screenshot()
	if err != nil {
		return nil, err
	}
	if scale < 1 {
		return scaleImage(img, scale), nil
	}
	return img, nil
}

func (t inProcessSetupAssistantTransport) MouseClick(x, y float64) error {
	if t.server == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	resp := t.server.sendMouseEvent(&controlpb.MouseCommand{
		X:      x,
		Y:      y,
		Button: 0,
		Action: "click",
	})
	if resp.Success {
		return nil
	}
	return fmt.Errorf("mouse click failed: %s", resp.Error)
}

func (t inProcessSetupAssistantTransport) KeyPress(keyCode uint16) error {
	if t.server == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	down := t.server.sendKeyEvent(&controlpb.KeyCommand{KeyCode: uint32(keyCode), KeyDown: true})
	if !down.Success {
		return fmt.Errorf("key down failed: %s", down.Error)
	}
	time.Sleep(50 * time.Millisecond)
	up := t.server.sendKeyEvent(&controlpb.KeyCommand{KeyCode: uint32(keyCode), KeyDown: false})
	if !up.Success {
		return fmt.Errorf("key up failed: %s", up.Error)
	}
	return nil
}

func (t inProcessSetupAssistantTransport) KeyPressWithModifiers(keyCode uint16, modifiers uint) error {
	if t.server == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	down := t.server.sendKeyEvent(&controlpb.KeyCommand{
		KeyCode:   uint32(keyCode),
		KeyDown:   true,
		Modifiers: uint32(modifiers),
	})
	if !down.Success {
		return fmt.Errorf("modified key down failed: %s", down.Error)
	}
	time.Sleep(50 * time.Millisecond)
	up := t.server.sendKeyEvent(&controlpb.KeyCommand{
		KeyCode:   uint32(keyCode),
		KeyDown:   false,
		Modifiers: uint32(modifiers),
	})
	if !up.Success {
		return fmt.Errorf("modified key up failed: %s", up.Error)
	}
	return nil
}

func (t inProcessSetupAssistantTransport) TypeText(text string) error {
	if t.server == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	resp := t.server.typeText(&controlpb.TextCommand{Text: text})
	if resp.Success {
		return nil
	}
	return fmt.Errorf("type text failed: %s", resp.Error)
}

func (t inProcessSetupAssistantTransport) OCRClickText(ocr *OCRService, text string, timeout time.Duration) error {
	if t.server == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.server.OCRClickText(ocr, text, timeout)
}

func (t inProcessSetupAssistantTransport) OCRClickTextWithOptions(ocr *OCRService, text string, timeout time.Duration, opts OCRSearchOptions) error {
	if t.server == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.server.OCRClickTextWithOptions(ocr, text, timeout, opts)
}

func (t inProcessSetupAssistantTransport) OCRDetectPage(ocr *OCRService) string {
	if t.server == nil {
		return "unknown"
	}
	return t.server.OCRDetectPage(ocr)
}

func (t inProcessSetupAssistantTransport) OCRWaitForPageChange(ocr *OCRService, currentPage string, timeout time.Duration) error {
	if t.server == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.server.OCRWaitForPageChange(ocr, currentPage, timeout)
}

func (t inProcessSetupAssistantTransport) InputBackendName() string {
	if t.server == nil {
		return "unknown"
	}
	return t.server.inputBackend().inputString()
}

type socketSetupAssistantTransport struct {
	client *ControlClient
}

func (t socketSetupAssistantTransport) WaitForConnection(timeout time.Duration) error {
	if t.client == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.client.WaitForConnection(timeout)
}

func (t socketSetupAssistantTransport) Screenshot() (image.Image, error) {
	if t.client == nil {
		return nil, fmt.Errorf("setup assistant transport unavailable")
	}
	return t.client.Screenshot()
}

func (t socketSetupAssistantTransport) ScreenshotScaled(scale float64) (image.Image, error) {
	if t.client == nil {
		return nil, fmt.Errorf("setup assistant transport unavailable")
	}
	return t.client.ScreenshotScaled(scale)
}

func (t socketSetupAssistantTransport) MouseClick(x, y float64) error {
	if t.client == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.client.MouseClick(x, y)
}

func (t socketSetupAssistantTransport) KeyPress(keyCode uint16) error {
	if t.client == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.client.KeyPress(keyCode)
}

func (t socketSetupAssistantTransport) KeyPressWithModifiers(keyCode uint16, modifiers uint) error {
	if t.client == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.client.KeyPressWithModifiers(keyCode, modifiers)
}

func (t socketSetupAssistantTransport) TypeText(text string) error {
	if t.client == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	return t.client.TypeText(text)
}

func (t socketSetupAssistantTransport) OCRClickText(ocr *OCRService, text string, timeout time.Duration) error {
	return t.OCRClickTextWithOptions(ocr, text, timeout, OCRSearchOptions{})
}

func (t socketSetupAssistantTransport) OCRClickTextWithOptions(ocr *OCRService, text string, timeout time.Duration, opts OCRSearchOptions) error {
	if t.client == nil || ocr == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, err := t.client.Screenshot()
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		normX, normY, found := ocr.FindTextNormalizedWithOptions(img, text, opts)
		if found {
			return t.client.MouseClick(normX, normY)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout: text %q not found", text)
}

func (t socketSetupAssistantTransport) OCRDetectPage(ocr *OCRService) string {
	if t.client == nil || ocr == nil {
		return "unknown"
	}
	img, err := t.client.Screenshot()
	if err != nil {
		return "unknown"
	}
	return OCRDetectSetupAssistantPage(img, ocr)
}

func (t socketSetupAssistantTransport) OCRWaitForPageChange(ocr *OCRService, currentPage string, timeout time.Duration) error {
	if t.client == nil || ocr == nil {
		return fmt.Errorf("setup assistant transport unavailable")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, err := t.client.Screenshot()
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if OCRDetectSetupAssistantPage(img, ocr) != currentPage {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("stuck on page %s", currentPage)
}

func (t socketSetupAssistantTransport) InputBackendName() string {
	return "socket"
}
