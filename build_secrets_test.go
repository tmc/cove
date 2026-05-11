package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestMountBuildStepSecretsLinux(t *testing.T) {
	t.Setenv("BUILD_SECRET_A", "alpha")
	t.Setenv("BUILD_SECRET_B", "beta")
	root := t.TempDir()
	sc := buildScratch{Dir: filepath.Join(root, "scratch")}
	if err := os.MkdirAll(sc.Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sc.Dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		if sock != "sock" {
			t.Fatalf("sock = %q, want sock", sock)
		}
		calls = append(calls, cmdType+":"+requestSummary(req))
		switch req.Type {
		case "agent-exec":
			return &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{}},
			}, nil
		case "agent-write":
			return &controlpb.ControlResponse{Success: true}, nil
		default:
			t.Fatalf("request type = %q", req.Type)
			return nil, nil
		}
	})
	defer restore()
	step := buildPlanStep{Meta: buildScriptMeta{Secrets: []string{"BUILD_SECRET_B", "BUILD_SECRET_A"}}}
	cleanup, err := mountBuildStepSecrets(context.Background(), step, sc, "sock")
	if err != nil {
		t.Fatalf("mountBuildStepSecrets(): %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup = nil")
	}
	if err := cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup(): %v", err)
	}
	want := []string{
		"agent-exec:/bin/sh -c swapoff -a; test -z \"$(swapon --show --noheadings)\"",
		"agent-exec:/bin/sh -c rm -rf '/tmp/cove-secrets'; mkdir -p '/tmp/cove-secrets'; mount -t tmpfs -o rw,noexec,nosuid,nodev,mode=0700,noswap tmpfs '/tmp/cove-secrets'",
		"agent-write:/tmp/cove-secrets/BUILD_SECRET_A=alpha",
		"agent-write:/tmp/cove-secrets/BUILD_SECRET_B=beta",
		"agent-exec:/bin/sh -c umount '/tmp/cove-secrets' 2>/dev/null || true; rm -rf '/tmp/cove-secrets'",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v\nwant %#v", calls, want)
	}
}

func TestMountBuildStepSecretsLinuxSwapFailure(t *testing.T) {
	t.Setenv("BUILD_SECRET", "secret")
	root := t.TempDir()
	sc := buildScratch{Dir: filepath.Join(root, "scratch")}
	if err := os.MkdirAll(sc.Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sc.Dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{
			Success: true,
			Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
				ExitCode: 1,
				Stderr:   "/swapfile\n",
			}},
		}, nil
	})
	defer restore()
	_, err := mountBuildStepSecrets(context.Background(), buildPlanStep{Meta: buildScriptMeta{Secrets: []string{"BUILD_SECRET"}}}, sc, "sock")
	if err == nil || !strings.Contains(err.Error(), "verify linux no-swap") {
		t.Fatalf("mountBuildStepSecrets() = %v, want no-swap failure", err)
	}
}

func TestBuildStepSecretValuesSecretFromEnv(t *testing.T) {
	t.Setenv("SOURCE_TOKEN", "secret-from-env")
	values, err := buildStepSecretValues(buildPlanStep{Meta: buildScriptMeta{
		SecretFrom: []buildSecretRef{{Name: "TARGET_TOKEN", URI: "env://SOURCE_TOKEN"}},
	}})
	if err != nil {
		t.Fatalf("buildStepSecretValues(): %v", err)
	}
	if got := string(values["TARGET_TOKEN"]); got != "secret-from-env" {
		t.Fatalf("TARGET_TOKEN = %q, want secret-from-env", got)
	}
	if _, ok := values["SOURCE_TOKEN"]; ok {
		t.Fatal("source environment name was exposed as a guest secret")
	}
}

func TestBuildStepSecretValuesSecretFromDuplicate(t *testing.T) {
	t.Setenv("BUILD_SECRET", "legacy")
	t.Setenv("SOURCE_TOKEN", "secret-from-env")
	_, err := buildStepSecretValues(buildPlanStep{Meta: buildScriptMeta{
		Secrets:    []string{"BUILD_SECRET"},
		SecretFrom: []buildSecretRef{{Name: "BUILD_SECRET", URI: "env://SOURCE_TOKEN"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "secret BUILD_SECRET declared more than once") {
		t.Fatalf("buildStepSecretValues() = %v, want duplicate error", err)
	}
}

func TestBuildStepSecretValuesSecretFromFileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := buildStepSecretValues(buildPlanStep{Meta: buildScriptMeta{
		SecretFrom: []buildSecretRef{{Name: "FILE_SECRET", URI: "file://" + path}},
	}})
	if err == nil || !strings.Contains(err.Error(), "secret-from FILE_SECRET=file://") || !strings.Contains(err.Error(), "permissions 0644 too open") {
		t.Fatalf("buildStepSecretValues() = %v, want wrapped file permissions error", err)
	}
}

func TestMountBuildStepSecretsMacOS(t *testing.T) {
	t.Setenv("BUILD_SECRET", "secret")
	root := t.TempDir()
	sc := buildScratch{Dir: filepath.Join(root, "scratch")}
	if err := os.MkdirAll(sc.Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sc.Dir, "disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		calls = append(calls, cmdType+":"+requestSummary(req))
		switch req.Type {
		case "agent-exec":
			return &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{}},
			}, nil
		case "agent-write":
			return &controlpb.ControlResponse{Success: true}, nil
		default:
			t.Fatalf("request type = %q", req.Type)
			return nil, nil
		}
	})
	defer restore()
	cleanup, err := mountBuildStepSecrets(context.Background(), buildPlanStep{Meta: buildScriptMeta{Secrets: []string{"BUILD_SECRET"}}}, sc, "sock")
	if err != nil {
		t.Fatalf("mountBuildStepSecrets(): %v", err)
	}
	if err := cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup(): %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %#v, want prepare, write, cleanup", calls)
	}
	for _, want := range []string{"hdiutil attach -nomount ram://131072", "diskutil erasevolume APFS cove_secrets", "ln -s /Volumes/cove_secrets '/tmp/cove-secrets'"} {
		if !strings.Contains(calls[0], want) {
			t.Fatalf("prepare call missing %q: %s", want, calls[0])
		}
	}
	if calls[1] != "agent-write:/tmp/cove-secrets/BUILD_SECRET=secret" {
		t.Fatalf("write call = %q", calls[1])
	}
	for _, want := range []string{"hdiutil detach /Volumes/cove_secrets", "rm -f '/tmp/cove-secrets'"} {
		if !strings.Contains(calls[2], want) {
			t.Fatalf("cleanup call missing %q: %s", want, calls[2])
		}
	}
}

func requestSummary(req *controlpb.ControlRequest) string {
	switch req.Type {
	case "agent-exec":
		return strings.Join(req.GetAgentExec().GetArgs(), " ")
	case "agent-write":
		cmd := req.GetAgentWrite()
		data, _ := base64.StdEncoding.DecodeString(cmd.GetData())
		return cmd.GetPath() + "=" + string(data)
	default:
		return req.Type
	}
}
