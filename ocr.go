// ocr.go - Vision framework OCR for screen text recognition.
//
// Delegates to github.com/tmc/apple/x/vzkit/ocr for the implementation.
// Type aliases maintain backwards compatibility with existing callsites.
package main

import (
	"image"

	"github.com/tmc/apple/x/vzkit/ocr"
)

// TextObservation is an alias for ocr.TextObservation.
type TextObservation = ocr.TextObservation

// OCRService is an alias for ocr.Service.
type OCRService = ocr.Service

// NewOCRService creates a new OCR service.
func NewOCRService(verbose bool) *OCRService {
	return ocr.NewService(verbose)
}

// bestMatchWithOptions delegates to ocr.BestMatch.
func bestMatchWithOptions(observations []TextObservation, needle string, opts OCRSearchOptions, bounds image.Rectangle) (TextObservation, bool) {
	return ocr.BestMatch(observations, needle, opts, bounds)
}
