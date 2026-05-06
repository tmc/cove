package agentsandbox

import (
	"context"
	"errors"
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

func TestRunOpenAIBridgeRequiresScript(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Provider: ProviderOpenAI,
		VMName:   "vm",
		Task:     "task",
		RepoRoot: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "provider script") {
		t.Fatalf("Run error = %v, want missing provider script", err)
	}
}

func TestRunAnthropicDelegatesToRuntime(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Provider: ProviderAnthropic,
		VMName:   "vm",
		Task:     "task",
	})
	if !errors.Is(err, ErrNotSupported) || !strings.Contains(err.Error(), "implemented by the cove runtime") {
		t.Fatalf("Run error = %v, want runtime delegation error", err)
	}
}

func TestProviderInfos(t *testing.T) {
	infos := ProviderInfos()
	if len(infos) != 4 {
		t.Fatalf("ProviderInfos len = %d, want 4", len(infos))
	}
	want := []string{ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderVertex}
	for i, info := range infos {
		if info.Name != want[i] {
			t.Fatalf("ProviderInfos[%d].Name = %q, want %q", i, info.Name, want[i])
		}
		if !info.Capabilities.Screenshot || !info.Capabilities.Click || !info.Capabilities.Type || !info.Capabilities.Scroll || !info.Capabilities.Wait {
			t.Fatalf("%s capabilities incomplete: %+v", info.Name, info.Capabilities)
		}
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
