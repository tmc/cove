//go:build !darwin

package main

import (
	"context"
	"errors"
	pb "github.com/tmc/cove/proto/agentpb"
)

type unsupportedUIBackend struct{}

func newPlatformUIBackend() userUIBackend { return unsupportedUIBackend{} }
func (unsupportedUIBackend) Status(context.Context) *pb.UIStatus {
	return &pb.UIStatus{State: "unsupported", Backend: "none", PermissionState: "unsupported", SessionState: "unknown", Reason: "guest accessibility is supported on macOS only"}
}
func (unsupportedUIBackend) Inspect(context.Context, *pb.UIRequest) ([]*pb.UINode, bool, string, error) {
	return nil, false, "", errors.New("guest accessibility unsupported")
}
