//go:build integration && darwin && arm64

package main

import (
	"bytes"
	"image/png"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmstate"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func testCtl(t *testing.T, vm *testVM) {
	t.Run("status", func(t *testing.T) {
		status := statusResponse(t, vm)
		if got := vmstate.Canonical(status.GetState()); got != "running" {
			t.Fatalf("status state: got %q, want %q", got, "running")
		}
	})

	t.Run("screenshot", func(t *testing.T) {
		requireGUI(t)

		data := screenshotPNG(t, vm)
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode screenshot PNG: %v", err)
		}
		if bounds := img.Bounds(); bounds.Dx() == 0 || bounds.Dy() == 0 {
			t.Fatalf("screenshot bounds: got %v", bounds)
		}
	})

	t.Run("pause-resume", func(t *testing.T) {
		status := statusResponse(t, vm)
		if !status.GetCanPause() {
			t.Skip("pause not supported for this VM")
		}

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "pause"})
		waitVMState(t, vm, "paused", 30*time.Second)

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "resume"})
		waitVMState(t, vm, "running", 30*time.Second)
		waitVMReady(t, vm, integrationVMReadyTimeout(vm, false))
	})
}
