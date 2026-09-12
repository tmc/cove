//go:build darwin || linux

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
)

func TestUserExecEnvironment(t *testing.T) {
	t.Setenv("COVE_USER_EXEC_OVERRIDE", "inherited")
	t.Setenv("COVE_USER_EXEC_PRESERVED", "preserved")
	path, handler := agentpbconnect.NewUserAgentHandler(newUserAgentServer())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentpbconnect.NewUserAgentClient(server.Client(), server.URL)
	dir := t.TempDir()
	req := &pb.ExecRequest{
		Args:       []string{"/bin/sh", "-c", `printf '%s\n' "$COVE_USER_EXEC_OVERRIDE" "$COVE_USER_EXEC_PRESERVED"; test "$PWD" -ef "$COVE_USER_EXEC_DIR" || exit 8; printf directory-ok`},
		Env:        map[string]string{"COVE_USER_EXEC_OVERRIDE": "overridden", "COVE_USER_EXEC_DIR": dir},
		WorkingDir: dir,
	}
	const want = "overridden\npreserved\ndirectory-ok"
	for _, mode := range []string{"unary", "stream"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if mode == "unary" {
				resp, err := client.UserExec(ctx, connect.NewRequest(req))
				if err != nil {
					t.Fatal(err)
				}
				if string(resp.Msg.GetStdout()) != want || resp.Msg.GetExitCode() != 0 {
					t.Fatalf("user exec = %v, want stdout %q and exit 0", resp.Msg, want)
				}
				return
			}
			stream, err := client.UserExecStream(ctx, connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var stdout strings.Builder
			var exitCode *int32
			for stream.Receive() {
				msg := stream.Msg()
				if msg.GetStream() == pb.ExecOutput_STDOUT {
					stdout.Write(msg.GetData())
				}
				if msg.ExitCode != nil {
					code := msg.GetExitCode()
					exitCode = &code
				}
			}
			if err := stream.Err(); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != want || exitCode == nil || *exitCode != 0 {
				t.Fatalf("user exec stdout = %q, exit = %v, want %q and exit 0", stdout.String(), exitCode, want)
			}
		})
	}
}
