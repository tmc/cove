package agent

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
)

func ExampleUserAgentClient_UserExecWithStdin() {
	client := &UserAgentClient{client: stdinExampleClient{}}
	result, err := client.UserExecWithStdin(context.Background(), []string{"cat"}, nil, "", []byte("hello\n"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Print(string(result.Stdout))
	// Output: hello
}

type stdinExampleClient struct{ agentpbconnect.UserAgentClient }

func (stdinExampleClient) UserExec(_ context.Context, req *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	return connect.NewResponse(&pb.ExecResponse{Stdout: req.Msg.Stdin}), nil
}
