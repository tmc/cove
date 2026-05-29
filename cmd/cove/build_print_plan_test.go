package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintBuildPlanCacheHitAndMiss(t *testing.T) {
	plan := buildPlan{
		Name:         "demo",
		Base:         "ghcr.io/acme/base",
		ParentDigest: "sha256:abc",
		Tags:         []string{"v1", "latest"},
		Steps: []buildPlanStep{
			{Name: "step-hit", Key: "sha256:k1", CacheHit: true, LayerDigest: "sha256:l1"},
			{Name: "step-miss", Key: "sha256:k2"},
		},
	}
	var out bytes.Buffer
	printBuildPlan(&out, plan, buildOptions{})
	got := out.String()
	for _, want := range []string{"cove build demo", "tag: v1", "tag: latest", "cache: hit (sha256:l1)", "cache: miss"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestPrintBuildPlanNoCacheDisablesAll(t *testing.T) {
	plan := buildPlan{
		Name:  "demo",
		Steps: []buildPlanStep{{Name: "s1", Key: "sha256:k"}},
	}
	var out bytes.Buffer
	printBuildPlan(&out, plan, buildOptions{NoCache: true})
	got := out.String()
	if !strings.Contains(got, "cache: disabled") {
		t.Fatalf("output missing 'cache: disabled':\n%s", got)
	}
}
