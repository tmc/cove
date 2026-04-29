package main

import "testing"

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

type fakeScreenshotGUI struct {
	status GUIStatus
}

func (g fakeScreenshotGUI) Open() error       { return nil }
func (g fakeScreenshotGUI) Close() error      { return nil }
func (g fakeScreenshotGUI) Status() GUIStatus { return g.status }
func (g fakeScreenshotGUI) Shutdown()         {}
