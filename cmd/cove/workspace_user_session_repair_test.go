package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/tmc/cove/internal/vmconfig"
	pb "github.com/tmc/cove/proto/agentpb"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestWorkspaceUserRepairBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name             string
		healthy, unknown bool
	}{{"healthy", true, false}, {"restart", false, false}, {"unknown restart", false, true}} {
		t.Run(tt.name, func(t *testing.T) {
			observations, restarts := 0, 0
			call := func(ctx context.Context, route string, args []string, cwd string) (*controlpb.AgentExecResponse, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if route == "agent-exec" && cwd == "/source" {
					if route != "agent-exec" || cwd != "/source" {
						t.Fatal("wrong root evidence")
					}
					return &controlpb.AgentExecResponse{Stdout: "/source\n"}, nil
				}
				if len(args) > 2 && strings.Contains(args[2], "kickstart -k -p") {
					restarts++
					if tt.unknown {
						return nil, context.DeadlineExceeded
					}
					return &controlpb.AgentExecResponse{Stdout: "101\n"}, nil
				}
				if route == "workspace-user-observe" {
					if cwd != "/source" {
						t.Fatal("wrong user cwd")
					}
					if tt.healthy || restarts > 0 {
						return &controlpb.AgentExecResponse{Stdout: "502\n" + map[bool]string{false: "100", true: "101"}[restarts > 0] + "\n/source\n"}, nil
					}
					return &controlpb.AgentExecResponse{ExitCode: 1, Stdout: "502\n100\n", Stderr: "pwd: .: No such file or directory"}, nil
				}
				observations++
				pid := "100"
				if restarts > 0 {
					pid = "101"
				}
				return &controlpb.AgentExecResponse{Stdout: "502\n" + pid + "\n"}, nil
			}
			changed, err := repairWorkspaceUserSession(context.Background(), workspacePlan{SourceGuestPath: "/source"}, 502, call)
			if tt.healthy {
				if err != nil || changed || restarts != 0 {
					t.Fatalf("healthy %v %v restart%d", changed, err, restarts)
				}
			} else if tt.unknown {
				if !errors.Is(err, context.DeadlineExceeded) || restarts != 1 {
					t.Fatalf("unknown %v restart%d", err, restarts)
				}
			} else if err != nil || !changed || restarts != 1 || observations != 3 {
				t.Fatalf("restart %v %v calls%d", changed, err, restarts)
			}
		})
	}
}

func TestWorkspaceUserRepairIdentityMismatchStops(t *testing.T) {
	calls := 0
	_, err := repairWorkspaceUserSession(context.Background(), workspacePlan{SourceGuestPath: "/source"}, 502, func(context.Context, string, []string, string) (*controlpb.AgentExecResponse, error) {
		calls++
		if calls == 1 {
			return &controlpb.AgentExecResponse{Stdout: "/source"}, nil
		}
		return &controlpb.AgentExecResponse{Stdout: "503\n100\n"}, nil
	})
	if err == nil || calls != 2 {
		t.Fatalf("identity %v calls%d", err, calls)
	}
}

func TestWorkspaceUserRepairUnknownPreflightStops(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result *controlpb.AgentExecResponse
		err    error
	}{
		{"permission", &controlpb.AgentExecResponse{ExitCode: 1, Stdout: "502\n100\n", Stderr: "Operation not permitted"}, nil},
		{"wrong user", &controlpb.AgentExecResponse{ExitCode: 1, Stdout: "503\n100\n", Stderr: "No such file or directory"}, nil},
		{"timeout", nil, context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			_, err := repairWorkspaceUserSession(context.Background(), workspacePlan{SourceGuestPath: "/source"}, 502, func(_ context.Context, route string, args []string, _ string) (*controlpb.AgentExecResponse, error) {
				calls++
				if calls == 1 {
					return &controlpb.AgentExecResponse{Stdout: "/source"}, nil
				}
				if route == "workspace-user-observe" {
					return tt.result, tt.err
				}
				if len(args) > 2 && strings.Contains(args[2], "kickstart") {
					t.Fatal("restart after unknown evidence")
				}
				return &controlpb.AgentExecResponse{Stdout: "502\n100\n"}, nil
			})
			if err == nil || calls != 3 {
				t.Fatalf("preflight %v calls%d", err, calls)
			}
		})
	}
}

func TestWorkspaceRepairCLISessionAdmission(t *testing.T) {
	for _, state := range []string{"locked", "inactive", "unknown", "failed dispatch"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv(controlTokenEnvVar, "")
			root := shortSharedFolderVMDir(t)
			t.Setenv(vmconfig.StateDirEnv, root)
			dir := vmconfig.Path("repair")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := vmconfig.Save(dir, &vmconfig.Config{GuestUserUID: 502}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hw.model"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			source, output := t.TempDir(), t.TempDir()
			var err error
			source, err = filepath.EvalSymlinks(source)
			if err != nil {
				t.Fatal(err)
			}
			output, err = filepath.EvalSymlinks(output)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveSharedFolders(dir, []SharedFolderEntry{{Path: source, Tag: workspaceSourceTag, ReadOnly: true}, {Path: output, Tag: workspaceOutputTag}}); err != nil {
				t.Fatal(err)
			}
			resp := &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentUi{AgentUi: &pb.UIResponse{Status: &pb.UIStatus{SessionState: state}}}}
			if state == "failed dispatch" {
				resp = &controlpb.ControlResponse{Error: "observation unavailable"}
			}
			verify := serveSharedFolderControlSteps(t, dir, "token", []sharedFolderControlStep{{wantType: "agent-ui-status", resp: resp}})
			var out bytes.Buffer
			err = handleWorkspaceCommand(commandEnv{Stdout: &out, Stderr: &out}, []string{"repair", "-vm", "repair", "-source", source, "-output", output})
			t.Logf("repair admission: %v", err)
			verify()
			if err == nil {
				t.Fatal("invalid session repair admitted")
			}
		})
	}
}
