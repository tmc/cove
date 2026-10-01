package agent

import (
	"bytes"
	"context"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
)

type stdinUserAgentHandler struct {
	agentpbconnect.UnimplementedUserAgentHandler
	request *pb.ExecRequest
}

func (h *stdinUserAgentHandler) UserExec(_ context.Context, req *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	h.request = req.Msg
	return connect.NewResponse(&pb.ExecResponse{Stdout: req.Msg.Stdin}), nil
}

func TestUserExecWithStdin(t *testing.T) {
	handler := new(stdinUserAgentHandler)
	client := newTestUserAgentClient(t, handler)
	defer client.Close()
	input := []byte("quotes ' \" $() ;\nUnicode: 日本語\x00")
	result, err := client.UserExecWithStdin(context.Background(), []string{"/usr/bin/pbcopy"}, nil, "", input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Stdout, input) {
		t.Fatalf("stdin was not preserved")
	}
	if len(handler.request.Args) != 1 || handler.request.Args[0] != "/usr/bin/pbcopy" {
		t.Fatal("clipboard input leaked into command arguments")
	}
}
