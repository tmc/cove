package main

import (
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

func TestToolbarStateTransitions(t *testing.T) {
	var captured bool
	class, err := objc.RegisterClass("CoveToolbarCaptureTestView", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("setCapturesSystemKeys:"), Fn: func(_ objc.ID, _ objc.SEL, value bool) { captured = value }},
		{Cmd: objc.RegisterName("capturesSystemKeys"), Fn: func(_ objc.ID, _ objc.SEL) bool { return captured }},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(class), objc.Sel("alloc")), objc.Sel("init"))
	view := vz.VZVirtualMachineViewFromID(id)
	tb := &VMToolbar{vmView: view, items: make(map[string]appkit.NSToolbarItem)}
	for _, id := range []string{toolbarIDStartPause, toolbarIDStop, toolbarIDCaptureInput, toolbarIDScreenshot} {
		item := appkit.NewToolbarItemWithItemIdentifier(appkit.NSToolbarItemIdentifier(id))
		item.SetAutovalidates(false)
		tb.items[id] = item
	}
	for _, tt := range []struct {
		name                 string
		state                vz.VZVirtualMachineState
		label                string
		start, stop, capture bool
	}{
		{"starting", vz.VZVirtualMachineStateStarting, "starting", false, false, false},
		{"running", vz.VZVirtualMachineStateRunning, "Pause", true, true, true},
		{"pausing", vz.VZVirtualMachineStatePausing, "pausing", false, false, false},
		{"paused", vz.VZVirtualMachineStatePaused, "Resume", true, true, false},
		{"resuming", vz.VZVirtualMachineStateResuming, "resuming", false, false, false},
		{"saving", vz.VZVirtualMachineStateSaving, "saving", false, false, false},
		{"stopping", vz.VZVirtualMachineStateStopping, "stopping", false, false, false},
		{"stopped", vz.VZVirtualMachineStateStopped, "Start", true, false, false},
		{"error", vz.VZVirtualMachineStateError, "Start", true, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tb.captureEnabled = true
			view.SetCapturesSystemKeys(true)
			tb.UpdateState(tt.state)
			if got := tb.items[toolbarIDStartPause]; got.IsEnabled() != tt.start || got.Label() != tt.label {
				t.Fatalf("start control: enabled=%v label=%q", got.IsEnabled(), got.Label())
			}
			if got := tb.items[toolbarIDStop].IsEnabled(); got != tt.stop {
				t.Errorf("stop enabled=%v, want %v", got, tt.stop)
			}
			if got := tb.items[toolbarIDCaptureInput].IsEnabled(); got != tt.capture {
				t.Errorf("capture enabled=%v, want %v", got, tt.capture)
			}
			if view.CapturesSystemKeys() != tt.capture || tb.captureEnabled != tt.capture {
				t.Errorf("capture remained active outside running state")
			}
		})
	}
}
