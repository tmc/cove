package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	agentpb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestAgentExecBudget(t *testing.T) {
	for _, tt := range []struct {
		name   string
		offset time.Duration
		zero   bool
	}{
		{"expired", -time.Second, false},
		{"caller", time.Minute, false},
		{"server-cap", time.Hour, false},
		{"legacy", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			requested := before.Add(tt.offset)
			value := requested.UnixNano()
			if tt.zero {
				value = 0
			}
			ctx, cancel := agentExecBudget(context.Background(), value)
			defer cancel()
			deadline, _ := ctx.Deadline()
			if !tt.zero && tt.offset < 10*time.Minute {
				if !deadline.Equal(requested) {
					t.Fatalf("deadline %v, want %v", deadline, requested)
				}
			} else if deadline.Before(before.Add(10*time.Minute)) || deadline.After(time.Now().Add(10*time.Minute)) {
				t.Fatalf("server cap %v", deadline)
			}
		})
	}
}

func TestWorkspaceExecDeadlineWireAndError(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")
	dir := shortSharedFolderVMDir(t)
	listener, err := net.Listen("unix", GetControlSocketPathForVM(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			done <- err
			return
		}
		var req controlpb.ControlRequest
		if err := protojson.Unmarshal(line, &req); err != nil {
			done <- err
			return
		}
		if cmd := req.GetAgentExec(); cmd == nil || cmd.DeadlineUnixNano != want.UnixNano() {
			done <- errors.New("caller deadline absent or replaced")
			return
		}
		_, err = conn.Write([]byte("{\"error\":\"command not dispatched\"}\n"))
		done <- err
	}()
	_, err = workspaceExec(ctx, dir, "agent-user-exec", []string{"true"}, nil, "", time.Minute)
	if err == nil || !strings.Contains(err.Error(), "command not dispatched") {
		t.Fatalf("error %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAgentExecBudgetDispatch(t *testing.T) {
	for _, mode := range []string{"expired", "blocked-acquisition", "expires-after-acquisition", "dispatched"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if mode == "expired" {
				cancel()
			}
			acquired, called := false, false
			_, dispatched, err := acquireAgentExec(ctx, func(got context.Context) (func(context.Context) (*agentpb.ExecResponse, error), error) {
				acquired = true
				if got != ctx {
					t.Fatal("acquisition replaced caller budget")
				}
				if mode == "blocked-acquisition" {
					<-got.Done()
					return nil, got.Err()
				}
				if mode == "expires-after-acquisition" {
					cancel()
				}
				return func(got context.Context) (*agentpb.ExecResponse, error) {
					called = true
					if got != ctx {
						t.Fatal("dispatch replaced caller budget")
					}
					<-got.Done()
					return nil, got.Err()
				}, nil
			})
			if err == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("error %v", err)
			}
			want := mode == "dispatched"
			if called != want || dispatched != want || mode == "expired" && acquired {
				t.Fatalf("acquired %v, called %v, dispatched %v", acquired, called, dispatched)
			}
		})
	}
}

func TestAgentExecBudgetInheritedParent(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx, stop := agentExecBudget(parent, time.Now().Add(time.Hour).UnixNano())
	defer stop()
	want, _ := parent.Deadline()
	got, _ := ctx.Deadline()
	if !got.Equal(want) {
		t.Fatalf("deadline %v, want %v", got, want)
	}
}
