// screenshots.go - Screenshot capture, diff, and compression for VM control
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"unsafe"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"golang.org/x/image/draw"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// takeScreenshotWithOptions captures the VM view with specified options
func (s *ControlServer) takeScreenshotWithOptions(opts *controlpb.ScreenshotCommand) *controlpb.ControlResponse {
	if s.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "screenshot requires GUI mode (run with -gui)"}
	}

	// Apply defaults
	scale := opts.Scale
	if scale <= 0 || scale > 1 {
		scale = 0.5
	}
	quality := opts.Quality
	if quality <= 0 || quality > 100 {
		quality = 60
	}
	format := opts.Format
	if format == "" {
		format = "jpeg"
	}

	// Capture raw screenshot.
	img, errMsg := s.captureDisplayImage()
	if errMsg != "" {
		return &controlpb.ControlResponse{Error: errMsg}
	}

	// Generate diff if requested and we have a previous screenshot
	var outputImg image.Image = img
	s.screenshotMu.Lock()
	if opts.Diff && s.lastScreenshot != nil {
		outputImg = generateDiff(s.lastScreenshot, img)
	}
	s.lastScreenshot = img
	s.screenshotMu.Unlock()

	// Scale image
	if scale < 1 {
		outputImg = scaleImage(outputImg, scale)
	}

	// Encode to bytes
	var rawBytes []byte
	var encErr string
	if format == "png" {
		rawBytes, encErr = encodePNGBytes(outputImg)
	} else {
		rawBytes, encErr = encodeJPEGBytes(outputImg, int(quality))
	}
	if encErr != "" {
		return &controlpb.ControlResponse{Error: encErr}
	}

	encoded := base64.StdEncoding.EncodeToString(rawBytes)
	bounds := outputImg.Bounds()
	return &controlpb.ControlResponse{
		Success: true,
		Data:    encoded,
		Result: &controlpb.ControlResponse_ScreenshotResult{ScreenshotResult: &controlpb.ScreenshotResponse{
			ImageData: rawBytes,
			Format:    format,
			Width:     int32(bounds.Dx()),
			Height:    int32(bounds.Dy()),
		}},
	}
}

func (s *ControlServer) captureDisplayImage() (image.Image, string) {
	s.mu.Lock()
	gui := s.gui
	s.mu.Unlock()
	if gui != nil {
		status := gui.Status()
		if !status.Headed {
			if img, errMsg := s.capturePrivateGraphicsDisplay(); errMsg == "" {
				return img, ""
			} else if verbose {
				fmt.Printf("[screenshot] private capture unavailable: %s\n", errMsg)
			}
		}
	}
	return s.captureVMView()
}

// captureVMView captures the raw image from the VM view using CGWindowListCreateImage
func (s *ControlServer) captureVMView() (image.Image, string) {
	if s.window.ID == 0 {
		return nil, "window not set"
	}

	// Use cached window number to avoid AppKit call from background thread.
	windowNum := s.windowNum
	if windowNum <= 0 {
		return nil, fmt.Sprintf("invalid window number: %d", windowNum)
	}

	// Use CGWindowListCreateImage to capture the window
	// CGRectNull (all zeros) means capture the minimum rect that encloses the window
	bounds := corefoundation.CGRect{} // CGRectNull

	// CGWindowListOption: kCGWindowListOptionIncludingWindow = 1 << 3 = 8
	// CGWindowImageOption: kCGWindowImageBoundsIgnoreFraming = 1
	// Use BoundsIgnoreFraming to capture only the content area (no title bar),
	// so OCR pixel coordinates map directly to VM view coordinates.
	cgImage := coregraphics.CGWindowListCreateImage(
		bounds,
		coregraphics.CGWindowListOption(8), // kCGWindowListOptionIncludingWindow
		coregraphics.CGWindowID(windowNum),
		coregraphics.CGWindowImageOption(1), // kCGWindowImageBoundsIgnoreFraming
	)

	if cgImage == 0 {
		if verbose {
			fmt.Printf("[screenshot] CGWindowListCreateImage returned nil for windowNum=%d\n", windowNum)
		}
		return nil, "CGWindowListCreateImage returned nil"
	}
	defer coregraphics.CGImageRelease(cgImage)

	// Determine the title bar offset by comparing CGImage height to the
	// VM view bounds. CGWindowListCreateImage captures the full window
	// (including the title bar) even with kCGWindowImageBoundsIgnoreFraming.
	// We crop to match the view content area so OCR pixel coordinates map
	// directly to normalized mouse input coordinates.
	titleBarPx := 0
	height := int(coregraphics.CGImageGetHeight(cgImage))
	viewH := s.viewContentHeight
	if viewH > 0 && height > viewH {
		titleBarPx = height - viewH
		if verbose {
			fmt.Printf("[screenshot] cropping %dpx title bar (screenshot=%d, view=%d)\n",
				titleBarPx, height, viewH)
		}
	}

	return goImageFromCGImage(cgImage, titleBarPx)
}

func goImageFromCGImage(cgImage coregraphics.CGImageRef, cropTopPx int) (image.Image, string) {
	if cgImage == 0 {
		return nil, "cgimage is nil"
	}

	// Get image dimensions
	width := int(coregraphics.CGImageGetWidth(cgImage))
	height := int(coregraphics.CGImageGetHeight(cgImage))
	if width == 0 || height == 0 {
		return nil, fmt.Sprintf("invalid image dimensions: %dx%d", width, height)
	}

	// Get the data provider and copy data
	dataProvider := coregraphics.CGImageGetDataProvider(cgImage)
	if dataProvider == 0 {
		return nil, "failed to get data provider"
	}

	cfData := coregraphics.CGDataProviderCopyData(dataProvider)
	if cfData == 0 {
		return nil, "failed to copy data from provider"
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(cfData))

	// Get the raw bytes
	dataLength := corefoundation.CFDataGetLength(cfData)
	dataPtr := corefoundation.CFDataGetBytePtr(cfData)

	if dataPtr == nil || dataLength == 0 {
		return nil, "failed to get data bytes"
	}

	// Get bytes per row
	bytesPerRow := int(coregraphics.CGImageGetBytesPerRow(cgImage))
	contentHeight := height - cropTopPx
	if contentHeight <= 0 {
		return nil, fmt.Sprintf("invalid cropped height: %d", contentHeight)
	}

	// Convert to Go image - CGImage typically uses BGRA format
	rgba := image.NewRGBA(image.Rect(0, 0, width, contentHeight))
	srcData := unsafe.Slice((*byte)(dataPtr), dataLength)
	dstData := rgba.Pix

	for y := 0; y < contentHeight; y++ {
		srcRowStart := (y + cropTopPx) * bytesPerRow
		dstRowStart := y * rgba.Stride
		for x := 0; x < width; x++ {
			srcPixel := srcRowStart + x*4
			if srcPixel+3 < int(dataLength) {
				dstPixel := dstRowStart + x*4
				// BGRA to RGBA
				dstData[dstPixel] = srcData[srcPixel+2]   // R
				dstData[dstPixel+1] = srcData[srcPixel+1] // G
				dstData[dstPixel+2] = srcData[srcPixel]   // B
				dstData[dstPixel+3] = srcData[srcPixel+3] // A
			}
		}
	}

	return rgba, ""
}

// generateDiff creates a diff image highlighting changes between two images
func generateDiff(old, new image.Image) image.Image {
	oldBounds := old.Bounds()
	newBounds := new.Bounds()

	// Use the new image's bounds
	width := newBounds.Dx()
	height := newBounds.Dy()

	diff := image.NewRGBA(image.Rect(0, 0, width, height))

	// Check if dimensions match
	if oldBounds.Dx() != width || oldBounds.Dy() != height {
		// Dimensions changed - return new image with red border
		draw.Copy(diff, image.Point{}, new, newBounds, draw.Src, nil)
		// Add red border to indicate size change
		for x := 0; x < width; x++ {
			diff.SetRGBA(x, 0, color.RGBA{255, 0, 0, 255})
			diff.SetRGBA(x, height-1, color.RGBA{255, 0, 0, 255})
		}
		for y := 0; y < height; y++ {
			diff.SetRGBA(0, y, color.RGBA{255, 0, 0, 255})
			diff.SetRGBA(width-1, y, color.RGBA{255, 0, 0, 255})
		}
		return diff
	}

	// Generate per-pixel diff
	threshold := uint32(10 * 256) // Color difference threshold
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			oldR, oldG, oldB, _ := old.At(x+oldBounds.Min.X, y+oldBounds.Min.Y).RGBA()
			newR, newG, newB, newA := new.At(x+newBounds.Min.X, y+newBounds.Min.Y).RGBA()

			// Calculate color difference
			dr := absDiff(oldR, newR)
			dg := absDiff(oldG, newG)
			db := absDiff(oldB, newB)

			if dr > threshold || dg > threshold || db > threshold {
				// Pixel changed - show in color with highlight
				diff.SetRGBA(x, y, color.RGBA{
					R: uint8(newR >> 8),
					G: uint8(newG >> 8),
					B: uint8(newB >> 8),
					A: uint8(newA >> 8),
				})
			} else {
				// Pixel unchanged - show as dimmed grayscale
				gray := uint8((newR + newG + newB) / 3 >> 8)
				diff.SetRGBA(x, y, color.RGBA{gray / 3, gray / 3, gray / 3, 255})
			}
		}
	}

	return diff
}

// scaleImage resizes an image by the given scale factor
func scaleImage(img image.Image, scale float64) image.Image {
	bounds := img.Bounds()
	newWidth := int(float64(bounds.Dx()) * scale)
	newHeight := int(float64(bounds.Dy()) * scale)

	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}

	scaled := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.BiLinear.Scale(scaled, scaled.Bounds(), img, bounds, draw.Over, nil)
	return scaled
}

// encodeJPEGBytes encodes an image as JPEG with the given quality, returning raw bytes.
func encodeJPEGBytes(img image.Image, quality int) ([]byte, string) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, "failed to encode JPEG: " + err.Error()
	}
	return buf.Bytes(), ""
}

// encodePNGBytes encodes an image as PNG, returning raw bytes.
func encodePNGBytes(img image.Image) ([]byte, string) {
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		return nil, "failed to encode PNG: " + err.Error()
	}
	return buf.Bytes(), ""
}

// absDiff returns the absolute difference between two uint32 values
func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
