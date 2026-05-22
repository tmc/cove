// control_operations.go - Per-VM long-running operations registry exposed
// over the control socket. The registry is lazily initialized on first use
// against a file-backed store at <vmDir>/operations/, so the gateway and the
// per-VM ctl client both see the same persistent op records.
package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/tmc/cove/internal/control/operations"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

// ensureOps lazily initializes the per-VM operations registry. The store is
// rooted at <vmDir>/operations/ so async ops survive cove restarts and any
// orphaned pending/running records on Load() are reaped to "failed" with
// code "server_restart" by the underlying FileOperationStore.
func (s *ControlServer) ensureOps() (*operations.OperationRegistry, error) {
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	if s.opsReg != nil {
		return s.opsReg, nil
	}
	dir := filepath.Join(s.effectiveVMDir(), "operations")
	store, err := operations.NewFileOperationStore(dir)
	if err != nil {
		return nil, fmt.Errorf("init operations store: %w", err)
	}
	reg, err := operations.NewOperationRegistry(store)
	if err != nil {
		return nil, fmt.Errorf("init operations registry: %w", err)
	}
	s.opsReg = reg
	return s.opsReg, nil
}

// operationToProto converts an in-memory Operation to its proto wire form.
// Times are emitted in RFC3339 to keep JSON parsers happy.
func operationToProto(op *operations.Operation) *controlpb.OperationInfo {
	if op == nil {
		return nil
	}
	out := &controlpb.OperationInfo{
		Id:        op.ID,
		Resource:  op.Resource,
		Status:    op.Status,
		CreatedAt: op.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: op.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if op.Error != nil {
		out.ErrorCode = op.Error.Code
		out.ErrorMessage = op.Error.Message
	}
	return out
}

// handleOperationsCommand serves "get" and "list" actions against the per-VM
// operations registry. "get" requires id; "list" returns all known ops.
func (s *ControlServer) handleOperationsCommand(cmd *controlpb.OperationsCommand) *controlpb.ControlResponse {
	reg, err := s.ensureOps()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	switch cmd.Action {
	case "get":
		if cmd.Id == "" {
			return &controlpb.ControlResponse{Error: "operations get: id required"}
		}
		op, ok := reg.Get(cmd.Id)
		if !ok {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("operation not found: %s", cmd.Id)}
		}
		info := operationToProto(op)
		return &controlpb.ControlResponse{
			Success: true,
			Result:  &controlpb.ControlResponse_Operation{Operation: info},
		}

	case "list":
		all := reg.List()
		infos := make([]*controlpb.OperationInfo, 0, len(all))
		for _, op := range all {
			infos = append(infos, operationToProto(op))
		}
		return &controlpb.ControlResponse{
			Success: true,
			Result: &controlpb.ControlResponse_OperationsList{
				OperationsList: &controlpb.OperationsListResponse{Operations: infos},
			},
		}

	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("operations: unknown action %q (use get or list)", cmd.Action)}
	}
}
