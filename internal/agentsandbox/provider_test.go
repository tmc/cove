package agentsandbox

import (
	"context"
	"strings"
	"testing"
)

func TestRunRejectsUnsupportedProvider(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Provider: "bogus",
		VMName:   "vm",
		Task:     "task",
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported provider "bogus"`) {
		t.Fatalf("Run error = %v, want unsupported provider", err)
	}
}

func TestRunOpenAIStub(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Provider: ProviderOpenAI,
		VMName:   "vm",
		Task:     "task",
	})
	if err == nil || !strings.Contains(err.Error(), "openai provider is not implemented yet") {
		t.Fatalf("Run error = %v, want openai stub error", err)
	}
}

func TestRunRequiresVMAndTask(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "vm",
			opts: Options{Provider: ProviderAnthropic, Task: "task"},
			want: "vm name required",
		},
		{
			name: "task",
			opts: Options{Provider: ProviderAnthropic, VMName: "vm"},
			want: "task required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run error = %v, want %q", err, tc.want)
			}
		})
	}
}
