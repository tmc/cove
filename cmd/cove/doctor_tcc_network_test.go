package main

import (
	controlpb "github.com/tmc/cove/proto/controlpb"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestNetworkVolumePrompt(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		want       bool
	}{
		{"exact", "\"vz-agent\" would like to access files on a network volume. Don't Allow Allow", true},
		{"curly quotes", "“vz-agent” would like to access files on a network volume", true},
		{"OCR line breaks", "vz-agent would like to access\nfiles on a network volume", true},
		{"agent elsewhere", "vz-agent is connected. Terminal would like to access files on a network volume", false},
		{"another app", "Terminal would like to access files on a network volume", false},
		{"another permission", "vz-agent would like to access files in your Downloads folder", false},
		{"local network", "vz-agent would like to find devices on your local network", false},
		{"unrelated", "vz-agent clipboard ready Allow", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := networkVolumePrompt(tt.text); got != tt.want {
				t.Fatalf("got %t want %t", got, tt.want)
			}
		})
	}
}

func TestNetworkVolumeApprovalEnabled(t *testing.T) {
	directory := t.TempDir()
	if networkVolumeApprovalEnabled(directory) {
		t.Fatal("missing consent enabled")
	}
	for _, tt := range []struct {
		data string
		want bool
	}{
		{`{"vzAgent":true}`, true}, {`{"vzAgent":false}`, false}, {`{}`, false}, {`broken`, false},
	} {
		if err := os.WriteFile(filepath.Join(directory, networkVolumeApprovalFile), []byte(tt.data), 0600); err != nil {
			t.Fatal(err)
		}
		if got := networkVolumeApprovalEnabled(directory); got != tt.want {
			t.Fatalf("%s got %t", tt.data, got)
		}
	}
}

func TestNetworkVolumeAllowTarget(t *testing.T) {
	request := &controlpb.OCRMatch{Text: `"vz-agent" would like to access files on a network volume`, X: 0.35, Y: 0.3, Width: 0.3, Height: 0.06}
	decline := &controlpb.OCRMatch{Text: "Don't Allow", X: 0.38, Y: 0.42, Width: 0.1, Height: 0.03}
	allow := &controlpb.OCRMatch{Text: "Allow", X: 0.53, Y: 0.42, Width: 0.08, Height: 0.03}
	background := &controlpb.OCRMatch{Text: "Allow", X: 0.8, Y: 0.7, Width: 0.08, Height: 0.03}
	for _, tt := range []struct {
		name    string
		matches []*controlpb.OCRMatch
		want    bool
	}{
		{"specific request and exact button", []*controlpb.OCRMatch{request, decline, allow}, true},
		{"dont allow only", []*controlpb.OCRMatch{request, decline}, false},
		{"background allow only", []*controlpb.OCRMatch{request, decline, background}, false},
		{"no decline anchor", []*controlpb.OCRMatch{request, allow}, false},
		{"unrelated request", []*controlpb.OCRMatch{{Text: "Terminal would like to access files on a network volume", X: 0.35, Y: 0.3, Width: 0.3}, decline, allow}, false},
		{"ambiguous exact buttons", []*controlpb.OCRMatch{request, decline, allow, allow}, false},
		{"background button with valid dialog", []*controlpb.OCRMatch{request, decline, allow, background}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x, y, got := networkVolumeAllowTarget(tt.matches)
			if got != tt.want {
				t.Fatalf("got %t want %t", got, tt.want)
			}
			if got && (math.Abs(x-0.57) > 0.001 || math.Abs(y-0.435) > 0.001) {
				t.Fatalf("wrong normalized target %v,%v", x, y)
			}
		})
	}
}
