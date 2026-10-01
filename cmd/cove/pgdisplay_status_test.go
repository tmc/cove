//go:build darwin

package main

import (
	"encoding/json"
	"testing"

	"github.com/tmc/cove/internal/vmrun"
)

func TestDisplayStatusDoesNotObserveNativeObjects(t *testing.T) {
	var server *ControlServer
	response := server.handleDisplayStatus()
	if !response.Success || response.Error != "" {
		t.Fatalf("response = %v", response)
	}
	var status DisplayStatus
	if err := json.Unmarshal([]byte(response.Data), &status); err != nil {
		t.Fatal(err)
	}
	if status.Available || status.Reason == "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestDisplayStatusConfiguredGeometry(t *testing.T) {
	for _, tt := range []struct {
		name          string
		displays      []vmrun.DisplaySpec
		wantCount     int
		wantTruncated bool
	}{
		{name: "no inferred defaults"},
		{name: "explicit", displays: []vmrun.DisplaySpec{{Width: 1920, Height: 1080, PPI: 144}, {Width: 1024, Height: 768}}, wantCount: 2},
		{name: "bounded", displays: make([]vmrun.DisplaySpec, 17), wantCount: 16, wantTruncated: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := &ControlServer{runConfig: vmrun.RunConfig{Displays: tt.displays}}
			status := server.pgDisplayStatus()
			if status.Available || status.LiveMetricsAvailable || status.Reason == "" {
				t.Fatalf("claimed live observation: %+v", status)
			}
			if len(status.ConfiguredDisplays) != tt.wantCount || status.ConfiguredDisplaysTruncated != tt.wantTruncated {
				t.Fatalf("status = %+v", status)
			}
			wantSource := ""
			if tt.wantCount > 0 {
				wantSource = "run_config"
			}
			if status.ConfigurationSource != wantSource {
				t.Fatalf("source = %q", status.ConfigurationSource)
			}
			for i, got := range status.ConfiguredDisplays {
				want := tt.displays[i]
				if got.Width != want.Width || got.Height != want.Height || got.PixelsPerInch != want.PPI {
					t.Fatalf("display %d = %+v, want %+v", i, got, want)
				}
			}
			if tt.wantCount > 0 {
				before := status.ConfiguredDisplays[0]
				server.runConfig.Displays[0].Width++
				if status.ConfiguredDisplays[0] != before {
					t.Fatal("status aliases run configuration")
				}
			}
		})
	}
}
