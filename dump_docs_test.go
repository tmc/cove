package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestBuildDumpDocsAll(t *testing.T) {
	docs, err := buildDumpDocs("all")
	if err != nil {
		t.Fatalf("buildDumpDocs(all): %v", err)
	}
	if docs.CLI == nil || docs.API == nil || docs.MCP == nil {
		t.Fatalf("expected cli/api/mcp docs, got %#v", docs)
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
	if got := apiEndpointByPath(docs.API.Endpoints, "POST", "/v1/vms/{name}/request-stop"); got == nil {
		t.Fatal("missing request-stop API endpoint")
	}
	if got := apiEndpointByPath(docs.API.Endpoints, "GET", "/v1/vms/{name}/disk-snapshots"); got == nil {
		t.Fatal("missing disk snapshot API endpoint")
	}
	if got := apiEndpointByPath(docs.API.Endpoints, "GET", "/v1/vms/{name}/pit-snapshots"); got == nil {
		t.Fatal("missing PIT snapshot API endpoint")
	}
	if got := apiEndpointByPath(docs.API.Endpoints, "GET", "/v1/vms"); got == nil {
		t.Fatal("missing VM list API endpoint")
	}
	if got := apiEndpointByPath(docs.API.Endpoints, "POST", "/v1/vms"); got == nil {
		t.Fatal("missing VM create API endpoint")
	}
	if got := mcpToolByName(docs.MCP.Tools, "vm_snapshot_save"); got == nil {
		t.Fatal("missing vm_snapshot_save MCP tool")
	}
	if got := mcpToolByName(docs.MCP.Tools, "vm_disk_snapshot_list"); got == nil {
		t.Fatal("missing vm_disk_snapshot_list MCP tool")
	}
	if got := mcpToolByName(docs.MCP.Tools, "vm_pit_snapshot_list"); got == nil {
		t.Fatal("missing vm_pit_snapshot_list MCP tool")
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
		{name: "mcp", kind: "mcp", wantMCP: true},
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
	compact := cliCommandByName(docs.Commands, "compact")
	if compact == nil {
		t.Fatal("missing compact command")
	}
	if want := "Usage: cove compact"; !strings.Contains(compact.Usage, want) {
		t.Fatalf("compact usage missing %q", want)
	}
	push := cliCommandByName(docs.Commands, "push")
	if push == nil {
		t.Fatal("missing push command")
	}
	if want := "Usage: cove push"; !strings.Contains(push.Usage, want) {
		t.Fatalf("push usage missing %q", want)
	}
	if got := cliFlagByName(push.Flags, "--base"); got == nil {
		t.Fatal("push docs missing --base flag")
	}
	if got := cliFlagByName(push.Flags, "--additional-tag"); got == nil {
		t.Fatal("push docs missing --additional-tag flag")
	} else if !got.Repeatable {
		t.Fatal("push --additional-tag flag is not marked repeatable")
	}
	if len(push.Examples) == 0 {
		t.Fatal("push docs missing examples")
	}
	pull := cliCommandByName(docs.Commands, "pull")
	if pull == nil {
		t.Fatal("missing pull command")
	}
	if want := "Usage: cove pull"; !strings.Contains(pull.Usage, want) {
		t.Fatalf("pull usage missing %q", want)
	}
	if got := cliFlagByName(pull.Flags, "--manifest"); got == nil {
		t.Fatal("pull docs missing --manifest flag")
	}
	if len(pull.Examples) == 0 {
		t.Fatal("pull docs missing examples")
	}
}

func TestBuildCLIDocsDoesNotWriteStderr(t *testing.T) {
	got := captureStderrForTest(func() {
		_ = buildCLIDocs()
	})
	if got != "" {
		t.Fatalf("buildCLIDocs wrote stderr: %q", got)
	}
}

func TestBuildMCPDocsIncludesSchemas(t *testing.T) {
	docs := buildMCPDocs()
	if docs.ProtocolVersion != mcpProtocolVersion {
		t.Fatalf("protocol version = %q, want %q", docs.ProtocolVersion, mcpProtocolVersion)
	}
	for _, name := range []string{"vm_pause", "vm_resume", "vm_request_stop", "vm_snapshot_save", "vm_disk_snapshot_list", "vm_pit_snapshot_list"} {
		tool := mcpToolByName(docs.Tools, name)
		if tool == nil {
			t.Fatalf("missing MCP tool %q", name)
		}
		if tool.InputSchema == nil {
			t.Fatalf("MCP tool %q input schema is nil", name)
		}
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

func captureStderrForTest(fn func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		return ""
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return string(data)
}

func cliFlagByName(flags []cliFlagDoc, name string) *cliFlagDoc {
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i]
		}
	}
	return nil
}

func mcpToolByName(tools []mcpToolDoc, name string) *mcpToolDoc {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}
