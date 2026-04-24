package main

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// NewHTTPHandler returns an http.Handler exposing a ControlServer over HTTP.
// The handler accepts requests under /v1/ and dispatches to the underlying
// ControlServer.handleRequest. For per-VM mode (cove run -http), pass the
// per-VM ControlServer + its auth token. The vmName parameter is the VM
// identifier exposed in URL paths (e.g. /v1/vms/<vmName>/status). Operations
// registry may be nil (per-VM mode); in gateway mode, pass a shared registry.
func NewHTTPHandler(cs *ControlServer, vmName string, authToken string, ops *OperationRegistry) http.Handler {
	h := &httpHandler{
		cs:        cs,
		vmName:    vmName,
		authToken: authToken,
		ops:       ops,
	}
	return h.buildMux()
}

type httpHandler struct {
	cs        *ControlServer
	vmName    string
	authToken string
	ops       *OperationRegistry
}

func (h *httpHandler) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	// No-auth health check.
	mux.HandleFunc("GET /healthz", h.handleHealthz)

	// Lifecycle.
	mux.HandleFunc("GET /v1/vms/{name}/status", h.auth(h.handleStatus))
	mux.HandleFunc("POST /v1/vms/{name}/pause", h.auth(h.handlePause))
	mux.HandleFunc("POST /v1/vms/{name}/resume", h.auth(h.handleResume))
	mux.HandleFunc("POST /v1/vms/{name}/stop", h.auth(h.handleStop))
	mux.HandleFunc("POST /v1/vms/{name}/request-stop", h.auth(h.handleRequestStop))
	mux.HandleFunc("GET /v1/vms/{name}/screenshot", h.auth(h.handleScreenshot))
	mux.HandleFunc("POST /v1/vms/{name}/type", h.auth(h.handleType))
	mux.HandleFunc("POST /v1/vms/{name}/key", h.auth(h.handleKey))
	mux.HandleFunc("POST /v1/vms/{name}/mouse", h.auth(h.handleMouse))

	// Guest agent.
	mux.HandleFunc("POST /v1/vms/{name}/agent/exec", h.auth(h.handleAgentExec))
	mux.HandleFunc("GET /v1/vms/{name}/agent/read", h.auth(h.handleAgentRead))
	mux.HandleFunc("POST /v1/vms/{name}/agent/write", h.auth(h.handleAgentWrite))
	mux.HandleFunc("POST /v1/vms/{name}/agent/cp", h.auth(h.handleAgentCp))

	// Snapshots.
	mux.HandleFunc("POST /v1/vms/{name}/snapshot", h.auth(h.handleSnapshotSave))
	mux.HandleFunc("GET /v1/vms/{name}/snapshots", h.auth(h.handleSnapshotList))
	mux.HandleFunc("POST /v1/vms/{name}/snapshots/{snap}/restore", h.auth(h.handleSnapshotRestore))
	mux.HandleFunc("DELETE /v1/vms/{name}/snapshots/{snap}", h.auth(h.handleSnapshotDelete))
	mux.HandleFunc("GET /v1/vms/{name}/disk-snapshots", h.auth(h.handleDiskSnapshotList))
	mux.HandleFunc("GET /v1/vms/{name}/pit-snapshots", h.auth(h.handlePITSnapshotList))

	// Events SSE.
	mux.HandleFunc("GET /v1/vms/{name}/events", h.auth(h.handleEvents))

	// Operations (only if registry is provided).
	if h.ops != nil {
		mux.HandleFunc("GET /v1/operations/{id}/events", h.auth(h.handleOperationEvents))
		mux.HandleFunc("GET /v1/operations/{id}", h.auth(h.handleOperationGet))
		mux.HandleFunc("GET /v1/operations", h.auth(h.handleOperationList))
	}

	return mux
}

// auth wraps a handler with Bearer token authentication.
func (h *httpHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.checkAuth(r) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (h *httpHandler) checkAuth(r *http.Request) bool {
	if h.authToken == "" {
		return true
	}
	hdr := r.Header.Get("Authorization")
	token := strings.TrimPrefix(hdr, "Bearer ")
	token = strings.TrimSpace(token)
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.authToken)) == 1
}

// checkVMName validates the {name} path value matches h.vmName.
func (h *httpHandler) checkVMName(w http.ResponseWriter, r *http.Request) bool {
	name := r.PathValue("name")
	if name != h.vmName {
		writeJSONError(w, http.StatusNotFound, "not_found")
		return false
	}
	return true
}

// dispatch sends a ControlRequest and writes the response as JSON.
// Returns (resp, true) on success; writes error and returns (nil, false) on failure.
func (h *httpHandler) dispatch(w http.ResponseWriter, req *controlpb.ControlRequest) (*controlpb.ControlResponse, bool) {
	resp := h.cs.handleRequest(req)
	if resp.Error != "" {
		writeJSONError(w, http.StatusInternalServerError, resp.Error)
		return nil, false
	}
	return resp, true
}

func (h *httpHandler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`)) //nolint
}

func (h *httpHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{Type: "status"})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handlePause(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{Type: "pause"})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleResume(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{Type: "resume"})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleStop(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{Type: "stop"})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleRequestStop(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{Type: "request-stop"})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type:    "screenshot",
		Command: &controlpb.ControlRequest_Screenshot{Screenshot: &controlpb.ScreenshotCommand{}},
	})
	if !ok {
		return
	}
	sr := resp.GetScreenshotResult()
	if sr != nil && len(sr.ImageData) > 0 {
		ct := "image/png"
		if sr.Format == "jpeg" {
			ct = "image/jpeg"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		w.Write(sr.ImageData) //nolint
		return
	}
	// Legacy path: resp.Data contains base64 PNG.
	if resp.Data != "" {
		img, err := base64.StdEncoding.DecodeString(resp.Data)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "screenshot decode error")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(img) //nolint
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "no screenshot data")
}

func (h *httpHandler) handleType(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type:    "text",
		Command: &controlpb.ControlRequest_Text{Text: &controlpb.TextCommand{Text: body.Text}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleKey(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	var body struct {
		Code      uint32 `json:"code"`
		Modifiers uint32 `json:"modifiers"`
		KeyDown   bool   `json:"key_down"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type: "key",
		Command: &controlpb.ControlRequest_Key{Key: &controlpb.KeyCommand{
			KeyCode:   body.Code,
			Modifiers: body.Modifiers,
			KeyDown:   body.KeyDown,
		}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleMouse(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	var body struct {
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Button int32   `json:"button"`
		Action string  `json:"action"`
		Click  bool    `json:"click"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	action := body.Action
	if action == "" && body.Click {
		action = "click"
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type: "mouse",
		Command: &controlpb.ControlRequest_Mouse{Mouse: &controlpb.MouseCommand{
			X:      body.X,
			Y:      body.Y,
			Button: body.Button,
			Action: action,
		}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleAgentExec(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	var body struct {
		Cmd  string   `json:"cmd"`
		Args []string `json:"args"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	args := body.Args
	if body.Cmd != "" {
		args = append([]string{body.Cmd}, args...)
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type:    "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: args}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleAgentRead(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeJSONError(w, http.StatusBadRequest, "path query parameter required")
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type:    "agent-read",
		Command: &controlpb.ControlRequest_AgentRead{AgentRead: &controlpb.AgentFileReadCommand{Path: path}},
	})
	if !ok {
		return
	}
	af := resp.GetAgentFile()
	if af != nil && len(af.Data) > 0 {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(af.Data) //nolint
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleAgentWrite(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	var body struct {
		Path string `json:"path"`
		Data string `json:"data"` // base64
		Mode uint32 `json:"mode"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type: "agent-write",
		Command: &controlpb.ControlRequest_AgentWrite{AgentWrite: &controlpb.AgentFileWriteCommand{
			Path: body.Path,
			Data: body.Data,
			Mode: body.Mode,
		}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleAgentCp(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	var body struct {
		Src     string `json:"src"`
		Dst     string `json:"dst"`
		ToGuest bool   `json:"to_guest"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{AgentCp: &controlpb.AgentCopyCommand{
			HostPath:  body.Src,
			GuestPath: body.Dst,
			ToGuest:   body.ToGuest,
		}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleSnapshotSave(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{
			Action: "save",
			Name:   body.Name,
		}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleSnapshotList(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type:    "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{Action: "list"}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	snap := r.PathValue("snap")
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{
			Action: "restore",
			Name:   snap,
		}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	snap := r.PathValue("snap")
	resp, ok := h.dispatch(w, &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{Snapshot: &controlpb.SnapshotCommand{
			Action: "delete",
			Name:   snap,
		}},
	})
	if !ok {
		return
	}
	writeProtoJSON(w, resp)
}

func (h *httpHandler) handleDiskSnapshotList(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	snapshots, err := NewDiskSnapshotManager(h.cs.effectiveVMDir()).List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"snapshots": snapshots})
}

func (h *httpHandler) handlePITSnapshotList(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	snapshots, err := NewPITSnapshotManager(h.cs.effectiveVMDir()).List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"snapshots": snapshots})
}

func (h *httpHandler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !h.checkVMName(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	var lastData []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ":\n\n")
			flusher.Flush()
		case <-ticker.C:
			resp := h.cs.handleRequest(&controlpb.ControlRequest{Type: "status"})
			data, err := protojsonMarshaler.Marshal(resp)
			if err != nil {
				continue
			}
			if string(data) == string(lastData) {
				continue
			}
			lastData = data
			writeSSE(w, "", data)
			flusher.Flush()
		}
	}
}

func (h *httpHandler) handleOperationGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	op, ok := h.ops.Get(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found")
		return
	}
	data, err := json.Marshal(op)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "marshal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint
}

func (h *httpHandler) handleOperationList(w http.ResponseWriter, r *http.Request) {
	ops := h.ops.List()
	data, err := json.Marshal(ops)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "marshal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint
}

func (h *httpHandler) handleOperationEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ctx := r.Context()
	ch, err := h.ops.Subscribe(ctx, id)
	if err != nil {
		if errors.Is(err, ErrOperationNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for op := range ch {
		data, err := json.Marshal(op)
		if err != nil {
			continue
		}
		writeSSE(w, "", data)
		flusher.Flush()
	}
}

// writeSSE writes a single SSE event. If event is empty, the event: line is omitted.
func writeSSE(w http.ResponseWriter, event string, data []byte) {
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// writeProtoJSON marshals resp via protojsonMarshaler and writes it as application/json.
func writeProtoJSON(w http.ResponseWriter, resp *controlpb.ControlResponse) {
	data, err := protojsonMarshaler.Marshal(resp)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "marshal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint
}

// writeJSONError writes {"error":"..."} with the given status code.
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "marshal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(append(data, '\n')) //nolint
}

// decodeJSON reads the request body into v. On error it writes a 400 and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}
