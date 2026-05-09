package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSecretLikeCacheEnvName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: false},
		{name: "whitespace-only", in: "   ", want: false},
		{name: "plain", in: "PATH", want: false},
		{name: "exact TOKEN", in: "TOKEN", want: true},
		{name: "exact PASSWORD", in: "password", want: true},
		{name: "exact SECRET lowercase", in: "secret", want: true},
		{name: "exact KEY", in: "key", want: true},
		{name: "suffix _TOKEN", in: "GITHUB_TOKEN", want: true},
		{name: "suffix _password", in: "DB_password", want: true},
		{name: "suffix _SECRET trimmed", in: "  CLIENT_SECRET  ", want: true},
		{name: "suffix _KEY", in: "OPENAI_KEY", want: true},
		{name: "contains but no underscore", in: "TOKENIZER", want: false},
		{name: "no underscore separator", in: "MYTOKEN", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := secretLikeCacheEnvName(tt.in)
			if got != tt.want {
				t.Fatalf("secretLikeCacheEnvName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestPrintBuildWarnings(t *testing.T) {
	tests := []struct {
		name     string
		plan     buildPlan
		wants    []string
		notWants []string
	}{
		{
			name: "no steps no warnings",
			plan: buildPlan{},
		},
		{
			name: "clean cache-env emits nothing",
			plan: buildPlan{Steps: []buildPlanStep{{
				Name: "build",
				Meta: buildScriptMeta{CacheEnv: []string{"PATH", "HOME"}},
			}}},
		},
		{
			name: "secret-like cache-env emits warning",
			plan: buildPlan{Steps: []buildPlanStep{{
				Name: "build",
				Meta: buildScriptMeta{CacheEnv: []string{"GITHUB_TOKEN"}},
			}}},
			wants: []string{
				"warning: ",
				"step \"build\"",
				"GITHUB_TOKEN",
				"looks secret",
			},
		},
		{
			name: "secret + compact:fast emits second warning",
			plan: buildPlan{Steps: []buildPlanStep{{
				Name: "deploy",
				Meta: buildScriptMeta{
					Secrets: []string{"DEPLOY_KEY"},
					Compact: "fast",
				},
			}}},
			wants: []string{
				"step \"deploy\"",
				"# secret: with # compact: fast",
				"plaintext",
			},
		},
		{
			name: "non-fast compact does not warn",
			plan: buildPlan{Steps: []buildPlanStep{{
				Name: "deploy",
				Meta: buildScriptMeta{
					Secrets: []string{"DEPLOY_KEY"},
					Compact: "full",
				},
			}}},
			notWants: []string{"plaintext"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printBuildWarnings(&buf, tt.plan)
			out := buf.String()
			if len(tt.wants) == 0 && len(tt.notWants) == 0 && out != "" {
				t.Fatalf("expected empty output, got %q", out)
			}
			for _, w := range tt.wants {
				if !strings.Contains(out, w) {
					t.Fatalf("output %q missing %q", out, w)
				}
			}
			for _, w := range tt.notWants {
				if strings.Contains(out, w) {
					t.Fatalf("output %q unexpectedly contains %q", out, w)
				}
			}
		})
	}
}
