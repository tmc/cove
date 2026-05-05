package main

import (
	"encoding/json"
	"image"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

func TestHeadlessControllerStatusWithDetachedView(t *testing.T) {
	c := &vmGUIController{
		vmView: vz.VZVirtualMachineViewFromID(objc.ID(1)),
	}

	status := c.Status()
	if status.Headed {
		t.Fatal("Status().Headed = true, want false")
	}
	if status.WindowReady {
		t.Fatal("Status().WindowReady = true, want false")
	}
	if status.CaptureMode != "private-framebuffer" {
		t.Fatalf("Status().CaptureMode = %q, want private-framebuffer", status.CaptureMode)
	}
}

func TestHeadlessControllerBindingsUseDetachedViewWithoutWindow(t *testing.T) {
	view := vz.VZVirtualMachineViewFromID(objc.ID(1))
	bindings := &recordingGUIBindings{}
	c := &vmGUIController{
		vmView: view,
	}

	c.setControlBindings(bindings)

	if bindings.calls != 1 {
		t.Fatalf("SetVMViewWithWindow calls = %d, want 1", bindings.calls)
	}
	if bindings.view.ID != view.ID {
		t.Fatalf("bound view = %#x, want %#x", bindings.view.ID, view.ID)
	}
	if bindings.window.ID != 0 {
		t.Fatalf("bound window = %#x, want 0", bindings.window.ID)
	}
	if c.toolbar != nil {
		t.Fatal("toolbar created for detached headless view")
	}
}

func TestGUIStatusUsesInstalledController(t *testing.T) {
	s := &ControlServer{}
	s.SetGUIController(fakeGUIController{
		status: GUIStatus{
			Supported:   true,
			Headed:      true,
			WindowReady: true,
			CaptureMode: "window",
		},
	})

	resp := s.handleGUIRequest("gui-status")
	if !resp.Success {
		t.Fatalf("gui-status failed: %s", resp.Error)
	}
	var status GUIStatus
	if err := json.Unmarshal([]byte(resp.Data), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Supported || !status.Headed || !status.WindowReady {
		t.Fatalf("status = %+v, want supported headed ready", status)
	}
}

type fakeGUIController struct {
	status GUIStatus
}

func (f fakeGUIController) Open() error       { return nil }
func (f fakeGUIController) Close() error      { return nil }
func (f fakeGUIController) Status() GUIStatus { return f.status }
func (f fakeGUIController) Shutdown()         {}

type recordingGUIBindings struct {
	calls  int
	view   vz.VZVirtualMachineView
	window appkit.NSWindow
}

func (r *recordingGUIBindings) SetVMViewWithWindow(view vz.VZVirtualMachineView, window appkit.NSWindow) {
	r.calls++
	r.view = view
	r.window = window
}

func (r *recordingGUIBindings) captureDisplayImage() (image.Image, string) {
	return nil, ""
}
