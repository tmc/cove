package main

import (
	"context"
	"testing"

	pb "github.com/tmc/vz-macos/proto/agentpb"
)

func TestNewExecCommandRejectsUnknownUser(t *testing.T) {
	user := "vz-agent-missing-user"
	_, err := newExecCommand(context.Background(), &pb.ExecRequest{
		Args: []string{"/usr/bin/true"},
		User: &user,
	})
	if err == nil {
		t.Fatalf("newExecCommand() error = nil, want invalid user error")
	}
}
