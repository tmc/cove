package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ipswDownloadOptions struct {
	attempts     int
	stallSeconds int
	retryDelay   time.Duration
}

func downloadIPSW(ctx context.Context, url, path string, progress progressFunc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ipswLooksComplete(path) {
		fmt.Println("  Using existing IPSW:", path)
		if progress != nil {
			progress("Using existing IPSW", 100)
		}
		return nil
	}
	total := getHTTPContentLength(ctx, url)
	opts := ipswDownloadOptions{attempts: 4, stallSeconds: 30, retryDelay: time.Second}
	if err := downloadIPSWTransfer(ctx, url, path, total, progress, opts); err != nil {
		return err
	}
	if err := verifyIPSWFile(path); err != nil {
		return err
	}
	fmt.Println("\n  Download complete:", path)
	if progress != nil {
		progress("Download complete", 100)
	}
	return nil
}

func downloadIPSWTransfer(ctx context.Context, url, path string, total int64, progress progressFunc, opts ipswDownloadOptions) error {
	if opts.attempts < 1 || opts.stallSeconds < 1 {
		return fmt.Errorf("invalid download retry options")
	}
	var lastErr error
	for attempt := 0; attempt < opts.attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			status := fmt.Sprintf("Download interrupted; resuming (attempt %d/%d)", attempt+1, opts.attempts)
			fmt.Println("\n ", status)
			if progress != nil {
				progress(status, -1)
			}
			timer := time.NewTimer(opts.retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		retry, err := downloadIPSWAttempt(ctx, url, path, total, progress, opts.stallSeconds)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			return err
		}
	}
	return fmt.Errorf("download failed after %d attempts; partial file retained at %s: %w", opts.attempts, path, lastErr)
}

func downloadIPSWAttempt(ctx context.Context, url, path string, total int64, progress progressFunc, stallSeconds int) (bool, error) {
	startSize := downloadFileSize(path)
	cmd := exec.CommandContext(ctx, "curl", "--location", "--silent", "--show-error", "--fail",
		"--connect-timeout", "15", "--speed-time", strconv.Itoa(stallSeconds), "--speed-limit", "1024",
		"--continue-at", "-", "--output", path, "--write-out", "%{http_code}", url)
	var stderr, status bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &status
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("start download: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			return false, ctx.Err()
		case err := <-done:
			if err == nil {
				return false, nil
			}
			code, _ := strconv.Atoi(strings.TrimSpace(status.String()))
			return retryIPSWDownload(err, code), fmt.Errorf("download: %w: %s", err, strings.TrimSpace(stderr.String()))
		case <-ticker.C:
			current := downloadFileSize(path)
			speed := float64(current-startSize) / time.Since(start).Seconds()
			pct := -1.0
			message := fmt.Sprintf("Downloading... %.1f GB", float64(current)/(1<<30))
			if total > 0 {
				pct = min(99.9, 100*float64(current)/float64(total))
				remaining := time.Duration(0)
				if speed > 0 && total > current {
					remaining = time.Duration(float64(total-current)/speed) * time.Second
				}
				printDownloadProgress(pct, current, total, speed/(1<<20), remaining)
				message = fmt.Sprintf("Downloading... %.1f%% (%.1f/%.1f GB)", pct, float64(current)/(1<<30), float64(total)/(1<<30))
			} else {
				fmt.Printf("\r\033[K  %s", message)
			}
			if progress != nil {
				progress(message, pct)
			}
		}
	}
}

func downloadFileSize(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

func retryIPSWDownload(err error, status int) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	switch exit.ExitCode() {
	case 5, 6, 7, 16, 18, 28, 52, 55, 56, 92:
		return true
	case 22:
		return status == 408 || status == 429 || status >= 500 && status <= 599
	}
	return false
}
