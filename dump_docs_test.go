package main

import (
	"strings"
	"testing"
)

func TestBuildDumpDocsAll(t *testing.T) {
	docs, err := buildDumpDocs("all")
	if err != nil {
		t.Fatalf("buildDumpDocs(all): %v", err)
	}
	if docs.CLI == nil || docs.API == nil {
		t.Fatalf("expected cli/api docs, got %#v", docs)
	}
	if docs.Version == "" {
		t.Fatal("expected version in docs bundle")
	}
	if got := cliCommandByName(docs.CLI.Commands, "dump-docs"); got == nil {
		t.Fatal("missing dump-docs CLI command")
	} else if got.Usage == "" {
		t.Fatal("dump-docs usage is empty")
	}
	if got := apiEndpointByPath(docs.API.Endpoints, "POST", "/v1/vms/{name}/snapshot"); got == nil {
		t.Fatal("missing snapshot API endpoint")
	}
}

func TestBuildDumpDocsTypeSelector(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		wantCLI bool
		wantAPI bool
		wantMCP bool
		wantErr bool
	}{
		{name: "cli", kind: "cli", wantCLI: true},
		{name: "api", kind: "api", wantAPI: true},
		{name: "mcp", kind: "mcp", wantErr: true},
		{name: "bad", kind: "wat", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs, err := buildDumpDocs(tt.kind)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("buildDumpDocs(%q) = nil error, want error", tt.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildDumpDocs(%q): %v", tt.kind, err)
			}
			if got := docs.CLI != nil; got != tt.wantCLI {
				t.Fatalf("CLI present = %v, want %v", got, tt.wantCLI)
			}
			if got := docs.API != nil; got != tt.wantAPI {
				t.Fatalf("API present = %v, want %v", got, tt.wantAPI)
			}
			if got := docs.MCP != nil; got != tt.wantMCP {
				t.Fatalf("MCP present = %v, want %v", got, tt.wantMCP)
			}
		})
	}
}

func TestBuildCLIDocsIncludesCapturedUsage(t *testing.T) {
	docs := buildCLIDocs()
	if docs.Overview == "" {
		t.Fatal("expected non-empty CLI overview")
	}
	ctl := cliCommandByName(docs.Commands, "ctl")
	if ctl == nil {
		t.Fatal("missing ctl command")
	}
	if ctl.Usage == "" {
		t.Fatal("ctl usage is empty")
	}
	if want := "Usage: cove ctl"; !strings.Contains(ctl.Usage, want) {
		t.Fatalf("ctl usage missing %q", want)
	}
}

func TestBuildMCPDocsUnavailableByDefault(t *testing.T) {
	if docs := buildMCPDocs(); docs != nil {
		t.Fatalf("buildMCPDocs() = %#v, want nil", docs)
	}
}

func cliCommandByName(commands []cliCommandDoc, name string) *cliCommandDoc {
	for i := range commands {
		if commands[i].Name == name {
			return &commands[i]
		}
	}
	return nil
}

func apiEndpointByPath(endpoints []apiEndpointDoc, method, path string) *apiEndpointDoc {
	for i := range endpoints {
		if endpoints[i].Method == method && endpoints[i].Path == path {
			return &endpoints[i]
		}
	}
	return nil
}
