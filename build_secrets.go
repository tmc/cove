package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/secrets"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

const buildSecretsGuestDir = "/tmp/cove-secrets"

func mountBuildStepSecrets(ctx context.Context, step buildPlanStep, sc buildScratch, socketPath string) (buildGuestCleanup, error) {
	values, err := buildStepSecretValues(step)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	platform := agentstate.Platform(sc.Dir)
	if err := prepareBuildSecretsGuestDir(ctx, socketPath, platform); err != nil {
		return nil, err
	}
	cleanup := func(ctx context.Context) error {
		return cleanupBuildSecretsGuestDir(ctx, socketPath, platform)
	}
	for _, name := range sortedMapKeys(values) {
		if err := writeBuildSecret(ctx, socketPath, name, values[name]); err != nil {
			_ = cleanup(ctx)
			return nil, err
		}
	}
	return cleanup, nil
}

func buildStepSecretValues(step buildPlanStep) (map[string][]byte, error) {
	values := make(map[string][]byte, len(step.Meta.Secrets)+len(step.Meta.SecretFrom))
	for _, name := range step.Meta.Secrets {
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil, fmt.Errorf("missing secret environment variable %s", name)
		}
		values[name] = []byte(value)
	}
	for _, ref := range step.Meta.SecretFrom {
		if _, ok := values[ref.Name]; ok {
			return nil, fmt.Errorf("secret %s declared more than once", ref.Name)
		}
		value, err := secrets.Resolve(ref.URI)
		if err != nil {
			return nil, fmt.Errorf("secret-from %s=%s: %w", ref.Name, ref.URI, err)
		}
		values[ref.Name] = value
	}
	return values, nil
}

func prepareBuildSecretsGuestDir(ctx context.Context, socketPath, platform string) error {
	switch platform {
	case agentstate.PlatformLinux:
		if err := runBuildAgentShell(ctx, socketPath, "swapoff -a; test -z \"$(swapon --show --noheadings)\""); err != nil {
			return fmt.Errorf("verify linux no-swap: %w", err)
		}
		return runBuildAgentShell(ctx, socketPath, "rm -rf "+shellQuote(buildSecretsGuestDir)+"; mkdir -p "+shellQuote(buildSecretsGuestDir)+"; mount -t tmpfs -o rw,noexec,nosuid,nodev,mode=0700,noswap tmpfs "+shellQuote(buildSecretsGuestDir))
	case agentstate.PlatformMacOS:
		// `hdiutil attach -nomount ram://N` prints a line like "/dev/disk4          "
		// with trailing whitespace, so trim before passing the device to
		// `diskutil erasevolume`. Without the trim, erasevolume fails with
		// "Unable to find disk for /dev/disk4 ".
		script := strings.Join([]string{
			"set -e",
			"rm -rf " + shellQuote(buildSecretsGuestDir),
			"dev=$(hdiutil attach -nomount ram://131072 | awk 'NR==1{print $1}')",
			"test -n \"$dev\"",
			"diskutil erasevolume APFS cove_secrets \"$dev\" >/dev/null",
			"ln -s /Volumes/cove_secrets " + shellQuote(buildSecretsGuestDir),
		}, "; ")
		return runBuildAgentShell(ctx, socketPath, script)
	default:
		return fmt.Errorf("unsupported guest platform %q", platform)
	}
}

func cleanupBuildSecretsGuestDir(ctx context.Context, socketPath, platform string) error {
	switch platform {
	case agentstate.PlatformLinux:
		return runBuildAgentShell(ctx, socketPath, "umount "+shellQuote(buildSecretsGuestDir)+" 2>/dev/null || true; rm -rf "+shellQuote(buildSecretsGuestDir))
	case agentstate.PlatformMacOS:
		script := strings.Join([]string{
			"set +e",
			"hdiutil detach /Volumes/cove_secrets >/dev/null 2>&1 || true",
			"rm -f " + shellQuote(buildSecretsGuestDir),
		}, "; ")
		return runBuildAgentShell(ctx, socketPath, script)
	default:
		return nil
	}
}

func writeBuildSecret(ctx context.Context, socketPath, name string, value []byte) error {
	if !validBuildSecretName(name) {
		return fmt.Errorf("invalid secret name %q", name)
	}
	req := &controlpb.ControlRequest{
		Type: "agent-write",
		Command: &controlpb.ControlRequest_AgentWrite{
			AgentWrite: &controlpb.AgentFileWriteCommand{
				Path: path.Join(buildSecretsGuestDir, name),
				Data: base64.StdEncoding.EncodeToString(value),
				Mode: 0600,
			},
		},
	}
	resp, err := sendBuildControl(ctx, socketPath, req, 30*time.Second, "agent-write")
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("write secret %s: %s", name, resp.Error)
	}
	return nil
}

func runBuildAgentShell(ctx context.Context, socketPath, script string) error {
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: []string{"/bin/sh", "-c", script}},
		},
	}
	resp, err := sendBuildControl(ctx, socketPath, req, 2*time.Minute, "agent-exec")
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	result := resp.GetAgentExecResult()
	if result != nil && result.ExitCode != 0 {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(result.Stdout)
		}
		return fmt.Errorf("exit %d: %s", result.ExitCode, msg)
	}
	return nil
}

func sendBuildControl(ctx context.Context, socketPath string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := sendBuildControlRequest(socketPath, req, timeout, cmdType)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return resp, nil
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
