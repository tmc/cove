package main

import "image"

// bootOverlayFadeFrames is the length of the boot overlay fade-out, in run-loop
// frames (~0.5s at 30 Hz).
const bootOverlayFadeFrames = 15

// bootOverlayFadeInput is everything the run loop knows about whether the boot
// overlay may start fading. It is a plain value so the decision is testable
// without a live VM.
type bootOverlayFadeInput struct {
	// vmRunning reports whether the VM reached the Running state. Running
	// means the virtualization is up, not that the guest painted anything.
	vmRunning bool
	// holdForFirstBoot is the first-boot/provisioning hold: the overlay
	// deliberately covers a window of boot that has no useful guest UI.
	holdForFirstBoot bool
	// agentReady reports that the guest agent checked in, which means the
	// guest is well past painting its first frame.
	agentReady bool
	// paintProbeWorks reports that the guest framebuffer can be sampled
	// without capturing cove's own overlays. When false the paint floor is
	// skipped rather than guessed at.
	paintProbeWorks bool
	// guestPainted reports that a sampled guest frame showed content.
	guestPainted bool
	// heldFrames counts run-loop frames the overlay has been up for, bounded
	// by maxHoldFrames so a guest that never paints still gets the screen.
	heldFrames    int
	maxHoldFrames int
}

// shouldFadeBootOverlay reports whether the boot overlay may start fading.
//
// The paint floor is why this is not simply "VM is Running": on a cold boot the
// guest framebuffer stays black for a minute or more after the VM reports
// Running, and fading there leaves the user staring at a black window. The
// floor is additional to the existing first-boot hold, never a replacement for
// it, and the whole thing is bounded by maxHoldFrames.
func shouldFadeBootOverlay(in bootOverlayFadeInput) bool {
	if in.maxHoldFrames > 0 && in.heldFrames >= in.maxHoldFrames {
		return true
	}
	if !in.vmRunning {
		return false
	}
	if in.agentReady {
		return true
	}
	if in.holdForFirstBoot {
		return false
	}
	if in.paintProbeWorks && !in.guestPainted {
		return false
	}
	return true
}

// guestFrameHasContent reports whether a captured guest frame shows anything
// other than a black screen.
//
// It leans on the same classification as the rest of the automation
// (DetectScreenState / ScreenStateBlack, "overall brightness < 10") and adds a
// bright-pixel test, because the first thing a macOS guest paints is a small
// white Apple logo on black: mean brightness stays under the black threshold
// while the screen is very obviously no longer blank.
func guestFrameHasContent(img image.Image) bool {
	if img == nil {
		return false
	}
	if DetectScreenState(img) != ScreenStateBlack {
		return true
	}
	return brightPixelFraction(img) > 0.002
}

// brightPixelFraction is the fraction of sampled pixels that are clearly lit.
func brightPixelFraction(img image.Image) float64 {
	bounds := img.Bounds()
	if bounds.Empty() {
		return 0
	}
	const (
		sampleStep      = 10
		brightThreshold = 120
	)
	var bright, total int
	for y := bounds.Min.Y; y < bounds.Max.Y; y += sampleStep {
		for x := bounds.Min.X; x < bounds.Max.X; x += sampleStep {
			r, g, b, _ := img.At(x, y).RGBA()
			if int(r>>8)+int(g>>8)+int(b>>8) >= brightThreshold*3 {
				bright++
			}
			total++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(bright) / float64(total)
}

// probeGuestPainted samples the guest framebuffer and reports whether the
// sample showed content and whether the probe itself is usable.
//
// Only the private-framebuffer capture mode is trusted: the view-cache fallback
// caches the whole VM view, which includes the boot overlay itself, so it would
// only ever measure cove's own dark rectangle.
func probeGuestPainted(s *ControlServer) (painted, ok bool) {
	if s == nil {
		return false, false
	}
	img, mode, errMsg := s.capturePrivateGraphicsDisplayMode()
	if errMsg != "" || img == nil || mode != "private-framebuffer" {
		return false, false
	}
	return guestFrameHasContent(img), true
}
