package main

import (
	"context"
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

func TestValidateProxyFlagsFor(t *testing.T) {
	tests := []struct {
		name    string
		cfg     proxyFlags
		wantErr string
	}{
		{
			name: "valid macos",
			cfg: proxyFlags{
				RawURL:         "http://127.0.0.1:8080",
				NetworkMode:    "nat",
				RuntimeProfile: "full",
				AgentCapable:   true,
			},
		},
		{
			name: "strict sandbox",
			cfg: proxyFlags{
				RawURL:       "http://127.0.0.1:8080",
				SandboxLevel: "strict",
				NetworkMode:  "nat",
				AgentCapable: true,
			},
			wantErr: "strict",
		},
		{
			name: "network none",
			cfg: proxyFlags{
				RawURL:       "http://127.0.0.1:8080",
				NetworkMode:  "none",
				AgentCapable: true,
			},
			wantErr: "network none",
		},
		{
			name: "minimal runtime",
			cfg: proxyFlags{
				RawURL:         "http://127.0.0.1:8080",
				NetworkMode:    "nat",
				RuntimeProfile: "minimal",
				AgentCapable:   true,
			},
			wantErr: "minimal",
		},
		{
			name: "missing agent",
			cfg: proxyFlags{
				RawURL:       "http://127.0.0.1:8080",
				NetworkMode:  "nat",
				AgentCapable: false,
			},
			wantErr: "agent-capable",
		},
		{
			name: "bad url",
			cfg: proxyFlags{
				RawURL:       "http://127.0.0.1",
				NetworkMode:  "nat",
				AgentCapable: true,
			},
			wantErr: "missing a port",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProxyFlagsFor(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateProxyFlagsFor() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantErr)) {
				t.Fatalf("validateProxyFlagsFor() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestProxyAgentCapableLinuxIgnoresNoAgentFlag(t *testing.T) {
	oldLinux := linuxMode
	oldNoAgent := noAgent
	oldSandboxLevel := sandboxLevel
	t.Cleanup(func() {
		linuxMode = oldLinux
		noAgent = oldNoAgent
		sandboxLevel = oldSandboxLevel
	})

	linuxMode = true
	noAgent = true
	sandboxLevel = ""
	if !proxyAgentCapable() {
		t.Fatal("proxyAgentCapable() = false, want true for an existing Linux VM run")
	}

	sandboxLevel = "strict"
	if proxyAgentCapable() {
		t.Fatal("proxyAgentCapable() = true, want false when strict sandbox disables vsock")
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
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-listallnetworkservices"}, "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n* Disabled Service\n")
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-getwebproxy", "Wi-Fi"}, "Enabled: No\nServer: \nPort: 0\n")
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-getsecurewebproxy", "Wi-Fi"}, "Enabled: No\nServer: \nPort: 0\n")

	specRaw := "http://192.168.64.1:8080"
	flags := proxyFlags{
		RawURL:         specRaw,
		NetworkMode:    "nat",
		RuntimeProfile: "full",
		AgentCapable:   true,
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
	if _, err := os.Stat(filepath.Join(rt.vmDir, proxyStateFileName)); err != nil {
		t.Fatalf("proxy state file missing: %v", err)
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
		RawURL:       specRaw,
		NetworkMode:  "nat",
		Linux:        true,
		AgentCapable: true,
	}
	if err := configureGuestProxyOnRuntime(context.Background(), rt, specRaw, flags); err != nil {
		t.Fatalf("configureGuestProxyOnRuntime() = %v", err)
	}
	for path := range linuxProxyFilesMust(rt.vmDir, specRaw) {
		if _, ok := rt.file[path]; !ok {
			t.Fatalf("expected file %s to be written", path)
		}
	}
	if err := teardownGuestProxyOnRuntime(context.Background(), rt); err != nil {
		t.Fatalf("teardownGuestProxyOnRuntime() = %v", err)
	}
	for path := range linuxProxyFilesMust(rt.vmDir, specRaw) {
		if _, ok := rt.file[path]; ok {
			t.Fatalf("expected file %s to be removed", path)
		}
	}
	if _, err := os.Stat(filepath.Join(rt.vmDir, proxyStateFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected proxy state file removed, got err=%v", err)
	}
}

func linuxProxyFilesMust(vmDir, raw string) map[string]string {
	spec, _ := parseProxySpec(raw)
	return linuxProxyFiles(spec)
}

type fakeProxyRuntime struct {
	linux       bool
	vmDir       string
	mu          sync.Mutex
	file        map[string][]byte
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
	f.file[path] = append([]byte(nil), data...)
	return nil
}

func (f *fakeProxyRuntime) setUserExecResponse(args []string, stdout string) {
	f.userExec[commandKey(args)] = &pb.ExecResponse{ExitCode: 0, Stdout: []byte(stdout)}
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

func commandKey(args []string) string {
	return strings.Join(args, "\x00")
}

func TestProxyValidationUsesGlobals(t *testing.T) {
	oldURL, oldSandbox, oldNet, oldRuntime, oldLinux, oldProvision, oldAgent := proxyURL, proxySandboxLevel, networkMode, runtimeProfile, linuxMode, provisionUser, noAgent
	oldVMDir := vmDir
	t.Cleanup(func() {
		proxyURL = oldURL
		proxySandboxLevel = oldSandbox
		networkMode = oldNet
		runtimeProfile = oldRuntime
		linuxMode = oldLinux
		provisionUser = oldProvision
		noAgent = oldAgent
		vmDir = oldVMDir
	})
	vmDir = t.TempDir()
	proxyURL = "http://127.0.0.1:8080"
	networkMode = "nat"
	runtimeProfile = "full"
	linuxMode = false
	provisionUser = "testuser"
	proxySandboxLevel = "minimal"
	if err := validateProxyFlags(); err != nil {
		t.Fatalf("validateProxyFlags() = %v", err)
	}
}

func TestProxyStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	state := &proxyState{
		Version:  proxyStateVersion,
		Platform: proxyPlatformLinux,
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
	if got.Platform != proxyPlatformLinux || len(got.Linux.Files) != 1 {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestProxyValidationErrorsAreActionable(t *testing.T) {
	err := validateProxyFlagsFor(proxyFlags{
		RawURL:       "http://127.0.0.1:8080",
		SandboxLevel: "strict",
		NetworkMode:  "nat",
		AgentCapable: true,
	})
	if err == nil || !strings.Contains(err.Error(), "strict") {
		t.Fatalf("expected strict error, got %v", err)
	}
}

func Example_parseProxySpec() {
	spec, _ := parseProxySpec("http://127.0.0.1:8080")
	fmt.Println(spec.endpoint())
	// Output: 127.0.0.1:8080
}
