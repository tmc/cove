package main

import (
	"image"
	"sync"
	"testing"
)

func TestScreenCaptureRememberBoundsNilImage(t *testing.T) {
	var c screenCapture
	c.rememberBounds(nil)
	w, h := c.lastBounds()
	if w != 0 || h != 0 {
		t.Fatalf("lastBounds = (%d,%d), want (0,0)", w, h)
	}
}

func TestScreenCaptureRememberBounds(t *testing.T) {
	var c screenCapture
	img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	c.rememberBounds(img)
	w, h := c.lastBounds()
	if w != 1024 || h != 768 {
		t.Fatalf("lastBounds = (%d,%d), want (1024,768)", w, h)
	}
}

func TestScreenCaptureDiffDisabledReturnsInput(t *testing.T) {
	var c screenCapture
	a := image.NewRGBA(image.Rect(0, 0, 4, 4))
	b := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if got := c.diff(a, false); got != image.Image(a) {
		t.Fatal("diff(_, false) should return the input image when no diff is requested")
	}
	if got := c.diff(b, false); got != image.Image(b) {
		t.Fatal("diff(_, false) should return the input image on subsequent calls too")
	}
}

func TestScreenCaptureDiffNoPriorReturnsInput(t *testing.T) {
	var c screenCapture
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if got := c.diff(img, true); got != image.Image(img) {
		t.Fatal("diff(_, true) without a prior capture should return the input image")
	}
}

func TestScreenCaptureDiffStoresPrior(t *testing.T) {
	var c screenCapture
	a := image.NewRGBA(image.Rect(0, 0, 4, 4))
	b := image.NewRGBA(image.Rect(0, 0, 4, 4))
	c.diff(a, true)
	got := c.diff(b, true)
	if got == image.Image(b) {
		t.Fatal("diff with prior capture should return a diff image, not the input")
	}
}

func TestScreenCaptureServiceLazyOnce(t *testing.T) {
	var c screenCapture
	first := c.service(false)
	if first == nil {
		t.Fatal("service returned nil")
	}
	second := c.service(false)
	if first != second {
		t.Fatal("service should cache the OCR service across calls")
	}
}

func TestScreenCaptureConcurrentAccess(t *testing.T) {
	var c screenCapture
	const goroutines = 64
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			img := image.NewRGBA(image.Rect(0, 0, i+1, i+1))
			c.rememberBounds(img)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = c.lastBounds()
		}()
	}
	wg.Wait()
}
