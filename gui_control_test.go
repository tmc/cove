package main

import (
	"encoding/json"
	"image"
	"sync/atomic"
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

func TestConfigureProcessIdentityDoesNotSetDockBadge(t *testing.T) {
	orig := setDockBadgeLabel
	defer func() { setDockBadgeLabel = orig }()

	setDockBadgeLabel = func(app appkit.NSApplication, label string) {
		t.Fatalf("setDockBadgeLabel called with %q", label)
	}

	c := &vmGUIController{target: vmSelection{Name: "work"}}
	c.configureProcessIdentity()
}

func TestSetDockBadgeOnMainUsesNamedVM(t *testing.T) {
	orig := setDockBadgeLabel
	defer func() { setDockBadgeLabel = orig }()

	var got string
	setDockBadgeLabel = func(app appkit.NSApplication, label string) {
		got = label
	}

	c := &vmGUIController{target: vmSelection{Name: "work"}}
	c.setDockBadgeOnMain()
	if got != "work" {
		t.Fatalf("dock badge = %q, want work", got)
	}
}

func TestSetDockBadgeOnMainSkipsDefaultVM(t *testing.T) {
	tests := []struct {
		name string
		vm   string
	}{
		{name: "empty", vm: ""},
		{name: "default", vm: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := setDockBadgeLabel
			defer func() { setDockBadgeLabel = orig }()

			setDockBadgeLabel = func(app appkit.NSApplication, label string) {
				t.Fatalf("setDockBadgeLabel called with %q", label)
			}

			c := &vmGUIController{target: vmSelection{Name: tt.vm}}
			c.setDockBadgeOnMain()
		})
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

func TestGUIDelegateHideRunsUnderLock(t *testing.T) {
	tests := []struct {
		name string
		run  func(*vmGUIController, func() error) bool
	}{
		{
			name: "window close",
			run:  func(c *vmGUIController, hide func() error) bool { return c.windowShouldCloseOnMainWithHide(hide) },
		},
		{
			name: "app terminate",
			run:  func(c *vmGUIController, hide func() error) bool { return c.appShouldTerminateOnMainWithHide(hide) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &vmGUIController{}
			var called atomic.Bool
			allow := tt.run(c, func() error {
				called.Store(true)
				if c.mu.TryLock() {
					c.mu.Unlock()
					t.Fatal("hide callback ran without c.mu held")
				}
				return nil
			})
			if allow {
				t.Fatal("delegate allowed close/terminate while not shutting down")
			}
			if !called.Load() {
				t.Fatal("hide callback was not called")
			}
		})
	}
}

func TestGUIDelegateAllowsShutdownWithoutHide(t *testing.T) {
	tests := []struct {
		name string
		run  func(*vmGUIController, func() error) bool
	}{
		{
			name: "window close",
			run:  func(c *vmGUIController, hide func() error) bool { return c.windowShouldCloseOnMainWithHide(hide) },
		},
		{
			name: "app terminate",
			run:  func(c *vmGUIController, hide func() error) bool { return c.appShouldTerminateOnMainWithHide(hide) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &vmGUIController{shuttingDown: true}
			allow := tt.run(c, func() error {
				t.Fatal("hide callback called during shutdown")
				return nil
			})
			if !allow {
				t.Fatal("delegate refused close/terminate during shutdown")
			}
		})
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
