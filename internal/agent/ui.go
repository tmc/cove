package agent

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/tmc/cove/proto/agentpb"
)

func (c *UserAgentClient) UIStatus(ctx context.Context, request *pb.UIRequest) (*pb.UIResponse, error) {
	response, err := c.client.UIStatus(ctx, connect.NewRequest(request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}
func (c *UserAgentClient) InspectUI(ctx context.Context, request *pb.UIRequest) (*pb.UIResponse, error) {
	response, err := c.client.InspectUI(ctx, connect.NewRequest(request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}
func (c *UserAgentClient) FindUI(ctx context.Context, request *pb.UIRequest) (*pb.UIResponse, error) {
	response, err := c.client.FindUI(ctx, connect.NewRequest(request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}
