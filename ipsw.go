// IPSW download and restore image handling for macOS VMs.
//
// This file contains native NSURLSession-based download functionality
// for Apple IPSW restore images, with support for resumable downloads.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

// downloadIPSWCurl downloads the IPSW from the given URL to the specified path using curl.
// curl handles resumable downloads via -C flag.
func downloadIPSWCurl(urlStr, path string) error {
	var startSize int64
	// Check if file already exists and looks complete (zip EOCD signature present)
	if ipswLooksComplete(path) {
		info, _ := os.Stat(path)
		fmt.Printf("  Using existing IPSW: %.1f GB\n", float64(info.Size())/(1024*1024*1024))
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		startSize = info.Size()
		fmt.Printf("  Resuming partial download (%.1f GB already downloaded)\n", float64(startSize)/(1024*1024*1024))
	}

	// Get total size via HTTP HEAD request (for accurate progress)
	totalSize := getHTTPContentLength(urlStr)
	if totalSize <= 0 {
		totalSize = 17 * 1024 * 1024 * 1024 // fallback ~17 GB
	}

	fmt.Println("  Download is resumable — Ctrl+C to pause, run again to continue.")
	fmt.Println()

	// Start curl in background with silent mode (we'll show our own progress)
	cmd := exec.Command("curl", "-L", "-s", "-C", "-", "-o", path, urlStr)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start download: %w", err)
	}

	// Monitor progress by checking file size
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	start := time.Now()

	for {
		select {
		case err := <-done:
			fmt.Println() // Clear progress line
			if err != nil {
				return fmt.Errorf("download failed: %w", err)
			}
			// Verify download
			info, statErr := os.Stat(path)
			if statErr != nil {
				return fmt.Errorf("downloaded file not found: %w", statErr)
			}
			if info.Size() < 1*1024*1024*1024 {
				return fmt.Errorf("downloaded file too small (%.1f GB)", float64(info.Size())/(1024*1024*1024))
			}
			fmt.Printf("  Download complete: %.1f GB\n", float64(info.Size())/(1024*1024*1024))
			return nil

		default:
			// Check current file size
			info, err := os.Stat(path)
			currentSize := startSize
			if err == nil {
				currentSize = info.Size()
			}

			// Safety: if the file exceeds 120% of expected size, the server
			// likely ignored our Range header and restarted from scratch.
			// Kill curl to prevent unbounded growth.
			if totalSize > 0 && currentSize > totalSize*120/100 {
				cmd.Process.Kill()
				fmt.Printf("\n  Download exceeded expected size (%.1f GB > %.1f GB) — possible resume failure.\n",
					float64(currentSize)/(1024*1024*1024), float64(totalSize)/(1024*1024*1024))
				fmt.Println("  Removing corrupted file. Re-run to start a fresh download.")
				os.Remove(path)
				return fmt.Errorf("download overshot expected size; removed %s", path)
			}

			downloaded := currentSize - startSize
			elapsed := time.Since(start)

			if downloaded > 0 && elapsed.Seconds() > 1 {
				speed := float64(downloaded) / elapsed.Seconds() / (1024 * 1024) // MB/s
				pct := float64(currentSize) / float64(totalSize) * 100
				if pct > 100 {
					pct = 99.9
				}
				remaining := time.Duration(float64(totalSize-currentSize) / (float64(downloaded) / float64(elapsed)))
				printDownloadProgress(pct, currentSize, totalSize, speed, remaining)
			} else {
				fmt.Printf("\r\033[K  [                              ]   0.0%%")
			}

			time.Sleep(500 * time.Millisecond)
		}
	}
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
func getHTTPContentLength(urlStr string) int64 {
	cmd := exec.Command("curl", "-sI", "-L", urlStr)
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
				runRunLoopOnce()
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

	return downloadIPSWCurlWithProgress(downloadURL, destPath, progress)
}

// downloadIPSWCurlWithProgress is like downloadIPSWCurl but also reports
// progress via a callback for GUI display.
func downloadIPSWCurlWithProgress(urlStr, path string, progress progressFunc) error {
	var startSize int64
	if info, err := os.Stat(path); err == nil {
		if info.Size() > 10*1024*1024*1024 {
			fmt.Printf("  Using existing IPSW: %.1f GB\n", float64(info.Size())/(1024*1024*1024))
			progress("Using existing IPSW", 100)
			return nil
		}
		startSize = info.Size()
		fmt.Printf("  Resuming partial download (%.1f GB already downloaded)\n", float64(info.Size())/(1024*1024*1024))
	}

	totalSize := getHTTPContentLength(urlStr)
	if totalSize <= 0 {
		totalSize = 17 * 1024 * 1024 * 1024
	}

	fmt.Println("  Download is resumable — Ctrl+C to pause, run again to continue.")
	fmt.Println()

	cmd := exec.Command("curl", "-L", "-s", "-C", "-", "-o", path, urlStr)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start download: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	start := time.Now()

	for {
		select {
		case err := <-done:
			fmt.Println()
			if err != nil {
				return fmt.Errorf("download failed: %w", err)
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				return fmt.Errorf("downloaded file not found: %w", statErr)
			}
			if info.Size() < 1*1024*1024*1024 {
				return fmt.Errorf("downloaded file too small (%.1f GB)", float64(info.Size())/(1024*1024*1024))
			}
			fmt.Printf("  Download complete: %.1f GB\n", float64(info.Size())/(1024*1024*1024))
			progress("Download complete", 100)
			return nil

		default:
			info, err := os.Stat(path)
			currentSize := startSize
			if err == nil {
				currentSize = info.Size()
			}

			downloaded := currentSize - startSize
			elapsed := time.Since(start)

			if downloaded > 0 && elapsed.Seconds() > 1 {
				speed := float64(downloaded) / elapsed.Seconds() / (1024 * 1024)
				pct := float64(currentSize) / float64(totalSize) * 100
				if pct > 100 {
					pct = 99.9
				}
				remaining := time.Duration(float64(totalSize-currentSize) / (float64(downloaded) / float64(elapsed)))
				printDownloadProgress(pct, currentSize, totalSize, speed, remaining)
				// Update GUI with same info
				status := fmt.Sprintf("Downloading... %.1f%% (%.1f/%.1f GB) %.0f MB/s",
					pct,
					float64(currentSize)/(1024*1024*1024),
					float64(totalSize)/(1024*1024*1024),
					speed)
				progress(status, pct)
			} else {
				fmt.Printf("\r\033[K  [                              ]   0.0%%")
				progress("Starting download...", 0)
			}

			time.Sleep(500 * time.Millisecond)
		}
	}
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
			runRunLoopOnce()
			elapsed := time.Since(start).Truncate(100 * time.Millisecond)
			fmt.Printf("\r  %s Contacting Apple servers... %v", spinner[i%len(spinner)], elapsed)
			i++
			time.Sleep(100 * time.Millisecond)
		}
	}
}
