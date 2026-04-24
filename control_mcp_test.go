package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// decodeFrames reads newline-delimited JSON frames from r and returns them.
func decodeFrames(t *testing.T, r io.Reader) []jsonrpcResponse {
	t.Helper()
	var out []jsonrpcResponse
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("frame %q: %v", line, err)
		}
		out = append(out, resp)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

// runMCP feeds input (one request per line) through a fresh mcpServer and
// returns the collected response frames.
func runMCP(t *testing.T, vmDir, input string) []jsonrpcResponse {
	t.Helper()
	var out, logBuf bytes.Buffer
	s := &mcpServer{
		VMDir: vmDir,
		In:    strings.NewReader(input),
		Out:   &out,
		Log:   &logBuf,
	}
	if err := s.run(); err != nil {
		t.Fatalf("mcp run: %v", err)
	}
	return decodeFrames(t, &out)
}

func TestMCP_InitializeHandshake(t *testing.T) {
	dir := t.TempDir()
	frames := runMCP(t, dir, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d: %+v", len(frames), frames)
	}
	f := frames[0]
	if f.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", f.JSONRPC)
	}
	if string(f.ID) != "1" {
		t.Errorf("id = %s, want 1", f.ID)
	}
	if f.Error != nil {
		t.Fatalf("unexpected error: %+v", f.Error)
	}
	res, ok := f.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not an object: %+v", f.Result)
	}
	if res["protocolVersion"] != mcpProtocolVersion {
		t.Errorf("protocolVersion = %v, want %q", res["protocolVersion"], mcpProtocolVersion)
	}
	caps, _ := res["capabilities"].(map[string]any)
	if _, has := caps["tools"]; !has {
		t.Errorf("capabilities missing tools: %+v", caps)
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != "cove" {
		t.Errorf("serverInfo.name = %v, want cove", info["name"])
	}
}

func TestMCP_ToolsList(t *testing.T) {
	dir := t.TempDir()
	frames := runMCP(t, dir, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	res, _ := frames[0].Result.(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("no tools advertised")
	}

	// Assert every well-known tool is present. If the table changes, update
	// this list deliberately so we notice.
	seen := make(map[string]bool)
	for _, ti := range tools {
		tm, ok := ti.(map[string]any)
		if !ok {
			t.Errorf("tool entry not an object: %v", ti)
			continue
		}
		name, _ := tm["name"].(string)
		seen[name] = true
		if _, has := tm["description"]; !has {
			t.Errorf("tool %q missing description", name)
		}
		if _, has := tm["inputSchema"]; !has {
			t.Errorf("tool %q missing inputSchema", name)
		}
	}
	want := []string{
		"vm_list",
		"vm_status",
		"vm_pause",
		"vm_resume",
		"vm_stop",
		"vm_request_stop",
		"vm_screenshot",
		"vm_type",
		"vm_key",
		"vm_mouse",
		"vm_agent_exec",
		"vm_agent_read",
		"vm_agent_write",
		"vm_snapshot_save",
		"vm_snapshot_list",
		"vm_disk_snapshot_list",
		"vm_pit_snapshot_list",
		"vm_snapshot_restore",
		"vm_snapshot_delete",
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("tool %q not advertised (seen=%v)", w, seen)
		}
	}
}

func TestMCPPowerToolsBuildControlRequests(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		wantType string
	}{
		{name: "stop", toolName: "vm_stop", wantType: "stop"},
		{name: "request stop", toolName: "vm_request_stop", wantType: "request-stop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, ok := lookupMCPTool(tt.toolName)
			if !ok {
				t.Fatalf("lookupMCPTool(%q) not found", tt.toolName)
			}
			req, err := tool.build(json.RawMessage(`{"name":"dev"}`), "token")
			if err != nil {
				t.Fatalf("build() error = %v", err)
			}
			if req.Type != tt.wantType {
				t.Fatalf("ControlRequest.Type = %q, want %q", req.Type, tt.wantType)
			}
			if req.AuthToken != "token" {
				t.Fatalf("ControlRequest.AuthToken = %q, want token", req.AuthToken)
			}
		})
	}
}

func TestMCP_VMListEmpty(t *testing.T) {
	dir := t.TempDir()
	frames := runMCP(t, dir,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vm_list","arguments":{}}}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if frames[0].Error != nil {
		t.Fatalf("unexpected error: %+v", frames[0].Error)
	}
	res, _ := frames[0].Result.(map[string]any)
	if res["isError"] != false {
		t.Errorf("isError = %v, want false", res["isError"])
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1", len(content))
	}
	ci, _ := content[0].(map[string]any)
	text, _ := ci["text"].(string)
	// vm_list returns a JSON string with {"vms": [...]}; empty dir → empty list.
	var body struct {
		VMs []map[string]any `json:"vms"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("parse vm_list text %q: %v", text, err)
	}
	if len(body.VMs) != 0 {
		t.Errorf("vm_list = %+v, want empty", body.VMs)
	}
}

func TestMCP_UnknownTool(t *testing.T) {
	dir := t.TempDir()
	frames := runMCP(t, dir,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"does_not_exist","arguments":{}}}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if frames[0].Error == nil {
		t.Fatalf("want JSON-RPC error for unknown tool, got: %+v", frames[0].Result)
	}
	if frames[0].Error.Code != rpcErrMethodNotFound {
		t.Errorf("error code = %d, want %d", frames[0].Error.Code, rpcErrMethodNotFound)
	}
}

func TestMCP_UnknownMethod(t *testing.T) {
	dir := t.TempDir()
	frames := runMCP(t, dir, `{"jsonrpc":"2.0","id":5,"method":"not/real"}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if frames[0].Error == nil || frames[0].Error.Code != rpcErrMethodNotFound {
		t.Errorf("want method-not-found error, got %+v", frames[0])
	}
}

func TestMCP_ParseError(t *testing.T) {
	dir := t.TempDir()
	// Malformed JSON; expect a parse error frame with id=null.
	frames := runMCP(t, dir, "{not valid json\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if frames[0].Error == nil || frames[0].Error.Code != rpcErrParse {
		t.Errorf("want parse error, got %+v", frames[0])
	}
	if string(frames[0].ID) != "null" {
		t.Errorf("id = %s, want null on parse error", frames[0].ID)
	}
}

func TestMCP_WrongJSONRPCVersion(t *testing.T) {
	dir := t.TempDir()
	frames := runMCP(t, dir, `{"jsonrpc":"1.0","id":6,"method":"ping"}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if frames[0].Error == nil || frames[0].Error.Code != rpcErrInvalidRequest {
		t.Errorf("want invalid-request error, got %+v", frames[0])
	}
}

func TestMCP_NotificationNoReply(t *testing.T) {
	dir := t.TempDir()
	// No "id" field = notification. Initialized notification must not respond.
	frames := runMCP(t, dir,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
			`{"jsonrpc":"2.0","method":"initialized"}`+"\n")
	if len(frames) != 0 {
		t.Errorf("notifications produced %d frame(s): %+v", len(frames), frames)
	}
}

func TestMCP_Ping(t *testing.T) {
	dir := t.TempDir()
	frames := runMCP(t, dir, `{"jsonrpc":"2.0","id":7,"method":"ping"}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if frames[0].Error != nil {
		t.Errorf("ping returned error: %+v", frames[0].Error)
	}
}

func TestMCP_VMStatus_MissingVMName(t *testing.T) {
	dir := t.TempDir()
	// vm_status needs a "name" — omit it and expect an MCP tool error, not a
	// JSON-RPC -32xxx protocol error.
	frames := runMCP(t, dir,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"vm_status","arguments":{}}}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	if frames[0].Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", frames[0].Error)
	}
	res, _ := frames[0].Result.(map[string]any)
	if res["isError"] != true {
		t.Errorf("isError = %v, want true", res["isError"])
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatal("no content in tool error")
	}
	ci, _ := content[0].(map[string]any)
	msg, _ := ci["text"].(string)
	if !strings.Contains(msg, "name") {
		t.Errorf("error text %q should mention 'name'", msg)
	}
}

func TestMCP_VMStatus_NotRunning(t *testing.T) {
	dir := t.TempDir()
	// Valid name but VM dir doesn't exist → reachable-socket check fails.
	frames := runMCP(t, dir,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"vm_status","arguments":{"name":"ghost"}}}`+"\n")
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	res, _ := frames[0].Result.(map[string]any)
	if res["isError"] != true {
		t.Fatalf("want isError=true, got %+v", res)
	}
	content, _ := res["content"].([]any)
	ci, _ := content[0].(map[string]any)
	msg, _ := ci["text"].(string)
	if !strings.Contains(msg, "ghost") {
		t.Errorf("error text should mention vm name %q; got %q", "ghost", msg)
	}
}

func TestMCP_MultipleRequestsSingleSession(t *testing.T) {
	dir := t.TempDir()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vm_list","arguments":{}}}`,
	}, "\n") + "\n"
	frames := runMCP(t, dir, input)
	// initialize + tools/list + vm_list = 3 responses; notifications/initialized = 0.
	if len(frames) != 3 {
		t.Fatalf("want 3 frames, got %d: %+v", len(frames), frames)
	}
	var ids []string
	for _, f := range frames {
		ids = append(ids, string(f.ID))
	}
	want := []string{"1", "2", "3"}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("frame %d id = %s, want %s", i, ids[i], w)
		}
	}
}
