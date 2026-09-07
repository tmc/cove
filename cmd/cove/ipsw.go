// IPSW download and restore image handling for macOS VMs.
//
// Downloads use curl with bounded stalls and resumable retries.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
)

type ipswSource struct {
	Path  string
	IsURL bool
}

func parseIPSWSource(raw string) (ipswSource, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ipswSource{}, fmt.Errorf("ipsw source is empty")
	}
	u, err := url.Parse(raw)
	if err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "http", "https":
			if u.Host == "" {
				return ipswSource{}, fmt.Errorf("ipsw url missing host")
			}
			return ipswSource{IsURL: true}, nil
		case "file":
			if u.Path == "" {
				return ipswSource{}, fmt.Errorf("ipsw file url missing path")
			}
			return ipswSource{Path: u.Path}, nil
		default:
			return ipswSource{}, fmt.Errorf("unsupported ipsw url scheme %q", u.Scheme)
		}
	}
	return ipswSource{Path: raw}, nil
}

func verifyIPSWFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrIPSWMissing, path)
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < ipswMinSize {
		return fmt.Errorf("%w: have %.1f GB, want >= %.1f GB", ErrIPSWTooSmall, float64(info.Size())/(1024*1024*1024), float64(ipswMinSize)/(1024*1024*1024))
	}
	if !readerHasZipEOCD(f, info.Size()) {
		return fmt.Errorf("%w: missing zip end record", ErrIPSWCorrupt)
	}
	return nil
}

func readerHasZipEOCD(r io.ReaderAt, size int64) bool {
	const tailSize = 256
	offset := size - tailSize
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, tailSize)
	n, err := r.ReadAt(buf, offset)
	if err != nil && n == 0 {
		return false
	}
	buf = buf[:n]
	for i := len(buf) - 4; i >= 0; i-- {
		if buf[i] == 0x50 && buf[i+1] == 0x4b && buf[i+2] == 0x05 && buf[i+3] == 0x06 {
			return true
		}
	}
	return false
}

// printDownloadProgress displays a progress bar for IPSW download.
func printDownloadProgress(percent float64, current, total int64, speedMBps float64, remaining time.Duration) {
	const barWidth = 30
	filled := int(percent / 100 * barWidth)
	if filled > barWidth {
		filled = barWidth
	}
	bar := make([]byte, barWidth)
	for i := range bar {
		if i < filled {
			bar[i] = '='
		} else if i == filled {
			bar[i] = '>'
		} else {
			bar[i] = ' '
		}
	}
	fmt.Printf("\r\033[K  [%s] %5.1f%% (%.1f/%.1f GB) %.0f MB/s ETA %v",
		string(bar), percent,
		float64(current)/(1024*1024*1024),
		float64(total)/(1024*1024*1024),
		speedMBps,
		remaining.Truncate(time.Second))
}

// getHTTPContentLength returns the Content-Length for a URL, or 0 on error.
// With -L (follow redirects), curl outputs headers for every hop. We return
// the last Content-Length, which corresponds to the final 200 response.
func getHTTPContentLength(ctx context.Context, urlStr string) int64 {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "curl", "--connect-timeout", "15", "--max-time", "15", "-sI", "-L", urlStr)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var last int64
	for _, line := range strings.Split(string(out), "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "content-length:") {
			valStr := strings.TrimSpace(strings.TrimPrefix(lower, "content-length:"))
			var val int64
			fmt.Sscanf(valStr, "%d", &val)
			if val > 0 {
				last = val
			}
		}
	}
	return last
}

// downloadRestoreImageVZWithProgress is like downloadRestoreImageVZ but also
// reports progress via a callback for GUI display.
func downloadRestoreImageVZWithProgress(ctx context.Context, destPath string, progress progressFunc) error {
	var restoreImage vz.VZMacOSRestoreImage
	var fetchErr error
	done := make(chan struct{})

	start := time.Now()
	vz.GetVZMacOSRestoreImageClass().FetchLatestSupportedWithCompletionHandler(func(img *vz.VZMacOSRestoreImage, err error) {
		if err != nil {
			fetchErr = err
		}
		if img != nil && img.ID != 0 {
			img.Retain()
			restoreImage = *img
		}
		close(done)
	})

	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	for {
		select {
		case <-done:
			if fetchErr != nil {
				fmt.Printf("\r\033[K")
				return hintEntitlements(fetchErr)
			}
			fmt.Printf("\r\033[K")
		default:
			if time.Since(start) > 30*time.Second {
				fmt.Printf("\r\033[K")
				return fmt.Errorf("timeout fetching restore image info")
			}
			if !guiMode {
				vzkit.RunRunLoopOnce()
			}
			elapsed := time.Since(start).Truncate(100 * time.Millisecond)
			status := fmt.Sprintf("Fetching restore image URL from Apple... %v", elapsed)
			fmt.Printf("\r  %s %s", spinner[i%len(spinner)], status)
			progress(status, -1)
			i++
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}

	if restoreImage.ID == 0 {
		return fmt.Errorf("no restore image returned")
	}

	downloadURL := restoreImage.URL().AbsoluteString()
	buildVersion := restoreImage.BuildVersion()
	if buildVersion != "" {
		fmt.Printf("  Restore image: macOS (build %s)\n", buildVersion)
	}
	fmt.Printf("  Downloading: %s\n", downloadURL)
	fmt.Printf("  Saving to:   %s\n", destPath)
	fmt.Println()

	needBytes := getHTTPContentLength(ctx, downloadURL)
	if needBytes <= 0 {
		needBytes = 17 * 1024 * 1024 * 1024
	}
	if err := checkDiskSpace(filepath.Dir(destPath), needBytes); err != nil {
		return err
	}

	return downloadIPSW(ctx, downloadURL, destPath, progress)
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = srcFile.WriteTo(dstFile)
	return err
}

// fetchLatestRestoreImageObject fetches the latest restore image and returns it as an object.
func fetchLatestRestoreImageObject() (vz.VZMacOSRestoreImage, error) {
	var result vz.VZMacOSRestoreImage
	var fetchErr error
	done := make(chan struct{})

	start := time.Now()
	// Use generated completion handler binding
	vz.GetVZMacOSRestoreImageClass().FetchLatestSupportedWithCompletionHandler(func(img *vz.VZMacOSRestoreImage, err error) {
		if err != nil {
			fetchErr = err
		}
		if img != nil && img.ID != 0 {
			img.Retain()
			result = *img
		}
		close(done)
	})

	// Wait with spinner
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	for {
		select {
		case <-done:
			fmt.Printf("\r  ✓ Response received in %v          \n", time.Since(start).Truncate(time.Millisecond))
			if fetchErr != nil {
				errStr := fetchErr.Error()
				if strings.Contains(errStr, "10001") || strings.Contains(errStr, "catalog failed to load") {
					return vz.VZMacOSRestoreImage{}, fmt.Errorf("network fetch requires code signing. Run: go generate")
				}
				return vz.VZMacOSRestoreImage{}, fetchErr
			}
			if result.ID == 0 {
				return vz.VZMacOSRestoreImage{}, fmt.Errorf("no restore image returned")
			}
			return result, nil
		default:
			if time.Since(start) > 30*time.Second {
				fmt.Printf("\r  ✗ Timeout after 30s\n")
				return vz.VZMacOSRestoreImage{}, fmt.Errorf("timeout waiting for response")
			}
			vzkit.RunRunLoopOnce()
			elapsed := time.Since(start).Truncate(100 * time.Millisecond)
			fmt.Printf("\r  %s Contacting Apple servers... %v", spinner[i%len(spinner)], elapsed)
			i++
			time.Sleep(100 * time.Millisecond)
		}
	}
}
