package main

import (
	"sync"
	"testing"
)

func TestCaptureDisplayImageHeadlessDoesNotFallBackToWindow(t *testing.T) {
	s := NewControlServerWithVMDir("", "")
	s.gui = fakeScreenshotGUI{status: GUIStatus{Headed: false}}

	img, errMsg := s.captureDisplayImage()
	if img != nil {
		t.Fatalf("captureDisplayImage image = %v, want nil", img)
	}
	if errMsg == "" {
		t.Fatal("captureDisplayImage error = empty, want private capture error")
	}
	if errMsg == "window not set" {
		t.Fatalf("captureDisplayImage error = %q, want private capture error", errMsg)
	}
}

func TestCaptureStateCopiesFieldsUnderLock(t *testing.T) {
	s := NewControlServerWithVMDir("", "")
	gui := fakeScreenshotGUI{status: GUIStatus{Headed: true}}
	s.mu.Lock()
	s.windowNum = 7
	s.viewContentHeight = 768
	s.gui = gui
	s.mu.Unlock()

	state := s.captureState()
	if state.windowNum != 7 {
		t.Fatalf("windowNum = %d, want 7", state.windowNum)
	}
	if state.viewContentHeight != 768 {
		t.Fatalf("viewContentHeight = %d, want 768", state.viewContentHeight)
	}
	if state.gui == nil {
		t.Fatal("gui = nil, want installed controller")
	}
}

func TestCaptureStateConcurrentAccess(t *testing.T) {
	s := NewControlServerWithVMDir("", "")

	const goroutines = 64
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.mu.Lock()
			s.windowNum = i
			s.viewContentHeight = i + 1
			s.mu.Unlock()
		}(i)
		go func() {
			defer wg.Done()
			_ = s.captureState()
		}()
	}
	wg.Wait()
}

type fakeScreenshotGUI struct {
	status GUIStatus
}

func (g fakeScreenshotGUI) Open() error       { return nil }
func (g fakeScreenshotGUI) Close() error      { return nil }
func (g fakeScreenshotGUI) Status() GUIStatus { return g.status }
func (g fakeScreenshotGUI) Shutdown()         {}
