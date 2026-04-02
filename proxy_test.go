package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	pb "github.com/tmc/vz-macos/proto/agentpb"
)

func TestParseProxySpec(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantURL string
		wantErr bool
	}{
		{name: "empty", raw: "", wantURL: ""},
		{name: "http", raw: "http://127.0.0.1:8080", wantURL: "http://127.0.0.1:8080"},
		{name: "ipv6", raw: "https://[::1]:8443", wantURL: "http://[::1]:8443"},
		{name: "missing port", raw: "http://127.0.0.1", wantErr: true},
		{name: "userinfo", raw: "http://user:pass@127.0.0.1:8080", wantErr: true},
		{name: "bad scheme", raw: "socks5://127.0.0.1:1080", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := parseProxySpec(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseProxySpec(%q) = nil error, want one", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProxySpec(%q) = %v", tt.raw, err)
			}
			if spec.canonicalURL() != tt.wantURL && tt.wantURL != "" {
				t.Fatalf("canonicalURL = %q, want %q", spec.canonicalURL(), tt.wantURL)
			}
		})
	}
}

func TestResolveProxyValidationFor(t *testing.T) {
	tests := []struct {
		name       string
		cfg        proxyFlags
		wantStatus string
		wantSource string
		wantReason string
		wantErr    string
	}{
		{
			name: "macos requested from config",
			cfg: proxyFlags{
				RawURL:         "http://127.0.0.1:8080",
				NetworkMode:    "nat",
				RuntimeProfile: "full",
				AgentConfig: &VMAgentConfig{
					Platform:  vmAgentPlatformMacOS,
					Requested: true,
				},
			},
			wantStatus: proxyCapabilityReady,
			wantSource: proxyCapabilityConfig,
			wantReason: "macos_agent_requested",
		},
		{
			name: "macos inject marker",
			cfg: proxyFlags{
				RawURL:          "http://127.0.0.1:8080",
				NetworkMode:     "nat",
				RuntimeProfile:  "full",
				InjectSucceeded: true,
			},
			wantStatus: proxyCapabilityReady,
			wantSource: proxyCapabilityConfig,
			wantReason: "inject_succeeded",
		},
		{
			name: "linux verified from config",
			cfg: proxyFlags{
				RawURL:      "http://127.0.0.1:8080",
				NetworkMode: "nat",
				Linux:       true,
				AgentConfig: &VMAgentConfig{
					Platform: vmAgentPlatformLinux,
					Verified: true,
				},
			},
			wantStatus: proxyCapabilityReady,
			wantSource: proxyCapabilityConfig,
			wantReason: "agent_verified",
		},
		{
			name: "linux requested remains unknown",
			cfg: proxyFlags{
				RawURL:      "http://127.0.0.1:8080",
				NetworkMode: "nat",
				Linux:       true,
				AgentConfig: &VMAgentConfig{
					Platform:  vmAgentPlatformLinux,
					Requested: true,
				},
			},
			wantStatus: proxyCapabilityUnknown,
			wantSource: proxyCapabilityConfig,
			wantReason: "linux_agent_requested",
		},
		{
			name: "linux requested false is unavailable",
			cfg: proxyFlags{
				RawURL:      "http://127.0.0.1:8080",
				NetworkMode: "nat",
				Linux:       true,
				AgentConfig: &VMAgentConfig{
					Platform: vmAgentPlatformLinux,
				},
			},
			wantErr: "provision-agent",
		},
		{
			name: "running linux runtime probe wins",
			cfg: proxyFlags{
				RawURL:       "http://127.0.0.1:8080",
				NetworkMode:  "nat",
				Linux:        true,
				Running:      true,
				RunningKnown: true,
				CapabilityProbe: func(context.Context, proxyFlags) proxyCapability {
					return proxyCapability{Status: proxyCapabilityReady, Source: proxyCapabilityRuntime, Reason: "linux_agent_ping"}
				},
			},
			wantStatus: proxyCapabilityReady,
			wantSource: proxyCapabilityRuntime,
			wantReason: "linux_agent_ping",
		},
		{
			name: "running macos without user agent rejects",
			cfg: proxyFlags{
				RawURL:         "http://127.0.0.1:8080",
				NetworkMode:    "nat",
				RuntimeProfile: "full",
				Running:        true,
				RunningKnown:   true,
				CapabilityProbe: func(context.Context, proxyFlags) proxyCapability {
					return proxyCapability{Status: proxyCapabilityUnavailable, Source: proxyCapabilityRuntime, Reason: "macos_user_agent_unavailable"}
				},
			},
			wantErr: "user agent",
		},
		{
			name: "strict sandbox rejects",
			cfg: proxyFlags{
				RawURL:       "http://127.0.0.1:8080",
				SandboxLevel: "strict",
				NetworkMode:  "nat",
			},
			wantErr: "strict",
		},
		{
			name: "network none rejects",
			cfg: proxyFlags{
				RawURL:      "http://127.0.0.1:8080",
				NetworkMode: "none",
			},
			wantErr: "network none",
		},
		{
			name: "minimal runtime rejects",
			cfg: proxyFlags{
				RawURL:         "http://127.0.0.1:8080",
				NetworkMode:    "nat",
				RuntimeProfile: "minimal",
			},
			wantErr: "minimal",
		},
		{
			name: "bad url rejects",
			cfg: proxyFlags{
				RawURL:      "http://127.0.0.1",
				NetworkMode: "nat",
			},
			wantErr: "missing a port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveProxyValidationFor(context.Background(), tt.cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantErr)) {
					t.Fatalf("resolveProxyValidationFor() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveProxyValidationFor() = %v", err)
			}
			if got.Capability.Status != tt.wantStatus {
				t.Fatalf("capability status = %q, want %q", got.Capability.Status, tt.wantStatus)
			}
			if got.Capability.Source != tt.wantSource {
				t.Fatalf("capability source = %q, want %q", got.Capability.Source, tt.wantSource)
			}
			if got.Capability.Reason != tt.wantReason {
				t.Fatalf("capability reason = %q, want %q", got.Capability.Reason, tt.wantReason)
			}
		})
	}
}

func TestValidateProxyFlagsUsesExistingLinuxAgentState(t *testing.T) {
	oldURL, oldSandbox, oldNet, oldRuntime, oldLinux, oldNoAgent, oldVMDir := proxyURL, proxySandboxLevel, networkMode, runtimeProfile, linuxMode, noAgent, vmDir
	oldValidation := proxyLastValidation
	t.Cleanup(func() {
		proxyURL = oldURL
		proxySandboxLevel = oldSandbox
		networkMode = oldNet
		runtimeProfile = oldRuntime
		linuxMode = oldLinux
		noAgent = oldNoAgent
		vmDir = oldVMDir
		proxyLastValidation = oldValidation
	})

	vmDir = t.TempDir()
	if err := SaveVMConfig(vmDir, &VMConfig{
		Agent: &VMAgentConfig{
			Platform:  vmAgentPlatformLinux,
			Requested: true,
		},
	}); err != nil {
		t.Fatalf("SaveVMConfig() error = %v", err)
	}

	proxyURL = "http://127.0.0.1:8080"
	proxySandboxLevel = "minimal"
	networkMode = "nat"
	runtimeProfile = "full"
	linuxMode = true
	noAgent = true

	if err := validateProxyFlags(); err != nil {
		t.Fatalf("validateProxyFlags() = %v", err)
	}
	if proxyLastValidation == nil {
		t.Fatal("validateProxyFlags() did not record proxyLastValidation")
	}
	if proxyLastValidation.Capability.Status != proxyCapabilityUnknown {
		t.Fatalf("capability status = %q, want %q", proxyLastValidation.Capability.Status, proxyCapabilityUnknown)
	}
}

func TestLinuxProxyFiles(t *testing.T) {
	spec, err := parseProxySpec("http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	files := linuxProxyFiles(spec)
	envPath := filepath.Join("/etc", "environment.d", proxyEnvFileName)
	profilePath := filepath.Join("/etc", "profile.d", proxyProfileFileName)
	env := files[envPath]
	if !strings.Contains(env, "HTTP_PROXY=http://127.0.0.1:8080") {
		t.Fatalf("env file missing proxy url: %s", env)
	}
	if !strings.Contains(env, "NO_PROXY=localhost,127.0.0.1,::1") {
		t.Fatalf("env file missing no_proxy: %s", env)
	}
	profile := files[profilePath]
	if !strings.Contains(profile, "export HTTP_PROXY='http://127.0.0.1:8080'") {
		t.Fatalf("profile file missing export: %s", profile)
	}
}

func TestParseNetworkSetupProxyStatus(t *testing.T) {
	status, err := parseNetworkSetupProxyStatus("Enabled: Yes\nServer: 127.0.0.1\nPort: 8080\nAuthenticated Proxy Enabled: 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Server != "127.0.0.1" || status.Port != 8080 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestConfigureAndTeardownMacOSProxy(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	prepareMacOSProxyRuntime(rt)

	specRaw := "http://192.168.64.1:8080"
	flags := proxyFlags{
		RawURL:         specRaw,
		NetworkMode:    "nat",
		RuntimeProfile: "full",
		AgentConfig: &VMAgentConfig{
			Platform:  vmAgentPlatformMacOS,
			Requested: true,
		},
	}
	if err := configureGuestProxyOnRuntime(context.Background(), rt, specRaw, flags); err != nil {
		t.Fatalf("configureGuestProxyOnRuntime() = %v", err)
	}
	wantSet := []string{
		"/usr/sbin/networksetup\x00-setwebproxy\x00Wi-Fi\x00192.168.64.1\x008080\x00on",
		"/usr/sbin/networksetup\x00-setsecurewebproxy\x00Wi-Fi\x00192.168.64.1\x008080\x00on",
		"/usr/sbin/networksetup\x00-setwebproxystate\x00Wi-Fi\x00on",
		"/usr/sbin/networksetup\x00-setsecurewebproxystate\x00Wi-Fi\x00on",
	}
	for _, want := range wantSet {
		if !rt.hasUserCall(want) {
			t.Fatalf("missing user command %q; calls=%v", want, rt.userCallList())
		}
	}
	state, err := loadProxyState(rt.vmDir)
	if err != nil {
		t.Fatalf("loadProxyState() = %v", err)
	}
	if state.currentStage() != proxyStateApplied {
		t.Fatalf("proxy stage = %q, want %q", state.currentStage(), proxyStateApplied)
	}
	if err := teardownGuestProxyOnRuntime(context.Background(), rt); err != nil {
		t.Fatalf("teardownGuestProxyOnRuntime() = %v", err)
	}
	wantRestore := []string{
		"/usr/sbin/networksetup\x00-setwebproxystate\x00Wi-Fi\x00off",
		"/usr/sbin/networksetup\x00-setsecurewebproxystate\x00Wi-Fi\x00off",
	}
	for _, want := range wantRestore {
		if !rt.hasUserCall(want) {
			t.Fatalf("missing restore command %q; calls=%v", want, rt.userCallList())
		}
	}
	if _, err := os.Stat(filepath.Join(rt.vmDir, proxyStateFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected proxy state file removed, got err=%v", err)
	}
}

func TestConfigureAndTeardownLinuxProxy(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), true)
	specRaw := "http://127.0.0.1:8080"
	flags := proxyFlags{
		RawURL:      specRaw,
		NetworkMode: "nat",
		Linux:       true,
		AgentConfig: &VMAgentConfig{
			Platform:  vmAgentPlatformLinux,
			Requested: true,
		},
	}
	if err := configureGuestProxyOnRuntime(context.Background(), rt, specRaw, flags); err != nil {
		t.Fatalf("configureGuestProxyOnRuntime() = %v", err)
	}
	for path := range linuxProxyFilesMust(specRaw) {
		if _, ok := rt.file[path]; !ok {
			t.Fatalf("expected file %s to be written", path)
		}
	}
	if err := teardownGuestProxyOnRuntime(context.Background(), rt); err != nil {
		t.Fatalf("teardownGuestProxyOnRuntime() = %v", err)
	}
	for path := range linuxProxyFilesMust(specRaw) {
		if _, ok := rt.file[path]; ok {
			t.Fatalf("expected file %s to be removed", path)
		}
	}
	if _, err := os.Stat(filepath.Join(rt.vmDir, proxyStateFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected proxy state file removed, got err=%v", err)
	}
}

func TestConfigureGuestProxyLinuxRollsBackOnFailure(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), true)
	envPath := filepath.Join("/etc", "environment.d", proxyEnvFileName)
	profilePath := filepath.Join("/etc", "profile.d", proxyProfileFileName)
	rt.file[envPath] = []byte("HTTP_PROXY=http://old.example:8000\n")
	rt.setWriteError(profilePath, errors.New("disk full"))

	err := configureGuestProxyOnRuntime(context.Background(), rt, "http://127.0.0.1:8080", proxyFlags{
		RawURL:      "http://127.0.0.1:8080",
		NetworkMode: "nat",
		Linux:       true,
		AgentConfig: &VMAgentConfig{
			Platform:  vmAgentPlatformLinux,
			Requested: true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "configure guest proxy") {
		t.Fatalf("configureGuestProxyOnRuntime() error = %v, want rollback failure context", err)
	}
	if got := string(rt.file[envPath]); got != "HTTP_PROXY=http://old.example:8000\n" {
		t.Fatalf("env file after rollback = %q, want original content", got)
	}
	if _, ok := rt.file[profilePath]; ok {
		t.Fatalf("profile file still present after rollback: %#v", rt.file[profilePath])
	}
	if _, err := os.Stat(filepath.Join(rt.vmDir, proxyStateFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected proxy state file removed after successful rollback, got err=%v", err)
	}
}

func TestConfigureGuestProxyMacOSRollsBackOnFailure(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	prepareMacOSProxyRuntime(rt)
	rt.setUserExecResult([]string{"/usr/sbin/networksetup", "-setsecurewebproxy", "Wi-Fi", "192.168.64.1", "8080", "on"}, 1, "", "boom")

	err := configureGuestProxyOnRuntime(context.Background(), rt, "http://192.168.64.1:8080", proxyFlags{
		RawURL:         "http://192.168.64.1:8080",
		NetworkMode:    "nat",
		RuntimeProfile: "full",
		AgentConfig: &VMAgentConfig{
			Platform:  vmAgentPlatformMacOS,
			Requested: true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "configure guest proxy") {
		t.Fatalf("configureGuestProxyOnRuntime() error = %v, want rollback failure context", err)
	}
	for _, want := range []string{
		"/usr/sbin/networksetup\x00-setwebproxystate\x00Wi-Fi\x00off",
		"/usr/sbin/networksetup\x00-setsecurewebproxystate\x00Wi-Fi\x00off",
	} {
		if !rt.hasUserCall(want) {
			t.Fatalf("missing rollback command %q; calls=%v", want, rt.userCallList())
		}
	}
	if _, err := os.Stat(filepath.Join(rt.vmDir, proxyStateFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected proxy state file removed after successful rollback, got err=%v", err)
	}
}

func TestTeardownGuestProxyRetainsRollbackStateOnFailure(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), true)
	specRaw := "http://127.0.0.1:8080"
	envPath := filepath.Join("/etc", "environment.d", proxyEnvFileName)
	rt.file[envPath] = []byte("HTTP_PROXY=http://old.example:8000\n")
	flags := proxyFlags{
		RawURL:      specRaw,
		NetworkMode: "nat",
		Linux:       true,
		AgentConfig: &VMAgentConfig{
			Platform:  vmAgentPlatformLinux,
			Requested: true,
		},
	}
	if err := configureGuestProxyOnRuntime(context.Background(), rt, specRaw, flags); err != nil {
		t.Fatalf("configureGuestProxyOnRuntime() = %v", err)
	}

	rt.setWriteError(envPath, errors.New("permission denied"))

	err := teardownGuestProxyOnRuntime(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "restore proxy file") {
		t.Fatalf("teardownGuestProxyOnRuntime() error = %v, want restore error", err)
	}
	state, loadErr := loadProxyState(rt.vmDir)
	if loadErr != nil {
		t.Fatalf("loadProxyState() = %v", loadErr)
	}
	if state.currentStage() != proxyStateRollback {
		t.Fatalf("proxy stage = %q, want %q", state.currentStage(), proxyStateRollback)
	}
}

func TestProxyStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	state := &proxyState{
		Version:  proxyStateVersion,
		Platform: proxyPlatformLinux,
		Stage:    proxyStateApplied,
		Linux: &linuxProxyState{
			Files: []proxyFileBackup{
				{Path: "/etc/environment.d/99-vz-macos-proxy.conf", Present: true, Mode: 0644, Data: []byte("HTTP_PROXY=http://127.0.0.1:8080\n")},
			},
		},
	}
	if err := saveProxyState(dir, state); err != nil {
		t.Fatal(err)
	}
	got, err := loadProxyState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Platform != proxyPlatformLinux || len(got.Linux.Files) != 1 || got.currentStage() != proxyStateApplied {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestProxyValidationErrorsAreActionable(t *testing.T) {
	err := validateProxyFlagsFor(proxyFlags{
		RawURL:       "http://127.0.0.1:8080",
		SandboxLevel: "strict",
		NetworkMode:  "nat",
	})
	if err == nil || !strings.Contains(err.Error(), "strict") {
		t.Fatalf("expected strict error, got %v", err)
	}
}

func TestProxyRecoveryLines(t *testing.T) {
	dir := t.TempDir()
	if err := saveProxyState(dir, &proxyState{
		Version:  proxyStateVersion,
		Platform: proxyPlatformLinux,
		Stage:    proxyStateRollback,
		Spec:     "http://127.0.0.1:8080",
	}); err != nil {
		t.Fatalf("saveProxyState() error = %v", err)
	}
	lines := proxyRecoveryLines(dir)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"vm dir: " + dir,
		"state file: " + proxyStatePath(dir),
		"vz-macos -vm " + filepath.Base(dir) + " run -proxy http://127.0.0.1:8080",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("proxyRecoveryLines() = %q, want substring %q", joined, want)
		}
	}
}

func linuxProxyFilesMust(raw string) map[string]string {
	spec, _ := parseProxySpec(raw)
	return linuxProxyFiles(spec)
}

type fakeProxyRuntime struct {
	linux       bool
	vmDir       string
	mu          sync.Mutex
	file        map[string][]byte
	writeErr    map[string]error
	userCallLog []string
	daemonCalls []string
	userExec    map[string]*pb.ExecResponse
	daemonExec  map[string]*pb.ExecResponse
}

func newFakeProxyRuntime(vmDir string, linux bool) *fakeProxyRuntime {
	return &fakeProxyRuntime{
		linux:      linux,
		vmDir:      vmDir,
		file:       make(map[string][]byte),
		writeErr:   make(map[string]error),
		userExec:   make(map[string]*pb.ExecResponse),
		daemonExec: make(map[string]*pb.ExecResponse),
	}
}

func (f *fakeProxyRuntime) VMDir() string { return f.vmDir }

func (f *fakeProxyRuntime) IsLinux() bool { return f.linux }

func (f *fakeProxyRuntime) Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.daemonCalls = append(f.daemonCalls, commandKey(args))
	if len(args) >= 3 && args[0] == "/bin/rm" && args[1] == "-f" {
		delete(f.file, args[2])
	}
	if resp, ok := f.daemonExec[commandKey(args)]; ok {
		return resp, nil
	}
	return &pb.ExecResponse{ExitCode: 0}, nil
}

func (f *fakeProxyRuntime) UserExec(ctx context.Context, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCallLog = append(f.userCallLog, commandKey(args))
	if resp, ok := f.userExec[commandKey(args)]; ok {
		return resp, nil
	}
	return &pb.ExecResponse{ExitCode: 0}, nil
}

func (f *fakeProxyRuntime) ReadFile(ctx context.Context, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.file[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (f *fakeProxyRuntime) WriteFile(ctx context.Context, path string, data []byte, mode uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.writeErr[path]; err != nil {
		return err
	}
	f.file[path] = append([]byte(nil), data...)
	return nil
}

func (f *fakeProxyRuntime) setUserExecResponse(args []string, stdout string) {
	f.setUserExecResult(args, 0, stdout, "")
}

func (f *fakeProxyRuntime) setUserExecResult(args []string, exitCode int32, stdout, stderr string) {
	f.userExec[commandKey(args)] = &pb.ExecResponse{ExitCode: exitCode, Stdout: []byte(stdout), Stderr: []byte(stderr)}
}

func (f *fakeProxyRuntime) setWriteError(path string, err error) {
	f.writeErr[path] = err
}

func (f *fakeProxyRuntime) hasUserCall(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.userCallLog {
		if call == key {
			return true
		}
	}
	return false
}

func (f *fakeProxyRuntime) userCallList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.userCallLog...)
}

func prepareMacOSProxyRuntime(rt *fakeProxyRuntime) {
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-listallnetworkservices"}, "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n* Disabled Service\n")
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-getwebproxy", "Wi-Fi"}, "Enabled: No\nServer: \nPort: 0\n")
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-getsecurewebproxy", "Wi-Fi"}, "Enabled: No\nServer: \nPort: 0\n")
}

func commandKey(args []string) string {
	return strings.Join(args, "\x00")
}

func Example_parseProxySpec() {
	spec, _ := parseProxySpec("http://127.0.0.1:8080")
	fmt.Println(spec.endpoint())
	// Output: 127.0.0.1:8080
}
