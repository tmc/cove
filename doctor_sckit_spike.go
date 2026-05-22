package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tmc/apple/screencapturekit"
	"github.com/tmc/cove/internal/sckit"
)

// runSCKitSpike implements `cove doctor sckit-spike`. It is a design 041
// Slice 2 spike: capture the cove VM window 20 times via SCScreenshotManager,
// report p50/p95/min/max latency, save the last frame for sanity check,
// and exit 0 iff median < 50ms (design 041 Q3 threshold).
func runSCKitSpike(args []string) error {
	fs := flag.NewFlagSet("doctor sckit-spike", flag.ContinueOnError)
	titlePrefix := fs.String("title-prefix", "macOS VM", "match window title prefix")
	owner := fs.String("owner", "cove", "match window's owning application name")
	iterations := fs.Int("n", 20, "capture iterations")
	threshold := fs.Duration("threshold", 50*time.Millisecond, "median latency budget")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	content, err := screencapturekit.GetSCShareableContentClass().GetShareableContentExcludingDesktopWindowsOnScreenWindowsOnly(ctx, true, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shareable content: %v\n", err)
		fmt.Fprintln(os.Stderr, "hint: grant Screen Recording in System Settings > Privacy & Security")
		os.Exit(2)
	}

	var windowID uint32
	var matched string
	for _, w := range content.Windows() {
		title := w.Title()
		appName := ""
		if app := w.OwningApplication(); app != nil {
			appName = app.ApplicationName()
		}
		titleHit := *titlePrefix != "" && strings.HasPrefix(title, *titlePrefix)
		ownerHit := *owner != "" && strings.EqualFold(appName, *owner)
		if titleHit || ownerHit {
			windowID = w.WindowID()
			matched = fmt.Sprintf("id=%d title=%q owner=%q", windowID, title, appName)
			break
		}
	}
	if windowID == 0 {
		return fmt.Errorf("no window matched title-prefix=%q or owner=%q (is cove running with -gui?)", *titlePrefix, *owner)
	}
	fmt.Printf("matched window: %s\n", matched)

	samples := make([]time.Duration, 0, *iterations)
	lastImg, _, _ := sckit.CaptureSpike(ctx, windowID) // warm-up; discard timing & error
	_ = lastImg
	for i := 0; i < *iterations; i++ {
		img, dur, err := sckit.CaptureSpike(ctx, windowID)
		if err != nil {
			return fmt.Errorf("iteration %d: %w", i, err)
		}
		samples = append(samples, dur)
		lastImg = img
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	min := samples[0]
	max := samples[len(samples)-1]
	p50 := samples[len(samples)/2]
	p95 := samples[(len(samples)*95)/100]
	fmt.Printf("iterations=%d min=%s p50=%s p95=%s max=%s threshold=%s\n",
		len(samples), min, p50, p95, max, *threshold)

	out := fmt.Sprintf("/tmp/sckit-spike-%d.png", time.Now().Unix())
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	defer f.Close()
	if err := png.Encode(f, lastImg); err != nil {
		return fmt.Errorf("encode %s: %w", out, err)
	}
	fmt.Printf("saved last frame: %s\n", out)

	if p50 >= *threshold {
		return fmt.Errorf("p50=%s exceeds threshold=%s", p50, *threshold)
	}
	fmt.Println("OK: p50 within threshold")
	return nil
}
