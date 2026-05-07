package controlserver

import (
	"image"
	"sync"
	"testing"
)

func TestCaptureRememberBoundsNilImage(t *testing.T) {
	var c Capture
	c.RememberBounds(nil)
	w, h := c.LastBounds()
	if w != 0 || h != 0 {
		t.Fatalf("LastBounds = (%d,%d), want (0,0)", w, h)
	}
}

func TestCaptureRememberBounds(t *testing.T) {
	var c Capture
	img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	c.RememberBounds(img)
	w, h := c.LastBounds()
	if w != 1024 || h != 768 {
		t.Fatalf("LastBounds = (%d,%d), want (1024,768)", w, h)
	}
}

func TestCaptureDiffDisabledReturnsInput(t *testing.T) {
	var c Capture
	a := image.NewRGBA(image.Rect(0, 0, 4, 4))
	b := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if got := c.Diff(a, false); got != image.Image(a) {
		t.Fatal("Diff(_, false) should return the input image when no diff is requested")
	}
	if got := c.Diff(b, false); got != image.Image(b) {
		t.Fatal("Diff(_, false) should return the input image on subsequent calls too")
	}
}

func TestCaptureDiffNoPriorReturnsInput(t *testing.T) {
	var c Capture
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if got := c.Diff(img, true); got != image.Image(img) {
		t.Fatal("Diff(_, true) without a prior capture should return the input image")
	}
}

func TestCaptureDiffStoresPrior(t *testing.T) {
	var c Capture
	a := image.NewRGBA(image.Rect(0, 0, 4, 4))
	b := image.NewRGBA(image.Rect(0, 0, 4, 4))
	c.Diff(a, true)
	got := c.Diff(b, true)
	if got == image.Image(b) {
		t.Fatal("Diff with prior capture should return a diff image, not the input")
	}
}

func TestCaptureServiceLazyOnce(t *testing.T) {
	var c Capture
	first := c.Service(false)
	if first == nil {
		t.Fatal("Service returned nil")
	}
	second := c.Service(false)
	if first != second {
		t.Fatal("Service should cache the OCR service across calls")
	}
}

func TestCaptureConcurrentAccess(t *testing.T) {
	var c Capture
	const goroutines = 64
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			img := image.NewRGBA(image.Rect(0, 0, i+1, i+1))
			c.RememberBounds(img)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = c.LastBounds()
		}()
	}
	wg.Wait()
}
