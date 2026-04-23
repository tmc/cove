package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

func init() {
	if len(os.Args) < 2 || os.Args[1] != "dump-docs" {
		return
	}
	if err := runDumpDocsCommand(os.Args[2:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

type dumpDocsBundle struct {
	Version string   `json:"version,omitempty"`
	CLI     *cliDocs `json:"cli,omitempty"`
	API     *apiDocs `json:"api,omitempty"`
	MCP     *mcpDocs `json:"mcp,omitempty"`
}

type cliDocs struct {
	Overview string          `json:"overview,omitempty"`
	Commands []cliCommandDoc `json:"commands"`
}

type cliCommandDoc struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Aliases []string `json:"aliases,omitempty"`
	Usage   string   `json:"usage,omitempty"`
}

type apiDocs struct {
	Endpoints []apiEndpointDoc `json:"endpoints"`
}

type apiEndpointDoc struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Auth        string `json:"auth"`
}

type mcpDocs struct {
	ProtocolVersion string       `json:"protocol_version"`
	Tools           []mcpToolDoc `json:"tools"`
}

type mcpToolDoc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

func runDumpDocsCommand(args []string) error {
	fs := flag.NewFlagSet("dump-docs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kind := fs.String("type", "all", "docs type: all, cli, api, or mcp")
	pretty := fs.Bool("pretty", false, "pretty-print JSON output")
	fs.Usage = func() {
		printDumpDocsUsage(os.Stderr, fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	docs, err := buildDumpDocs(*kind)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(docs)
}

func printDumpDocsUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `Usage: cove dump-docs [options]

Emit machine-readable JSON for cove's CLI, HTTP API, and any compiled MCP tool surface.

Options:
`)
	fs.PrintDefaults()
	fmt.Fprintf(w, `
Examples:
  cove dump-docs
  cove dump-docs -type cli -pretty
  cove dump-docs -type mcp
`)
}

func buildDumpDocs(kind string) (*dumpDocsBundle, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "all"
	}

	docs := &dumpDocsBundle{Version: hostVersion()}
	switch kind {
	case "all":
		docs.CLI = buildCLIDocs()
		docs.API = buildAPIDocs()
		docs.MCP = buildMCPDocs()
	case "cli":
		docs.CLI = buildCLIDocs()
	case "api", "http":
		docs.API = buildAPIDocs()
	case "mcp":
		docs.MCP = buildMCPDocs()
		if docs.MCP == nil {
			return nil, fmt.Errorf("mcp docs unavailable in this build")
		}
	default:
		return nil, fmt.Errorf("unknown docs type %q (want all, cli, api, or mcp)", kind)
	}
	return docs, nil
}

func buildCLIDocs() *cliDocs {
	docs := &cliDocs{
		Overview: captureCommandStderr(usage),
		Commands: make([]cliCommandDoc, 0, len(cliDocSpecs)),
	}
	for _, spec := range cliDocSpecs {
		doc := cliCommandDoc{
			Name:    spec.Name,
			Summary: spec.Summary,
			Aliases: spec.Aliases,
			Usage:   strings.TrimSpace(spec.Usage()),
		}
		docs.Commands = append(docs.Commands, doc)
	}
	return docs
}

func buildAPIDocs() *apiDocs {
	return &apiDocs{
		Endpoints: []apiEndpointDoc{
			{Method: "GET", Path: "/healthz", Description: "Health check.", Auth: "none"},
			{Method: "GET", Path: "/v1/vms/{name}/status", Description: "Report lifecycle state and capabilities of one VM.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/pause", Description: "Pause a running VM.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/resume", Description: "Resume a paused VM.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/stop", Description: "Stop a running VM.", Auth: "bearer"},
			{Method: "GET", Path: "/v1/vms/{name}/screenshot", Description: "Capture the VM display as an image.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/type", Description: "Type text into the guest with synthesized keyboard input.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/key", Description: "Send one keyboard event.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/mouse", Description: "Send one mouse event.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/agent/exec", Description: "Run a guest command through vz-agent.", Auth: "bearer"},
			{Method: "GET", Path: "/v1/vms/{name}/agent/read", Description: "Read a file from the guest.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/agent/write", Description: "Write a file into the guest.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/agent/cp", Description: "Copy a file between host and guest.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/snapshot", Description: "Save a VM state snapshot.", Auth: "bearer"},
			{Method: "GET", Path: "/v1/vms/{name}/snapshots", Description: "List VM state snapshots.", Auth: "bearer"},
			{Method: "POST", Path: "/v1/vms/{name}/snapshots/{snap}/restore", Description: "Restore a VM state snapshot.", Auth: "bearer"},
			{Method: "DELETE", Path: "/v1/vms/{name}/snapshots/{snap}", Description: "Delete a VM state snapshot.", Auth: "bearer"},
			{Method: "GET", Path: "/v1/vms/{name}/events", Description: "Subscribe to per-VM event stream via SSE.", Auth: "bearer"},
			{Method: "GET", Path: "/v1/operations", Description: "List known long-running operations.", Auth: "bearer"},
			{Method: "GET", Path: "/v1/operations/{id}", Description: "Get one long-running operation.", Auth: "bearer"},
			{Method: "GET", Path: "/v1/operations/{id}/events", Description: "Subscribe to one operation's SSE stream.", Auth: "bearer"},
		},
	}
}

func buildMCPDocs() *mcpDocs {
	tools := make([]mcpToolDoc, 0, len(mcpToolTable))
	for _, tool := range mcpToolTable {
		tools = append(tools, mcpToolDoc{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: cloneMap(tool.Schema),
		})
	}
	return &mcpDocs{
		ProtocolVersion: mcpProtocolVersion,
		Tools:           tools,
	}
}

type cliDocSpec struct {
	Name    string
	Summary string
	Aliases []string
	Usage   func() string
}

var cliDocSpecs = []cliDocSpec{
	{Name: "up", Summary: "Install, provision, and boot a VM in one command.", Usage: func() string {
		fs, _, _ := newUpFlagSet()
		return captureWriter(func(w io.Writer) { printUpUsage(w, fs) })
	}},
	{Name: "install", Summary: "Install an operating system into a VM directory.", Usage: captureInstallUsage},
	{Name: "run", Summary: "Boot the selected VM.", Usage: captureRunUsage},
	{Name: "list", Summary: "List installed VMs and templates.", Usage: captureListUsage},
	{Name: "clean", Summary: "Remove per-VM artifacts while keeping the directory.", Usage: captureCleanUsage},
	{Name: "provision", Summary: "Write provisioning files into the VM disk.", Aliases: []string{"inject"}, Usage: func() string {
		fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
		return captureWriter(func(w io.Writer) { printInjectUsage(w, fs) })
	}},
	{Name: "provision-agent", Summary: "Provision vz-agent into a VM.", Aliases: []string{"inject-agent"}, Usage: func() string {
		return captureWriter(printProvisionAgentUsage)
	}},
	{Name: "agent-upgrade", Summary: "Live-upgrade vz-agent inside a running VM.", Aliases: []string{"upgrade-agent"}, Usage: func() string {
		return captureWriter(printAgentUpgradeUsage)
	}},
	{Name: "verify", Summary: "Diagnose provisioning, agent, and file ownership health.", Aliases: []string{"doctor"}, Usage: func() string {
		fs, _, _ := newVerifyFlagSet()
		return captureWriter(func(w io.Writer) { printVerifyUsage(w, fs) })
	}},
	{Name: "sip", Summary: "Manage System Integrity Protection and recovery automation.", Usage: func() string { return "" }},
	{Name: "vm", Summary: "Manage VM selection, export/import, and metadata.", Usage: func() string {
		return captureWriter(printVMUsage)
	}},
	{Name: "clone", Summary: "Clone a VM.", Usage: captureCloneUsage},
	{Name: "gc", Summary: "Delete old disposable clones and cached artifacts.", Usage: func() string {
		return captureWriter(printGCUsage)
	}},
	{Name: "template", Summary: "Manage VM templates.", Usage: func() string {
		return captureWriter(printTemplateUsage)
	}},
	{Name: "shared-folder", Summary: "Manage shared folders and guest mounts.", Aliases: []string{"shared-folders"}, Usage: func() string {
		return captureWriter(printSharedFolderUsage)
	}},
	{Name: "snapshot", Summary: "Manage VM state snapshots.", Usage: func() string {
		return captureWriter(printSnapshotUsage)
	}},
	{Name: "pit", Summary: "Manage experimental point-in-time snapshots.", Usage: func() string {
		return captureWriter(printPITUsageHelp)
	}},
	{Name: "disk-snapshot", Summary: "Manage APFS copy-on-write disk snapshots.", Usage: func() string {
		return captureWriter(printDiskSnapshotUsageHelp)
	}},
	{Name: "serve", Summary: "Run the multi-VM HTTP and MCP gateway.", Usage: func() string {
		return captureCommandStderr(printServeUsage)
	}},
	{Name: "ctl", Summary: "Drive a running VM through the control socket.", Usage: func() string {
		fs, _, _, _, _, _, _ := newCtlFlagSet()
		return captureWriter(func(w io.Writer) { printCtlUsage(w, fs) })
	}},
	{Name: "vzscript", Summary: "Run guest-agent and UI automation scripts.", Usage: func() string {
		return captureWriter(printVzscriptUsage)
	}},
	{Name: "network", Summary: "Show networking modes and host interfaces.", Usage: func() string {
		return strings.TrimSpace(NetworkModeHelp())
	}},
	{Name: "rosetta", Summary: "Manage Rosetta support for Linux guests.", Usage: func() string {
		return strings.TrimSpace(RosettaHelp())
	}},
	{Name: "helper", Summary: "Manage the privileged host helper daemon.", Usage: func() string {
		return captureCommandStdout(func() { _ = helperUsage() })
	}},
	{Name: "disk-detach", Summary: "Detach a VM disk left mounted on the host.", Usage: captureDiskDetachUsage},
	{Name: "dump-docs", Summary: "Emit machine-readable CLI, HTTP API, and MCP docs as JSON.", Usage: func() string {
		fs := flag.NewFlagSet("dump-docs", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return captureWriter(func(w io.Writer) { printDumpDocsUsage(w, fs) })
	}},
	{Name: "version", Summary: "Print version information.", Usage: func() string { return "" }},
	{Name: "help", Summary: "Show top-level or command-specific help.", Usage: func() string { return "" }},
}

func captureInstallUsage() string    { return captureWriter(printInstallUsage) }
func captureRunUsage() string        { return captureWriter(printRunUsage) }
func captureListUsage() string       { return captureWriter(printListUsage) }
func captureCleanUsage() string      { return captureWriter(printCleanUsage) }
func captureCloneUsage() string      { return captureWriter(printCloneUsage) }
func captureDiskDetachUsage() string { return captureWriter(printDiskDetachUsage) }

var captureMu sync.Mutex

func captureWriter(fn func(io.Writer)) string {
	var buf bytes.Buffer
	fn(&buf)
	return strings.TrimSpace(buf.String())
}

func captureCommandStdout(fn func()) string {
	return captureCommandOSFile(&os.Stdout, fn)
}

func captureCommandStderr(fn func()) string {
	return captureCommandOSFile(&os.Stderr, fn)
}

func captureCommandOSFile(target **os.File, fn func()) string {
	captureMu.Lock()
	defer captureMu.Unlock()

	r, w, err := os.Pipe()
	if err != nil {
		return ""
	}
	defer r.Close()

	old := *target
	*target = w
	defer func() {
		*target = old
	}()

	fn()
	_ = w.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		return cloneMap(vv)
	case []string:
		out := make([]string, len(vv))
		copy(out, vv)
		return out
	case []any:
		out := make([]any, len(vv))
		for i := range vv {
			out[i] = cloneValue(vv[i])
		}
		return out
	default:
		return v
	}
}
