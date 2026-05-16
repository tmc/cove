//go:build darwin

package main

import (
	"fmt"
	"image"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/x/capture"
)

func (s *ControlServer) capturePrivateGraphicsDisplay() (image.Image, string) {
	state := s.captureState()
	if state.vmView.ID == 0 {
		return nil, "vm view not set"
	}

	var (
		cgImage coregraphics.CGImageRef
		mode    string
		errMsg  string
	)

	runOnUIThreadSync(func() {
		pool := foundation.NewNSAutoreleasePool()
		defer pool.Drain()

		vmView := vmViewAsNSView(state.vmView)
		captureView := vmView
		mode = "view-cache"

		vmViewObj := objectivec.ObjectFromID(state.vmView.ID)
		framebufferIvar := objectivec.Class_getInstanceVariable(objc.GetClass("VZVirtualMachineView"), "_framebufferView")
		if framebufferIvar != 0 {
			framebufferObj := objectivec.Object_getIvar(vmViewObj, framebufferIvar)
			if framebufferObj.ID != 0 {
				captureView = appkit.NSViewFromID(framebufferObj.ID)
				mode = "private-framebuffer"
			}
		}

		if state.window.ID != 0 {
			state.window.DisplayIfNeeded()
		}
		vmView.LayoutSubtreeIfNeeded()
		vmView.SetNeedsDisplay(true)
		vmView.DisplayIfNeeded()
		captureView.LayoutSubtreeIfNeeded()
		captureView.SetNeedsDisplay(true)
		captureView.DisplayIfNeeded()

		bounds := captureView.VisibleRect()
		if bounds.Size.Width <= 0 || bounds.Size.Height <= 0 {
			bounds = captureView.Bounds()
		}
		if bounds.Size.Width <= 0 || bounds.Size.Height <= 0 {
			bounds = captureView.Frame()
		}
		if bounds.Size.Width <= 0 || bounds.Size.Height <= 0 {
			errMsg = fmt.Sprintf("%s has empty bounds %.0fx%.0f", mode, bounds.Size.Width, bounds.Size.Height)
			return
		}

		rep := bitmapImageRepForCachingDisplay(captureView, bounds)
		if rep.GetID() == 0 {
			errMsg = fmt.Sprintf("%s bitmap cache setup failed", mode)
			return
		}
		cacheDisplayInRectToBitmapImageRep(captureView, bounds, rep)

		imageRef := coregraphics.CGImageRef(objc.Send[objc.ID](rep.GetID(), objc.Sel("CGImage")))
		if imageRef == 0 {
			errMsg = fmt.Sprintf("%s cgimage is nil", mode)
			return
		}
		cgImage = coregraphics.CGImageRetain(imageRef)
	})

	if errMsg != "" {
		return nil, errMsg
	}
	if cgImage == 0 {
		return nil, "private capture returned nil image"
	}
	defer coregraphics.CGImageRelease(cgImage)

	if verbose {
		fmt.Printf("[screenshot] using %s capture\n", mode)
	}
	img, err := capture.GoImageFromCGImage(cgImage, 0)
	if err != nil {
		return nil, err.Error()
	}
	return img, ""
}
