package guibench_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/tmc/cove/internal/guibench"
)

// ExampleMetrics_urlMatchNormalized scores a Safari navigation that picked up
// tracking query params against the gold URL: the normalized match ignores the
// query, fragment, scheme, and a leading "www.", so a correct navigation still
// verifies.
func ExampleMetrics_urlMatchNormalized() {
	m := guibench.Metrics()["url_match_normalized"]
	score, _ := m(
		"https://www.apple.com/mac/?utm_source=newsletter#top",
		"https://apple.com/mac",
		nil,
	)
	fmt.Printf("%.0f\n", score)
	// Output: 1
}

// ExampleMetrics_pdfContains scores already-extracted PDF text under fuzzy
// normalization, so a PDF export's reflowed whitespace does not defeat a
// correct content match.
//
// The getter extracts the text guest-side before this pure metric scores it,
// e.g. an exec getter running a one-shot PDFKit script:
//
//	osascript -l JavaScript -e 'ObjC.import("PDFKit"); ObjC.import("Foundation");
//	  $.PDFDocument.alloc.initWithURL($.NSURL.fileURLWithPath("/path/out.pdf")).string.js'
//
// or `mdimport -d2` / a bundled `pdftotext`; the metric is agnostic to how the
// text was extracted.
func ExampleMetrics_pdfContains() {
	m := guibench.Metrics()["pdf_contains"]
	extracted := "Invoice\n\n  Total   Due:   $1,200.00\n"
	score, _ := m(extracted, "total due: $1,200.00", nil)
	fmt.Printf("%.0f\n", score)
	// Output: 1
}

// ExampleComposite scores a task whose success spans two artifacts read by two
// different getters: an exported image (image_similar) and a settings value
// (exact_match). "and" requires both to be right.
func ExampleComposite() {
	// Stand in for the agent's exported file and the gold image with one solid
	// red PNG (in a real task the getter reads the file off the guest).
	red := solidRedPNG()

	comp := guibench.Composite{
		Conj: "and",
		Checks: []guibench.Check{
			{
				Func:    guibench.StringList{"image_similar"},
				Result:  guibench.GetterSpec{Kind: "file", Path: "/Users/tmc/Desktop/out.png"},
				Options: map[string]any{"expected": red},
			},
			{
				Func:    guibench.StringList{"exact_match"},
				Result:  guibench.GetterSpec{Kind: "defaults", Domain: "-g", Key: "AppleInterfaceStyle"},
				Options: map[string]any{"expected": "Dark"},
			},
		},
	}

	probe := guibench.FakeProbe{
		Files: map[string]string{"/Users/tmc/Desktop/out.png": red},
		Commands: map[string]guibench.ExecResult{
			"defaults read -g AppleInterfaceStyle": {Stdout: "Dark\n"},
		},
	}
	score, err := comp.Evaluate(probe, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%.0f\n", score)
	// Output: 1
}

// solidRedPNG returns a tiny solid-red PNG, generated in-process so no binary
// fixture is committed.
func solidRedPNG() string {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.String()
}
