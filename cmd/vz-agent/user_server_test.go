//go:build darwin || linux

package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
)

func TestUserExecEnvironment(t *testing.T) {
	t.Setenv("COVE_EXEC_OVERRIDE", "inherited")
	t.Setenv("COVE_EXEC_INHERITED", "keep")
	_, handler := agentpbconnect.NewUserAgentHandler(newUserAgentServer())
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentpbconnect.NewUserAgentClient(server.Client(), server.URL)
	for _, name := range []string{"exec", "stream"} {
		t.Run(name, func(t *testing.T) {
			request := connect.NewRequest(&pb.ExecRequest{Args: []string{"/usr/bin/env"}, Env: map[string]string{"COVE_EXEC_OVERRIDE": "override with spaces", "COVE_EXEC_NEW": "literal $(not a command)"}})
			var output string
			if name == "exec" {
				response, err := client.UserExec(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				if response.Msg.ExitCode != 0 {
					t.Fatalf("exit=%d stderr=%s", response.Msg.ExitCode, response.Msg.Stderr)
				}
				output = string(response.Msg.Stdout)
			} else {
				stream, err := client.UserExecStream(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				for stream.Receive() {
					output += string(stream.Msg().Data)
				}
				if err := stream.Err(); err != nil {
					t.Fatal(err)
				}
			}
			for _, value := range []string{"COVE_EXEC_OVERRIDE=override with spaces\n", "COVE_EXEC_INHERITED=keep\n", "COVE_EXEC_NEW=literal $(not a command)\n"} {
				if !strings.Contains(output, value) {
					t.Fatalf("missing %q in environment", value)
				}
			}
		})
	}
}
