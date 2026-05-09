package agent

import (
	"reflect"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestCloneConfigNil(t *testing.T) {
	if got := CloneConfig(nil); got != nil {
		t.Errorf("CloneConfig(nil) = %v, want nil", got)
	}
}

func TestCloneConfigCopiesScalars(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	src := &vmconfig.AgentConfig{
		Platform:   "linux",
		Requested:  true,
		Verified:   true,
		VerifiedAt: now,
		Source:     SourceRuntime,
		Version:    "v1.2.3",
		Commit:     "deadbeef",
		Features:   []string{"a", "b"},
	}
	clone := CloneConfig(src)
	if clone == src {
		t.Fatal("CloneConfig returned same pointer; want a fresh copy")
	}
	if !reflect.DeepEqual(clone, src) {
		t.Errorf("clone fields differ from source: %+v vs %+v", *clone, *src)
	}

	// Mutating scalar fields on the clone must not affect the source.
	clone.Platform = "macos"
	clone.Requested = false
	clone.Version = "v9.9.9"
	if src.Platform != "linux" || !src.Requested || src.Version != "v1.2.3" {
		t.Errorf("scalar mutation on clone leaked to source: %+v", *src)
	}
}

func TestCloneConfigSharesFeaturesSlice(t *testing.T) {
	// CloneConfig is documented as a shallow copy, so the Features slice
	// header is copied but the backing array is shared. Pin that contract.
	src := &vmconfig.AgentConfig{Features: []string{"x", "y"}}
	clone := CloneConfig(src)
	clone.Features[0] = "mutated"
	if src.Features[0] != "mutated" {
		t.Errorf("shallow-copy contract broken: src.Features[0] = %q, want mutated", src.Features[0])
	}
}
