package agentsandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestRunPassesGoogleModelOverrides(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "args.txt")
	writeBridge := func(rel string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$COVE_TEST_ARGS\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeBridge(filepath.Join("adapters", "google-bridge", "computer_use.py"))
	writeBridge(filepath.Join("adapters", "google-bridge", "vertex-ai", "computer_use.py"))
	t.Setenv("COVE_AGENT_SANDBOX_PYTHON", "/bin/sh")
	t.Setenv("COVE_TEST_ARGS", log)

	t.Setenv("COVE_GEMINI_MODEL", "gemini-model")
	if _, err := Run(context.Background(), Options{Provider: ProviderGemini, VMName: "vm", Task: "task", RepoRoot: root}); err != nil {
		t.Fatalf("gemini run: %v", err)
	}
	got := readFile(t, log)
	if !strings.Contains(got, "--model\ngemini-model\n") {
		t.Fatalf("gemini args:\n%s", got)
	}

	t.Setenv("GOOGLE_CLOUD_PROJECT", "project")
	t.Setenv("COVE_VERTEX_REGION", "region")
	t.Setenv("COVE_VERTEX_MODEL", "vertex-model")
	if _, err := Run(context.Background(), Options{Provider: ProviderVertex, VMName: "vm", Task: "task", RepoRoot: root}); err != nil {
		t.Fatalf("vertex run: %v", err)
	}
	got = readFile(t, log)
	for _, want := range []string{"--project\nproject\n", "--region\nregion\n", "--model\nvertex-model\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("vertex args missing %q:\n%s", want, got)
		}
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

func TestProviderMatrixMetadata(t *testing.T) {
	tests := []struct {
		name    string
		envVars []string
		notes   string
	}{
		{ProviderOpenAI, []string{"OPENAI_API_KEY"}, "OpenAI Agents SDK"},
		{ProviderAnthropic, []string{"ANTHROPIC_API_KEY"}, "cove runtime"},
		{ProviderGemini, []string{"GEMINI_API_KEY"}, ""},
		{ProviderVertex, []string{"GOOGLE_CLOUD_PROJECT or COVE_VERTEX_PROJECT", "COVE_VERTEX_REGION optional"}, ""},
	}
	infos := ProviderInfos()
	if len(infos) != len(tests) {
		t.Fatalf("ProviderInfos len = %d, want %d", len(infos), len(tests))
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ProviderNames()[i] != tt.name {
				t.Fatalf("ProviderNames[%d] = %q, want %q", i, ProviderNames()[i], tt.name)
			}
			p, err := LookupProvider("  " + strings.ToUpper(tt.name) + "  ")
			if err != nil {
				t.Fatal(err)
			}
			info := p.Info()
			if info.Name != tt.name || !info.FirstClass {
				t.Fatalf("Info = %+v, want first-class %q", info, tt.name)
			}
			if strings.Join(info.EnvVars, ",") != strings.Join(tt.envVars, ",") {
				t.Fatalf("EnvVars = %q, want %q", info.EnvVars, tt.envVars)
			}
			if tt.notes != "" && !strings.Contains(info.Notes, tt.notes) {
				t.Fatalf("Notes = %q, want substring %q", info.Notes, tt.notes)
			}
			if infos[i].Name != info.Name {
				t.Fatalf("ProviderInfos[%d] = %q, want %q", i, infos[i].Name, info.Name)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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

func TestRunValidationAndSentinels(t *testing.T) {
	tests := []struct {
		name   string
		run    func() error
		want   string
		wantIs error
	}{
		{
			name: "missing vm",
			run: func() error {
				_, err := Run(context.Background(), Options{Provider: ProviderAnthropic, Task: "task"})
				return err
			},
			want: "vm name required",
		},
		{
			name: "missing task",
			run: func() error {
				_, err := Run(context.Background(), Options{Provider: ProviderAnthropic, VMName: "vm"})
				return err
			},
			want: "task required",
		},
		{
			name: "unsupported provider",
			run: func() error {
				_, err := Run(context.Background(), Options{Provider: "bogus", VMName: "vm", Task: "task"})
				return err
			},
			want: `unsupported provider "bogus"`,
		},
		{
			name: "anthropic runtime sentinel",
			run: func() error {
				_, err := Run(context.Background(), Options{Provider: ProviderAnthropic, VMName: "vm", Task: "task"})
				return err
			},
			want:   "implemented by the cove runtime",
			wantIs: ErrNotSupported,
		},
		{
			name:   "generic stub sentinel",
			run:    func() error { _, err := (providerStub{}).Run(context.Background(), Options{}); return err },
			wantIs: ErrNotSupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("err = nil")
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want substring %q", err, tt.want)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tt.wantIs)
			}
		})
	}
}
