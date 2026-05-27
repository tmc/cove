//go:build !darwin

package main

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	pb "github.com/tmc/cove/proto/agentpb"
)

func resizeMacOSAPFS(context.Context, bool) (*pb.ResizeMacOSAPFSResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("macOS APFS resize is only available on darwin guests"))
}
