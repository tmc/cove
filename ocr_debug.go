// ocr_debug.go - Debug visualization for OCR results.
//
// Delegates to github.com/tmc/apple/x/vzkit/ocr for the implementation.
package main

import (
	"image"
	"image/color"

	"github.com/tmc/apple/x/vzkit/ocr"
)

// saveOCRDebugScreenshot saves a screenshot with OCR bounding boxes overlaid.
func saveOCRDebugScreenshot(img image.Image, observations []ocr.TextObservation, dir, prefix string) error {
	return ocr.SaveDebugScreenshot(img, observations, dir, prefix)
}

// drawRect draws a rectangle outline on an RGBA image.
func drawRect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA) {
	ocr.DrawRect(img, x1, y1, x2, y2, c)
}
