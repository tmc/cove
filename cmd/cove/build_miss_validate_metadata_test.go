package main

import (
	"strings"
	"testing"
)

func TestValidateBuildCacheStepMetadata(t *testing.T) {
	good := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name    string
		step    buildPlanStep
		wantSub string
	}{
		{"emptyParent", buildPlanStep{}, "digest"},
		{"badParentDigest", buildPlanStep{ParentDigest: "not-a-digest", ScriptDigest: good, AgentProtocolVersion: "v1"}, "digest"},
		{"badScriptDigest", buildPlanStep{ParentDigest: good, ScriptDigest: "bad", AgentProtocolVersion: "v1"}, "digest"},
		{"emptyAgentVersion", buildPlanStep{ParentDigest: good, ScriptDigest: good}, "agent protocol version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBuildCacheStepMetadata(tt.step)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want %q", err, tt.wantSub)
			}
		})
	}
}
