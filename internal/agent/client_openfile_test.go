package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
)

type openFileAgentHandler struct {
	agentpbconnect.UnimplementedAgentHandler
	reqs []*pb.ReadFileRequest
	err  error
}

func (h *openFileAgentHandler) ReadFile(_ context.Context, req *connect.Request[pb.ReadFileRequest]) (*connect.Response[pb.ReadFileResponse], error) {
	h.reqs = append(h.reqs, req.Msg)
	if h.err != nil {
		return nil, h.err
	}
	return connect.NewResponse(&pb.ReadFileResponse{Size: 65536}), nil
}

func TestAgentClientOpenFile(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{name: "open", err: nil},
		{name: "denied", err: connect.NewError(connect.CodePermissionDenied, errors.New("open: operation not permitted")), wantErr: "operation not permitted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &openFileAgentHandler{err: tt.err}
			client := newTestAgentClient(t, h)
			defer client.Close()

			err := client.OpenFile(context.Background(), "/Library/Application Support/com.apple.TCC/TCC.db")
			if tt.wantErr == "" && err != nil {
				t.Fatalf("OpenFile() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("OpenFile() error = %v, want %q", err, tt.wantErr)
			}
			if len(h.reqs) != 1 {
				t.Fatalf("ReadFile calls = %d, want 1", len(h.reqs))
			}
			req := h.reqs[0]
			if req.GetPath() != "/Library/Application Support/com.apple.TCC/TCC.db" || req.Limit == nil || req.GetLimit() != 0 {
				t.Fatalf("ReadFile request = %+v, want explicit zero limit", req)
			}
		})
	}
}

func ExampleAgentClient_OpenFile() {
	client := &AgentClient{client: openFileExampleClient{}}
	err := client.OpenFile(context.Background(), "/Library/Application Support/com.apple.TCC/TCC.db")
	fmt.Println(err)
	// Output: permission_denied: open: operation not permitted
}

type openFileExampleClient struct{ agentpbconnect.AgentClient }

func (openFileExampleClient) ReadFile(_ context.Context, req *connect.Request[pb.ReadFileRequest]) (*connect.Response[pb.ReadFileResponse], error) {
	if req.Msg.Limit == nil || req.Msg.GetLimit() != 0 {
		return nil, errors.New("contents requested")
	}
	return nil, connect.NewError(connect.CodePermissionDenied, errors.New("open: operation not permitted"))
}
