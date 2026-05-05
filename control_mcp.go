// control_mcp.go - Model Context Protocol (MCP) stdio transport.
//
// MCP is a JSON-RPC 2.0 protocol over newline-delimited JSON on stdin/stdout
// used by AI agents (Claude Code, Cursor, etc.) to invoke tools. This file
// exposes the same per-VM operations as control_http.go as MCP tools named
// vm_status, vm_pause, vm_screenshot, vm_agent_exec, and so on.
//
// Each tool call resolves a VM by name, dials its Unix control socket under
// ~/.vz/vms/<name>/control.sock, and proxies a ControlRequest through the
// same dispatch path the HTTP gateway uses. See control_http.go for the HTTP
// counterpart and docs/reference/http-api.md for operation semantics.
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// mcpProtocolVersion is the MCP protocol revision cove implements. Clients
// that declare an unknown version in initialize get this value back and are
// free to disconnect if they can't speak it.
const mcpProtocolVersion = "2024-11-05"

// jsonrpcRequest is a JSON-RPC 2.0 request frame. ID is a raw message so we
// echo it back verbatim (spec allows string, number, or null).
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response frame.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// jsonrpcError mirrors the JSON-RPC 2.0 error object.
type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC 2.0 standard error codes (plus -32002 for MCP tool-level failures).
const (
	rpcErrParse          = -32700
	rpcErrInvalidRequest = -32600
	rpcErrMethodNotFound = -32601
	rpcErrInvalidParams  = -32602
	rpcErrInternal       = -32603
)

// mcpServer holds MCP stdio server state. VMDir is the root (~/.vz/vms) used
// to resolve VM sockets; In/Out/Log are the framing streams (stdio by default).
type mcpServer struct {
	VMDir string
	In    io.Reader
	Out   io.Writer
	Log   io.Writer
}

// runMCPStdio runs the MCP server on stdin/stdout until EOF on stdin or a
// fatal framing error. Diagnostic output goes to stderr.
func runMCPStdio(vmDir string) error {
	s := &mcpServer{
		VMDir: vmDir,
		In:    os.Stdin,
		Out:   os.Stdout,
		Log:   os.Stderr,
	}
	return s.run()
}

// run reads JSON-RPC frames one line at a time, dispatches each, and writes
// the response frame on a single line. Notifications (no ID field) get no
// response. Returns nil on clean EOF.
func (s *mcpServer) run() error {
	scanner := bufio.NewScanner(s.In)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(nil, rpcErrParse, "parse error: "+err.Error())
			continue
		}
		if req.JSONRPC != "2.0" {
			s.writeError(req.ID, rpcErrInvalidRequest, `"jsonrpc" must be "2.0"`)
			continue
		}
		s.dispatch(&req)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("mcp stdin: %w", err)
	}
	return nil
}

// dispatch routes a single JSON-RPC request to its handler. It writes a
// response (or nothing, for notifications) and returns.
func (s *mcpServer) dispatch(req *jsonrpcRequest) {
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "initialized", "notifications/initialized":
		// Client signals it's ready. Nothing to do; don't reply to notifications.
		return
	case "ping":
		s.writeResult(req.ID, struct{}{})
	case "tools/list":
		s.writeResult(req.ID, map[string]any{"tools": mcpTools()})
	case "tools/call":
		s.handleToolsCall(req)
	case "shutdown", "exit":
		// Best-effort; client-initiated shutdown.
		if !isNotification {
			s.writeResult(req.ID, struct{}{})
		}
	default:
		if !isNotification {
			s.writeError(req.ID, rpcErrMethodNotFound, "method not found: "+req.Method)
		}
	}
}

// handleInitialize responds to the MCP handshake with server identity and a
// capabilities map advertising that we implement tools.
func (s *mcpServer) handleInitialize(req *jsonrpcRequest) {
	s.writeResult(req.ID, map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    "cove",
			"version": coveVersion(),
		},
	})
}

// handleToolsCall invokes the named tool with the provided arguments and
// encodes the ControlResponse (or an error) as MCP content items.
func (s *mcpServer) handleToolsCall(req *jsonrpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, rpcErrInvalidParams, "invalid params: "+err.Error())
		return
	}

	tool, ok := lookupMCPTool(params.Name)
	if !ok {
		s.writeError(req.ID, rpcErrMethodNotFound, "unknown tool: "+params.Name)
		return
	}

	// Tools that don't require a VM (e.g. vm_list) dispatch locally.
	if tool.local != nil {
		result, err := tool.local(s, params.Arguments)
		if err != nil {
			s.writeToolError(req.ID, err.Error())
			return
		}
		s.writeToolResult(req.ID, result)
		return
	}

	// VM-scoped tools: parse {name: ..., ...} then dispatch a ControlRequest.
	var base struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(params.Arguments, &base)
	if base.Name == "" {
		s.writeToolError(req.ID, `missing required argument: "name"`)
		return
	}

	sockPath := filepath.Join(s.VMDir, base.Name, "control.sock")
	if !socketIsReachable(sockPath) {
		s.writeToolError(req.ID, fmt.Sprintf(`vm %q not found or not running`, base.Name))
		return
	}

	tokenPath := filepath.Join(s.VMDir, base.Name, controlTokenFileName)
	token, _ := LoadControlTokenFromPath(tokenPath)

	creq, err := tool.build(params.Arguments, token)
	if err != nil {
		s.writeToolError(req.ID, err.Error())
		return
	}

	resp, err := dialAndSend(sockPath, creq)
	if err != nil {
		s.writeToolError(req.ID, err.Error())
		return
	}
	if resp.Error != "" {
		s.writeToolError(req.ID, resp.Error)
		return
	}

	// Screenshot is special: return image content instead of text.
	if sr := resp.GetScreenshotResult(); sr != nil && len(sr.GetImageData()) > 0 {
		b64 := base64.StdEncoding.EncodeToString(sr.GetImageData())
		mime := "image/png"
		if sr.Format == "jpeg" {
			mime = "image/jpeg"
		}
		s.writeToolContent(req.ID, []map[string]any{
			{"type": "image", "data": b64, "mimeType": mime},
		})
		return
	}

	data, err := protojsonMarshaler.Marshal(resp)
	if err != nil {
		s.writeToolError(req.ID, "marshal response: "+err.Error())
		return
	}
	s.writeToolResult(req.ID, string(data))
}

// writeResult encodes a successful JSON-RPC response on one line.
func (s *mcpServer) writeResult(id json.RawMessage, result any) {
	s.writeFrame(jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

// writeError encodes a JSON-RPC error response.
func (s *mcpServer) writeError(id json.RawMessage, code int, msg string) {
	if id == nil {
		// Notifications must not receive responses, but parse errors come
		// through with a nil ID per spec — write with JSON null.
		id = json.RawMessage("null")
	}
	s.writeFrame(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: msg},
	})
}

// writeToolResult wraps plain text as an MCP content item and responds.
func (s *mcpServer) writeToolResult(id json.RawMessage, text string) {
	s.writeToolContent(id, []map[string]any{{"type": "text", "text": text}})
}

// writeToolContent sends arbitrary MCP content blocks back to the caller.
func (s *mcpServer) writeToolContent(id json.RawMessage, content []map[string]any) {
	s.writeResult(id, map[string]any{
		"content": content,
		"isError": false,
	})
}

// writeToolError reports a tool-call failure as an MCP tool error (not a
// JSON-RPC protocol error). The RPC call succeeds but result.isError is true.
func (s *mcpServer) writeToolError(id json.RawMessage, msg string) {
	s.writeResult(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	})
}

// writeFrame marshals a response and writes it as one newline-terminated line.
func (s *mcpServer) writeFrame(resp jsonrpcResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(s.Log, "mcp: marshal response: %v\n", err)
		return
	}
	data = append(data, '\n')
	if _, err := s.Out.Write(data); err != nil {
		fmt.Fprintf(s.Log, "mcp: write: %v\n", err)
	}
}

// dialAndSend connects to a VM control socket, writes one ControlRequest JSON
// line, and reads back one ControlResponse. The 30s deadline covers slow
// guest-agent calls; screenshots return in milliseconds.
func dialAndSend(sockPath string, req *controlpb.ControlRequest) (*controlpb.ControlResponse, error) {
	reqBytes, err := protojsonMarshaler.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	conn, err := net.DialTimeout("unix", sockPath, 300*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", sockPath, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprintf(conn, "%s\n", reqBytes); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return nil, fmt.Errorf("read response: %w", scanErr)
		}
		return nil, errors.New("vm closed connection without response")
	}
	var resp controlpb.ControlResponse
	if err := protojsonUnmarshaler.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
}

// mcpTool defines one MCP tool: a name, human description, JSONSchema for the
// arguments, and either a local handler (vm_list) or a ControlRequest builder
// (everything else).
type mcpTool struct {
	Name        string
	Description string
	Schema      map[string]any
	// build constructs a ControlRequest from tool arguments; token is the
	// per-VM auth token to stamp on the request. Used for VM-scoped tools.
	build func(args json.RawMessage, token string) (*controlpb.ControlRequest, error)
	// local handles tools that don't require a VM (vm_list). Either build
	// or local is set, never both.
	local func(s *mcpServer, args json.RawMessage) (string, error)
}

// mcpTools returns the MCP tool descriptors in a stable order for tools/list.
func mcpTools() []map[string]any {
	out := make([]map[string]any, 0, len(mcpToolTable))
	for _, t := range mcpToolTable {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.Schema,
		})
	}
	return out
}

// lookupMCPTool returns the tool with the given name, if any.
func lookupMCPTool(name string) (*mcpTool, bool) {
	for i := range mcpToolTable {
		if mcpToolTable[i].Name == name {
			return &mcpToolTable[i], true
		}
	}
	return nil, false
}

// schemaVMName returns a JSONSchema fragment for the required "name" field.
func schemaVMName() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "VM name (directory under ~/.vz/vms)",
	}
}

// objectSchema builds a JSONSchema object with the given properties and required fields.
func objectSchema(props map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// vmArgs unmarshals a tool-call argument blob into v. Used by each build func.
func vmArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	return json.Unmarshal(args, v)
}

func mcpVMDirectory(root string, args json.RawMessage) (string, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := vmArgs(args, &in); err != nil {
		return "", err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", fmt.Errorf(`missing required argument: "name"`)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid vm name %q", name)
	}
	dir := filepath.Join(root, name)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(`vm %q not found`, name)
		}
		return "", fmt.Errorf("stat vm %q: %w", name, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("vm %q is not a directory", name)
	}
	return dir, nil
}

// mcpToolTable lists every tool exposed over MCP. Keep this in lockstep with
// the HTTP routes in control_http.go so the two transports stay symmetric.
var mcpToolTable = []mcpTool{
	{
		Name:        "vm_list",
		Description: "List VMs that have a reachable control socket.",
		Schema:      objectSchema(nil),
		local: func(s *mcpServer, _ json.RawMessage) (string, error) {
			entries, err := os.ReadDir(s.VMDir)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", s.VMDir, err)
			}
			type vmEntry struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			}
			var vms []vmEntry
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				sock := filepath.Join(s.VMDir, e.Name(), "control.sock")
				if !socketIsReachable(sock) {
					continue
				}
				vms = append(vms, vmEntry{Name: e.Name(), Status: "running"})
			}
			data, err := json.Marshal(map[string]any{"vms": vms})
			return string(data), err
		},
	},
	{
		Name:        "vm_status",
		Description: "Report lifecycle state and capabilities of a running VM.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			return &controlpb.ControlRequest{Type: "status", AuthToken: token}, nil
		},
	},
	{
		Name:        "vm_pause",
		Description: "Pause a running VM.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			return &controlpb.ControlRequest{Type: "pause", AuthToken: token}, nil
		},
	},
	{
		Name:        "vm_resume",
		Description: "Resume a paused VM.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			return &controlpb.ControlRequest{Type: "resume", AuthToken: token}, nil
		},
	},
	{
		Name:        "vm_stop",
		Description: "Force-stop a running VM.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			return &controlpb.ControlRequest{Type: "stop", AuthToken: token}, nil
		},
	},
	{
		Name:        "vm_request_stop",
		Description: "Request graceful guest shutdown with an ACPI power button event.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			return &controlpb.ControlRequest{Type: "request-stop", AuthToken: token}, nil
		},
	},
	{
		Name:        "vm_screenshot",
		Description: "Capture a PNG of the VM display. Returns image content.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			return &controlpb.ControlRequest{
				Type:      "screenshot",
				AuthToken: token,
				Command:   &controlpb.ControlRequest_Screenshot{Screenshot: &controlpb.ScreenshotCommand{}},
			}, nil
		},
	},
	{
		Name:        "vm_type",
		Description: "Type a string into the VM via synthesized keyboard events.",
		Schema: objectSchema(map[string]any{
			"name": schemaVMName(),
			"text": map[string]any{"type": "string", "description": "Text to type."},
		}, "name", "text"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Text string `json:"text"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			return &controlpb.ControlRequest{
				Type:      "text",
				AuthToken: token,
				Command:   &controlpb.ControlRequest_Text{Text: &controlpb.TextCommand{Text: in.Text}},
			}, nil
		},
	},
	{
		Name:        "vm_key",
		Description: "Send one keyboard event. Codes are macOS HIToolbox virtual key codes.",
		Schema: objectSchema(map[string]any{
			"name":      schemaVMName(),
			"code":      map[string]any{"type": "integer", "description": "Virtual key code (e.g. 36 for Return)."},
			"modifiers": map[string]any{"type": "integer", "description": "Modifier bitmap (Shift=0x20000, Ctrl=0x40000, Option=0x80000, Cmd=0x100000)."},
			"key_down":  map[string]any{"type": "boolean", "description": "true for keydown (default), false for keyup only."},
		}, "name", "code"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Code      uint32 `json:"code"`
				Modifiers uint32 `json:"modifiers"`
				KeyDown   *bool  `json:"key_down"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			down := true
			if in.KeyDown != nil {
				down = *in.KeyDown
			}
			return &controlpb.ControlRequest{
				Type:      "key",
				AuthToken: token,
				Command: &controlpb.ControlRequest_Key{Key: &controlpb.KeyCommand{
					KeyCode:   in.Code,
					Modifiers: in.Modifiers,
					KeyDown:   down,
				}},
			}, nil
		},
	},
	{
		Name:        "vm_mouse",
		Description: "Send one mouse event at normalized (x,y) coordinates.",
		Schema: objectSchema(map[string]any{
			"name":   schemaVMName(),
			"x":      map[string]any{"type": "number", "description": "Normalized X coordinate (0.0-1.0)."},
			"y":      map[string]any{"type": "number", "description": "Normalized Y coordinate (0.0-1.0)."},
			"button": map[string]any{"type": "integer", "description": "0=left, 1=right, 2=middle. Defaults to 0."},
			"action": map[string]any{"type": "string", "enum": []string{"click", "down", "up", "move"}, "description": "Mouse action."},
		}, "name", "x", "y"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				X      float64 `json:"x"`
				Y      float64 `json:"y"`
				Button int32   `json:"button"`
				Action string  `json:"action"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			if in.Action == "" {
				in.Action = "click"
			}
			return &controlpb.ControlRequest{
				Type:      "mouse",
				AuthToken: token,
				Command: &controlpb.ControlRequest_Mouse{Mouse: &controlpb.MouseCommand{
					X:      in.X,
					Y:      in.Y,
					Button: in.Button,
					Action: in.Action,
				}},
			}, nil
		},
	},
	{
		Name:        "vm_agent_exec",
		Description: "Run a command inside the guest via the path-aware vz-agent route and return exit code, stdout, and stderr.",
		Schema: objectSchema(map[string]any{
			"name": schemaVMName(),
			"cmd":  map[string]any{"type": "string", "description": "Executable to run inside the guest."},
			"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Arguments to cmd."},
		}, "name", "cmd"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Cmd  string   `json:"cmd"`
				Args []string `json:"args"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			full := in.Args
			if in.Cmd != "" {
				full = append([]string{in.Cmd}, in.Args...)
			}
			return &controlpb.ControlRequest{
				Type:      "agent-exec-auto",
				AuthToken: token,
				Command:   &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: full}},
			}, nil
		},
	},
	{
		Name:        "vm_agent_read",
		Description: "Read a file from inside the guest.",
		Schema: objectSchema(map[string]any{
			"name": schemaVMName(),
			"path": map[string]any{"type": "string", "description": "Absolute path inside the guest."},
		}, "name", "path"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			return &controlpb.ControlRequest{
				Type:      "agent-read",
				AuthToken: token,
				Command:   &controlpb.ControlRequest_AgentRead{AgentRead: &controlpb.AgentFileReadCommand{Path: in.Path}},
			}, nil
		},
	},
	{
		Name:        "vm_agent_write",
		Description: "Write a file inside the guest. data is base64-encoded bytes.",
		Schema: objectSchema(map[string]any{
			"name": schemaVMName(),
			"path": map[string]any{"type": "string"},
			"data": map[string]any{"type": "string", "description": "Base64-encoded file contents."},
			"mode": map[string]any{"type": "integer", "description": "Unix file mode (e.g. 420 for 0644). Optional."},
		}, "name", "path", "data"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Path string `json:"path"`
				Data string `json:"data"`
				Mode uint32 `json:"mode"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			return &controlpb.ControlRequest{
				Type:      "agent-write",
				AuthToken: token,
				Command: &controlpb.ControlRequest_AgentWrite{AgentWrite: &controlpb.AgentFileWriteCommand{
					Path: in.Path, Data: in.Data, Mode: in.Mode,
				}},
			}, nil
		},
	},
	{
		Name:        "vm_snapshot_save",
		Description: "Save a VM state snapshot with the given name.",
		Schema: objectSchema(map[string]any{
			"name":     schemaVMName(),
			"snapshot": map[string]any{"type": "string", "description": "Snapshot name."},
		}, "name", "snapshot"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Snapshot string `json:"snapshot"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			return &controlpb.ControlRequest{
				Type:      "snapshot",
				AuthToken: token,
				Command: &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{
					Action: "save", Name: in.Snapshot,
				}},
			}, nil
		},
	},
	{
		Name:        "vm_snapshot_list",
		Description: "List saved VM state snapshots.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			return &controlpb.ControlRequest{
				Type:      "snapshot",
				AuthToken: token,
				Command:   &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{Action: "list"}},
			}, nil
		},
	},
	{
		Name:        "vm_disk_snapshot_list",
		Description: "List disk-level snapshots for a VM without modifying it.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		local: func(s *mcpServer, args json.RawMessage) (string, error) {
			dir, err := mcpVMDirectory(s.VMDir, args)
			if err != nil {
				return "", err
			}
			snapshots, err := NewDiskSnapshotManager(dir).List()
			if err != nil {
				return "", err
			}
			data, err := json.Marshal(map[string]any{"snapshots": snapshots})
			return string(data), err
		},
	},
	{
		Name:        "vm_pit_snapshot_list",
		Description: "List point-in-time snapshots for a VM without modifying it.",
		Schema:      objectSchema(map[string]any{"name": schemaVMName()}, "name"),
		local: func(s *mcpServer, args json.RawMessage) (string, error) {
			dir, err := mcpVMDirectory(s.VMDir, args)
			if err != nil {
				return "", err
			}
			snapshots, err := NewPITSnapshotManager(dir).List()
			if err != nil {
				return "", err
			}
			data, err := json.Marshal(map[string]any{"snapshots": snapshots})
			return string(data), err
		},
	},
	{
		Name:        "vm_snapshot_restore",
		Description: "Restore a VM from a previously saved state snapshot.",
		Schema: objectSchema(map[string]any{
			"name":     schemaVMName(),
			"snapshot": map[string]any{"type": "string"},
		}, "name", "snapshot"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Snapshot string `json:"snapshot"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			return &controlpb.ControlRequest{
				Type:      "snapshot",
				AuthToken: token,
				Command: &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{
					Action: "restore", Name: in.Snapshot,
				}},
			}, nil
		},
	},
	{
		Name:        "vm_snapshot_delete",
		Description: "Delete a VM state snapshot.",
		Schema: objectSchema(map[string]any{
			"name":     schemaVMName(),
			"snapshot": map[string]any{"type": "string"},
		}, "name", "snapshot"),
		build: func(args json.RawMessage, token string) (*controlpb.ControlRequest, error) {
			var in struct {
				Snapshot string `json:"snapshot"`
			}
			if err := vmArgs(args, &in); err != nil {
				return nil, err
			}
			return &controlpb.ControlRequest{
				Type:      "snapshot",
				AuthToken: token,
				Command: &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{
					Action: "delete", Name: in.Snapshot,
				}},
			}, nil
		},
	},
}

// coveVersion returns a best-effort version string for MCP server identity.
func coveVersion() string {
	return hostVersion()
}
