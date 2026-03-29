// control_socket_ocr.go - OCR-driven automation methods for ControlServer
package main

import (
	"fmt"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// OCRClickText takes a screenshot, finds the given text via OCR, and clicks its center.
// Retries until text is found or timeout expires.
func (s *ControlServer) OCRClickText(ocr *OCRService, text string, timeout time.Duration) error {
	return s.OCRClickTextWithOptions(ocr, text, timeout, OCRSearchOptions{})
}

// OCRClickTextWithOptions takes a screenshot, finds text via OCR options, and clicks its center.
func (s *ControlServer) OCRClickTextWithOptions(ocr *OCRService, text string, timeout time.Duration, opts OCRSearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, errMsg := s.captureDisplayImage()
		if errMsg != "" {
			time.Sleep(time.Second)
			continue
		}

		normX, normY, found := ocr.FindTextNormalizedWithOptions(img, text, opts)
		if found {
			if verbose {
				fmt.Printf("[ocr-click] found %q at (%.3f, %.3f)\n", text, normX, normY)
			}
			resp := s.sendMouseEvent(&controlpb.MouseCommand{
				X:      normX,
				Y:      normY,
				Button: 0,
				Action: "click",
			})
			if !resp.Success {
				return fmt.Errorf("click %q: %s", text, resp.Error)
			}
			return nil
		}

		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout: text %q not found", text)
}

// OCRWaitForText polls screenshots until the given text appears or timeout expires.
func (s *ControlServer) OCRWaitForText(ocr *OCRService, text string, timeout time.Duration) error {
	return s.OCRWaitForTextWithOptions(ocr, text, timeout, OCRSearchOptions{})
}

// OCRWaitForTextWithOptions polls screenshots until text appears or timeout expires.
func (s *ControlServer) OCRWaitForTextWithOptions(ocr *OCRService, text string, timeout time.Duration, opts OCRSearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, errMsg := s.captureDisplayImage()
		if errMsg != "" {
			time.Sleep(time.Second)
			continue
		}

		_, _, found := ocr.FindTextNormalizedWithOptions(img, text, opts)
		if found {
			if verbose {
				fmt.Printf("[ocr-wait] found %q\n", text)
			}
			return nil
		}

		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout: text %q not found", text)
}

// OCRWaitAndClick waits for text to appear, then clicks it.
func (s *ControlServer) OCRWaitAndClick(ocr *OCRService, text string, timeout time.Duration) error {
	return s.OCRWaitAndClickWithOptions(ocr, text, timeout, OCRSearchOptions{})
}

// OCRWaitAndClickWithOptions waits for text using options, then clicks it.
func (s *ControlServer) OCRWaitAndClickWithOptions(ocr *OCRService, text string, timeout time.Duration, opts OCRSearchOptions) error {
	if err := s.OCRWaitForTextWithOptions(ocr, text, timeout, opts); err != nil {
		return err
	}
	// Brief pause to let the UI settle after text appears.
	time.Sleep(300 * time.Millisecond)
	return s.OCRClickTextWithOptions(ocr, text, 10*time.Second, opts)
}

// OCRAllText takes a screenshot and returns all recognized text.
func (s *ControlServer) OCRAllText(ocr *OCRService) (string, error) {
	img, errMsg := s.captureDisplayImage()
	if errMsg != "" {
		return "", fmt.Errorf("capture: %s", errMsg)
	}
	return ocr.AllText(img), nil
}

// OCRDetectPage takes a screenshot and identifies the Setup Assistant page.
func (s *ControlServer) OCRDetectPage(ocr *OCRService) string {
	img, errMsg := s.captureDisplayImage()
	if errMsg != "" {
		return "unknown"
	}
	return OCRDetectSetupAssistantPage(img, ocr)
}

// OCRWaitForPageChange polls until the detected page differs from currentPage.
func (s *ControlServer) OCRWaitForPageChange(ocr *OCRService, currentPage string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, errMsg := s.captureDisplayImage()
		if errMsg != "" {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		newPage := OCRDetectSetupAssistantPage(img, ocr)
		if newPage != currentPage {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("stuck on page %s", currentPage)
}
