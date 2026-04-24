// ocr.go - Vision framework OCR for screen text recognition.
//
// Delegates to github.com/tmc/apple/x/vzkit/ocr for the implementation.
package main

import (
	"image"

	"github.com/tmc/apple/x/vzkit/ocr"
)

// NewOCRService creates a new OCR service.
func NewOCRService(verbose bool) *ocr.Service {
	return ocr.NewService(verbose)
}

// bestMatchWithOptions delegates to ocr.BestMatch.
func bestMatchWithOptions(observations []ocr.TextObservation, needle string, opts ocr.SearchOptions, bounds image.Rectangle) (ocr.TextObservation, bool) {
	return ocr.BestMatch(observations, needle, opts, bounds)
}
