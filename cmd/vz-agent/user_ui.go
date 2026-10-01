package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"google.golang.org/protobuf/proto"
)

type userUIBackend interface {
	Status(context.Context) *pb.UIStatus
	Inspect(context.Context, *pb.UIRequest) ([]*pb.UINode, bool, string, error)
}

type userUIController struct {
	mu             sync.Mutex
	backend        userUIBackend
	sessionID      string
	generation     string
	hostGeneration string
	sessionState   string
}

func newUserUIController(backend userUIBackend) *userUIController {
	return &userUIController{backend: backend, sessionID: fmt.Sprintf("uid:%d", os.Getuid())}
}

func uiToken() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func (c *userUIController) status(ctx context.Context, request *pb.UIRequest) *pb.UIStatus {
	status := c.backend.Status(ctx)
	if status == nil {
		status = &pb.UIStatus{State: "unavailable", Reason: "accessibility backend unavailable"}
	} else {
		status = proto.Clone(status).(*pb.UIStatus)
	}
	status.ProtocolVersion = 1
	c.mu.Lock()
	defer c.mu.Unlock()
	identity := status.SessionState + ":" + status.State
	if c.generation == "" || c.hostGeneration != request.HostGeneration || c.sessionState != identity {
		generation, err := uiToken()
		if err != nil {
			status.State = "unavailable"
			status.Reason = "generate session identity"
			return status
		}
		c.generation = generation
		c.hostGeneration = request.HostGeneration
		c.sessionState = identity
	}
	status.SessionId = c.sessionID
	status.Generation = c.generation
	if request.ExpectedGeneration != "" && request.ExpectedGeneration != c.generation {
		status.State = "stale_generation"
		status.Reason = "user session or host runtime changed; inspect again"
	}
	return status
}

func normalizedUIRequest(request *pb.UIRequest) (*pb.UIRequest, error) {
	if request == nil {
		return nil, errors.New("ui request is missing")
	}
	request = proto.Clone(request).(*pb.UIRequest)
	if request.MaxDepth == 0 {
		request.MaxDepth = 5
	}
	if request.MaxNodes == 0 {
		request.MaxNodes = 256
	}
	if request.MaxBytes == 0 {
		request.MaxBytes = 64 << 10
	}
	if request.TimeoutMs == 0 {
		request.TimeoutMs = 2000
	}
	if request.MaxDepth > 16 || request.MaxNodes > 1024 || request.MaxBytes < 4096 || request.MaxBytes > 256<<10 || request.TimeoutMs < 100 || request.TimeoutMs > 10000 {
		return nil, errors.New("ui request bounds exceeded")
	}
	if len(request.Role) > 256 || len(request.Identifier) > 1024 || len(request.Label) > 1024 || len(request.HostGeneration) > 128 || len(request.ExpectedGeneration) > 128 {
		return nil, errors.New("ui selector or generation exceeds bounds")
	}
	return request, nil
}

func (s *userAgentServer) UIStatus(ctx context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	request, err := normalizedUIRequest(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutMs)*time.Millisecond)
	defer cancel()
	return connect.NewResponse(&pb.UIResponse{Status: s.ui.status(ctx, request)}), nil
}

func (s *userAgentServer) InspectUI(ctx context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	return s.inspectUI(ctx, req, false)
}

func (s *userAgentServer) FindUI(ctx context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	return s.inspectUI(ctx, req, true)
}

func (s *userAgentServer) inspectUI(ctx context.Context, req *connect.Request[pb.UIRequest], find bool) (*connect.Response[pb.UIResponse], error) {
	request, err := normalizedUIRequest(req.Msg)
	if err != nil || request != nil && request.Pid <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("positive application pid and valid bounds required"))
	}
	if find && request.Role == "" && request.Identifier == "" && request.Label == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("find requires a role, identifier or label"))
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutMs)*time.Millisecond)
	defer cancel()
	result := &pb.UIResponse{Status: s.ui.status(ctx, request)}
	if result.Status.State != "ready" {
		return connect.NewResponse(result), nil
	}
	observation, err := uiToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	result.ObservationId = observation
	result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	nodes, truncated, reason, err := s.ui.backend.Inspect(ctx, request)
	if err != nil {
		result.Status.State = "application_unresponsive"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.Status.State = "timed_out"
		}
		var failure *uiReadError
		if errors.As(err, &failure) {
			result.Status.State = failure.state
		}
		result.Status.Reason = boundedUIString(err.Error(), 1024)
		return connect.NewResponse(result), nil
	}
	result.Truncated = truncated
	result.TruncationReason = reason
	handles := make(map[string]string)
	for index, node := range nodes {
		if node == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("ui backend returned a missing node"))
		}
		key := node.Handle
		if key == "" {
			key = strconv.Itoa(index + 1)
		}
		sum := sha256.Sum256([]byte(result.Status.Generation + ":" + observation + ":" + key))
		handles[key] = hex.EncodeToString(sum[:16])
	}
	for index, node := range nodes {
		node = proto.Clone(node).(*pb.UINode)
		key := node.Handle
		if key == "" {
			key = strconv.Itoa(index + 1)
		}
		node.Handle = handles[key]
		node.ParentHandle = handles[node.ParentHandle]
		node.ValueOmitted = true
		node.Role = boundedUIString(node.Role, 256)
		node.Subrole = boundedUIString(node.Subrole, 256)
		node.Identifier = boundedUIString(node.Identifier, 1024)
		node.Label = boundedUIString(node.Label, 1024)
		if find && (request.Role != "" && request.Role != node.Role || request.Identifier != "" && request.Identifier != node.Identifier || request.Label != "" && request.Label != node.Label) {
			continue
		}
		if uint32(len(result.Nodes)) >= request.MaxNodes {
			result.Truncated = true
			result.TruncationReason = "node_limit"
			break
		}
		result.Nodes = append(result.Nodes, node)
		if proto.Size(result) > int(request.MaxBytes) {
			result.Nodes = result.Nodes[:len(result.Nodes)-1]
			result.Truncated = true
			result.TruncationReason = "byte_limit"
			break
		}
	}
	if find {
		result.MatchState = "matched"
		if len(result.Nodes) == 0 {
			result.MatchState = "no_matches"
		} else if len(result.Nodes) > 1 {
			result.MatchState = "multiple_matches"
		}
		if result.Truncated {
			result.MatchState = "incomplete"
		}
	}
	finalStatus := s.ui.status(ctx, request)
	if finalStatus.Generation != result.Status.Generation || finalStatus.State != "ready" {
		result.Nodes = nil
		result.Status = finalStatus
		if result.Status.State == "ready" {
			result.Status.State = "stale_generation"
			result.Status.Reason = "session changed during inspection"
		}
	}
	return connect.NewResponse(result), nil
}

type uiReadError struct {
	state string
	code  int32
}

func (e *uiReadError) Error() string {
	return fmt.Sprintf("accessibility read %s (code %d)", e.state, e.code)
}

func boundedUIString(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
		for len(value) > 0 && !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
