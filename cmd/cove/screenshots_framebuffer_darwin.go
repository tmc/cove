//go:build darwin

// screenshots_framebuffer_darwin.go - Windowless screenshot capture via the
// private VZ framebuffer layer.
//
// This backend is opt-in via COVE_SCREENSHOT_BACKEND=framebuffer. It locates
// the live VZFramebuffer object behind the running VM's graphics stack (the
// same bounded ivar-walk technique used by pgdisplay_status_darwin.go),
// triggers the private _takeScreenshotWithCompletionHandler:imageConversionBlock:
// selector, and reads the resulting pixels back from the framebuffer's
// IOSurface — no NSWindow or CGWindowList involved, so it works fully
// headless.
//
// Every private-API step is respondsToSelector-gated. On any failure the
// caller falls back to the existing capture paths and logs once.

package main

import (
	"context"
	"fmt"
	"image"
	"time"
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/iosurface"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
)

const vzfbTakeScreenshotSel = "_takeScreenshotWithCompletionHandler:imageConversionBlock:"

// captureVZFramebuffer captures the current guest frame directly from the
// private VZFramebuffer. It returns the image or a reason string; it never
// panics on missing private API — every step is probed.
func (s *ControlServer) captureVZFramebuffer() (image.Image, string) {
	s.mu.Lock()
	vm := s.vm
	s.mu.Unlock()
	state := s.captureState()

	if vm.ID == 0 && state.vmView.ID == 0 {
		return nil, "vm not set"
	}

	// Locate the framebuffer on the UI thread: the ivar walk touches
	// live AppKit/VZ object graphs.
	var fbID objc.ID
	var fbPath string
	runOnUIThreadSync(func() {
		pool := foundation.NewNSAutoreleasePool()
		defer pool.Drain()

		roots := []objc.ID{}
		if vm.ID != 0 {
			roots = append(roots, vm.ID)
		}
		if state.vmView.ID != 0 {
			roots = append(roots, state.vmView.ID)
		}
		fbID, fbPath = findVZFramebuffer(roots)
		if fbID == 0 && vm.ID != 0 {
			// Fall back to constructing a detached display for the
			// first graphics device/framebuffer and walking from it.
			fbID, fbPath = findVZFramebufferViaDisplay(vm.ID)
		}
		if fbID != 0 {
			objc.Send[objc.ID](fbID, objc.Sel("retain"))
		}
	})
	if fbID == 0 {
		return nil, "vz framebuffer not located"
	}
	defer objc.Send[objc.ID](fbID, objc.Sel("release"))
	if verbose {
		fmt.Printf("[screenshot] framebuffer backend using %s\n", fbPath)
	}

	fb := pvz.VZFramebufferFromID(fbID)
	if !fb.CanTakeScreenshotWithCompletionHandlerImageConversionBlock() {
		return nil, "framebuffer does not respond to " + vzfbTakeScreenshotSel
	}

	// Trigger the screenshot and wait for either block to fire. The
	// generated binding exposes both blocks as void handlers; which one
	// carries the payload needs empirical confirmation on a live VM, so
	// treat either invocation as completion and read pixels from the
	// framebuffer's IOSurface afterwards.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{}, 2)
	if err := fb.TakeScreenshotWithCompletionHandlerImageConversionBlock(
		func() { done <- struct{}{} },
		func() { done <- struct{}{} },
	); err != nil {
		return nil, fmt.Sprintf("take screenshot: %v", err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		return nil, "framebuffer screenshot timed out"
	}

	surface := vzfbLocateIOSurface(fbID)
	if surface == 0 {
		return nil, "framebuffer exposes no readable IOSurface"
	}
	img, err := goImageFromIOSurface(surface)
	if err != nil {
		return nil, fmt.Sprintf("decode iosurface: %v", err)
	}
	return img, ""
}

// findVZFramebuffer walks the ivar graphs from the given roots looking
// for the live VZFramebuffer (identified by class name or by responding
// to the private take-screenshot selector). Reuses the bounded BFS
// helpers from pgdisplay_status_darwin.go.
func findVZFramebuffer(roots []objc.ID) (objc.ID, string) {
	type node struct {
		id    objc.ID
		path  string
		depth int
	}
	const (
		maxDepth = 6
		maxNodes = 2000
	)
	visited := make(map[objc.ID]bool)
	queue := make([]node, 0, len(roots))
	for _, r := range roots {
		if r != 0 && !visited[r] {
			visited[r] = true
			queue = append(queue, node{id: r, path: objectClassName(r)})
		}
	}
	seen := 0
	for len(queue) > 0 && seen < maxNodes {
		n := queue[0]
		queue = queue[1:]
		seen++

		if isVZFramebuffer(n.id) {
			return n.id, n.path
		}
		if n.depth >= maxDepth {
			continue
		}
		for _, child := range objectIvarObjects(n.id) {
			if child.id == 0 || visited[child.id] {
				continue
			}
			visited[child.id] = true
			queue = append(queue, node{
				id:    child.id,
				path:  n.path + "." + child.name,
				depth: n.depth + 1,
			})
		}
	}
	return 0, ""
}

// isVZFramebuffer reports whether id is the private VZ framebuffer. The
// class-name check catches the common case; the selector probe covers
// renamed subclasses across macOS releases.
func isVZFramebuffer(id objc.ID) bool {
	if objectClassName(id) == "VZFramebuffer" {
		return true
	}
	return objc.RespondsToSelector(id, objc.Sel(vzfbTakeScreenshotSel))
}

// findVZFramebufferViaDisplay constructs a detached private display for
// graphics device 0 / framebuffer 0 of the VM and walks its ivars for
// the framebuffer. This covers fully headless runs where no
// VZVirtualMachineView (and thus no framebuffer view) exists.
func findVZFramebufferViaDisplay(vmID objc.ID) (objc.ID, string) {
	uuid := foundation.NewNSUUID()
	display := pvz.NewMacGraphicsDisplayWithVirtualMachineGraphicsDeviceIndexFramebufferIndexUuid(
		objectivec.ObjectFromID(vmID), 0, 0, uuid)
	if display.ID == 0 {
		return 0, ""
	}
	id, path := findVZFramebuffer([]objc.ID{display.ID})
	if id == 0 {
		return 0, ""
	}
	return id, "VZMacGraphicsDisplay(detached)." + path
}

// vzfbSurfaceSelectors are candidate accessors for the framebuffer's
// backing IOSurface, probed in order. The exact accessor is private and
// may shift per macOS release.
var vzfbSurfaceSelectors = []string{
	"ioSurface",
	"_ioSurface",
	"surface",
	"_surface",
	"screenshotSurface",
	"_screenshotSurface",
}

// vzfbLocateIOSurface probes the framebuffer object for an IOSurface
// accessor and validates the result has non-zero geometry.
func vzfbLocateIOSurface(fbID objc.ID) iosurface.IOSurfaceRef {
	for _, sel := range vzfbSurfaceSelectors {
		if !objc.RespondsToSelector(fbID, objc.Sel(sel)) {
			continue
		}
		id := objc.Send[objc.ID](fbID, objc.Sel(sel))
		if id == 0 {
			continue
		}
		surface := iosurface.IOSurfaceRef(id)
		if iosurface.IOSurfaceGetWidth(surface) == 0 || iosurface.IOSurfaceGetHeight(surface) == 0 {
			continue
		}
		if verbose {
			fmt.Printf("[screenshot] framebuffer iosurface via -%s\n", sel)
		}
		return surface
	}
	return 0
}

// goImageFromIOSurface copies the surface pixels into a Go image,
// converting BGRA (the VZ framebuffer default) to RGBA.
func goImageFromIOSurface(surface iosurface.IOSurfaceRef) (image.Image, error) {
	if iosurface.IOSurfaceGetPlaneCount(surface) > 1 {
		return nil, fmt.Errorf("planar surface (%d planes) not supported", iosurface.IOSurfaceGetPlaneCount(surface))
	}
	if rc := iosurface.IOSurfaceLock(surface, iosurface.IOSurfaceLockOptions(1), nil); rc != 0 { // kIOSurfaceLockReadOnly
		return nil, fmt.Errorf("IOSurfaceLock failed: %d", rc)
	}
	defer iosurface.IOSurfaceUnlock(surface, iosurface.IOSurfaceLockOptions(1), nil)

	base := iosurface.IOSurfaceGetBaseAddress(surface)
	if base == nil {
		return nil, fmt.Errorf("surface base address is nil")
	}
	width := int(iosurface.IOSurfaceGetWidth(surface))
	height := int(iosurface.IOSurfaceGetHeight(surface))
	stride := int(iosurface.IOSurfaceGetBytesPerRow(surface))
	if width <= 0 || height <= 0 || stride < width*4 {
		return nil, fmt.Errorf("unexpected surface geometry %dx%d stride %d", width, height, stride)
	}
	src := unsafe.Slice((*byte)(base), height*stride)

	// 'RGBA' surfaces copy through; anything else (typically 'BGRA')
	// gets the blue/red swap.
	rgbaOrder := iosurface.IOSurfaceGetPixelFormat(surface) == 0x52474241 // 'RGBA'
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := src[y*stride : y*stride+width*4]
		dst := img.Pix[y*img.Stride : y*img.Stride+width*4]
		if rgbaOrder {
			copy(dst, row)
			continue
		}
		for x := 0; x < width*4; x += 4 {
			dst[x+0] = row[x+2]
			dst[x+1] = row[x+1]
			dst[x+2] = row[x+0]
			dst[x+3] = row[x+3]
		}
	}
	return img, nil
}
