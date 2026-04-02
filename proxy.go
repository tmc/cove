package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pb "github.com/tmc/vz-macos/proto/agentpb"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

var proxyURL string
var proxySandboxLevel string
var proxyLastValidation *proxyValidation

const (
	proxyStateFileName   = ".proxy-state.json"
	proxyPlatformMacOS   = "macos"
	proxyPlatformLinux   = "linux"
	proxyStateVersion    = 2
	proxyEnvFileName     = "99-vz-macos-proxy.conf"
	proxyProfileFileName = "99-vz-macos-proxy.sh"
	proxyStateCaptured   = "captured"
	proxyStateApplied    = "applied"
	proxyStateRollback   = "rollback_pending"

	proxyCapabilityReady       = "ready"
	proxyCapabilityUnknown     = "unknown"
	proxyCapabilityUnavailable = "unavailable"

	proxyCapabilityRuntime = "runtime"
	proxyCapabilityConfig  = "config"
	proxyCapabilityNone    = "none"
)

const (
	proxyRuntimeWaitTimeout     = 2 * time.Minute
	proxyRuntimeTeardownTimeout = 15 * time.Second
	proxyRuntimePollInterval    = 2 * time.Second
)

type proxySpec struct {
	Raw    string
	URL    *url.URL
	Scheme string
	Host   string
	Port   int
}

func (s proxySpec) endpoint() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}

func (s proxySpec) canonicalURL() string {
	return (&url.URL{
		Scheme: "http",
		Host:   s.endpoint(),
	}).String()
}

type proxyFlags struct {
	RawURL          string
	SandboxLevel    string
	NetworkMode     string
	RuntimeProfile  string
	Linux           bool
	VMDir           string
	Running         bool
	RunningKnown    bool
	InjectSucceeded bool
	AgentConfig     *VMAgentConfig
	CapabilityProbe func(context.Context, proxyFlags) proxyCapability
}

type proxyValidation struct {
	Spec       proxySpec
	Capability proxyCapability
}

type proxyCapability struct {
	Status string
	Source string
	Reason string
}

type proxyState struct {
	Version   int              `json:"version"`
	Platform  string           `json:"platform"`
	Stage     string           `json:"stage,omitempty"`
	Spec      string           `json:"spec,omitempty"`
	AppliedAt time.Time        `json:"applied_at,omitempty"`
	Mac       *macOSProxyState `json:"mac,omitempty"`
	Linux     *linuxProxyState `json:"linux,omitempty"`
}

type macOSProxyState struct {
	Services []macOSProxyServiceState `json:"services"`
}

type macOSProxyServiceState struct {
	Name          string `json:"name"`
	WebEnabled    bool   `json:"web_enabled"`
	WebServer     string `json:"web_server,omitempty"`
	WebPort       int    `json:"web_port,omitempty"`
	SecureEnabled bool   `json:"secure_enabled"`
	SecureServer  string `json:"secure_server,omitempty"`
	SecurePort    int    `json:"secure_port,omitempty"`
}

type linuxProxyState struct {
	Files []proxyFileBackup `json:"files"`
}

type proxyFileBackup struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Mode    uint32 `json:"mode,omitempty"`
	Data    []byte `json:"data,omitempty"`
}

type proxyRuntime interface {
	VMDir() string
	IsLinux() bool
	Exec(context.Context, []string, map[string]string, string) (*pb.ExecResponse, error)
	UserExec(context.Context, []string, map[string]string, string) (*pb.ExecResponse, error)
	ReadFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, []byte, uint32) error
}

type proxyRuntimeClient struct {
	server *ControlServer
	linux  bool
}

func (r *proxyRuntimeClient) VMDir() string {
	if r == nil || r.server == nil {
		return vmDir
	}
	if dir := r.server.effectiveVMDir(); dir != "" {
		return dir
	}
	return vmDir
}

func (r *proxyRuntimeClient) IsLinux() bool {
	if r == nil {
		return linuxMode
	}
	return r.linux
}

func (r *proxyRuntimeClient) Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	if r == nil || r.server == nil {
		return nil, fmt.Errorf("proxy runtime unavailable")
	}
	agent, err := r.server.getAgent()
	if err != nil {
		return nil, err
	}
	return agent.Exec(ctx, args, env, workDir)
}

func (r *proxyRuntimeClient) UserExec(ctx context.Context, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	if r == nil || r.server == nil {
		return nil, fmt.Errorf("proxy runtime unavailable")
	}
	agent, err := r.server.getUserAgent()
	if err != nil {
		return nil, err
	}
	return agent.UserExec(ctx, args, env, workDir)
}

func (r *proxyRuntimeClient) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if r == nil || r.server == nil {
		return nil, fmt.Errorf("proxy runtime unavailable")
	}
	agent, err := r.server.getAgent()
	if err != nil {
		return nil, err
	}
	return agent.ReadFile(ctx, path)
}

func (r *proxyRuntimeClient) WriteFile(ctx context.Context, path string, data []byte, mode uint32) error {
	if r == nil || r.server == nil {
		return fmt.Errorf("proxy runtime unavailable")
	}
	agent, err := r.server.getAgent()
	if err != nil {
		return err
	}
	return agent.WriteFile(ctx, path, data, mode)
}

func validateProxyFlags() error {
	proxyLastValidation = nil
	if strings.TrimSpace(proxyURL) == "" {
		return nil
	}
	cfg, err := currentProxyFlags()
	if err != nil {
		return err
	}
	validation, err := resolveProxyValidationFor(context.Background(), cfg)
	if err != nil {
		return err
	}
	proxyLastValidation = validation
	return nil
}

func validateProxyFlagsFor(cfg proxyFlags) error {
	_, err := resolveProxyValidationFor(context.Background(), cfg)
	return err
}

func currentProxyFlags() (proxyFlags, error) {
	cfg := proxyFlags{
		RawURL:          proxyURL,
		SandboxLevel:    proxySandboxLevel,
		NetworkMode:     networkMode,
		RuntimeProfile:  runtimeProfile,
		Linux:           linuxMode,
		VMDir:           vmDir,
		RunningKnown:    strings.TrimSpace(vmDir) != "",
		InjectSucceeded: didInjectSucceed(),
	}
	if cfg.RunningKnown {
		cfg.Running = isVMRunningAt(vmDir)
	}
	vmcfg, err := LoadVMConfig(vmDir)
	if err != nil {
		return cfg, fmt.Errorf("load vm config: %w", err)
	}
	cfg.AgentConfig = cloneVMAgentConfig(vmcfg.Agent)
	return cfg, nil
}

func resolveProxyValidationFor(ctx context.Context, cfg proxyFlags) (*proxyValidation, error) {
	if strings.TrimSpace(cfg.RawURL) == "" {
		return &proxyValidation{}, nil
	}
	if strings.TrimSpace(cfg.SandboxLevel) == "strict" {
		return nil, fmt.Errorf("-sandbox-level strict does not allow -proxy; use minimal or omit -proxy")
	}
	if strings.TrimSpace(cfg.NetworkMode) == "none" {
		return nil, fmt.Errorf("-network none does not allow -proxy")
	}
	if !cfg.Linux && strings.TrimSpace(cfg.RuntimeProfile) == "minimal" {
		return nil, fmt.Errorf("-runtime-profile minimal disables vsock; use full for -proxy")
	}
	if !proxyVsockAvailable(cfg) {
		return nil, fmt.Errorf("-proxy requires vsock availability")
	}
	spec, err := parseProxySpec(cfg.RawURL)
	if err != nil {
		return nil, err
	}
	capability := resolveProxyCapability(ctx, cfg)
	if capability.Status == proxyCapabilityUnavailable {
		return nil, proxyCapabilityError(cfg, capability)
	}
	return &proxyValidation{Spec: spec, Capability: capability}, nil
}

func proxyVsockAvailable(cfg proxyFlags) bool {
	if strings.TrimSpace(cfg.SandboxLevel) == "strict" {
		return false
	}
	if !cfg.Linux && strings.TrimSpace(cfg.RuntimeProfile) == "minimal" {
		return false
	}
	return true
}

func resolveProxyCapability(ctx context.Context, cfg proxyFlags) proxyCapability {
	if cfg.RunningKnown && cfg.Running {
		probe := cfg.CapabilityProbe
		if probe == nil {
			probe = probeProxyRuntimeCapability
		}
		capability := probe(ctx, cfg)
		if capability.Status == proxyCapabilityReady && strings.TrimSpace(cfg.VMDir) != "" {
			_ = markVMAgentVerified(cfg.VMDir, proxyPlatformForFlags(cfg), vmAgentSourceRuntime, time.Now())
		}
		return capability
	}

	if agent := cloneVMAgentConfig(cfg.AgentConfig); agent != nil {
		if agent.Platform == "" || agent.Platform == proxyPlatformForFlags(cfg) {
			if agent.Verified {
				return proxyCapability{Status: proxyCapabilityReady, Source: proxyCapabilityConfig, Reason: "agent_verified"}
			}
			if cfg.Linux {
				if agent.Requested {
					return proxyCapability{Status: proxyCapabilityUnknown, Source: proxyCapabilityConfig, Reason: "linux_agent_requested"}
				}
				return proxyCapability{Status: proxyCapabilityUnavailable, Source: proxyCapabilityConfig, Reason: "linux_agent_requested_false"}
			}
			if agent.Requested {
				return proxyCapability{Status: proxyCapabilityReady, Source: proxyCapabilityConfig, Reason: "macos_agent_requested"}
			}
		}
	}

	if !cfg.Linux && cfg.InjectSucceeded {
		return proxyCapability{Status: proxyCapabilityReady, Source: proxyCapabilityConfig, Reason: "inject_succeeded"}
	}
	if cfg.Linux {
		return proxyCapability{Status: proxyCapabilityUnknown, Source: proxyCapabilityNone, Reason: "linux_agent_unknown"}
	}
	return proxyCapability{Status: proxyCapabilityUnknown, Source: proxyCapabilityNone, Reason: "macos_agent_unknown"}
}

func proxyPlatformForFlags(cfg proxyFlags) string {
	if cfg.Linux {
		return proxyPlatformLinux
	}
	return proxyPlatformMacOS
}

func probeProxyRuntimeCapability(ctx context.Context, cfg proxyFlags) proxyCapability {
	sock := GetControlSocketPathForVM(cfg.VMDir)
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	if cfg.Linux {
		resp, err := ctlSendRequest(sock, &controlpb.ControlRequest{Type: "agent-ping"}, timeout, "agent-ping")
		if err == nil && resp.Success {
			return proxyCapability{Status: proxyCapabilityReady, Source: proxyCapabilityRuntime, Reason: "linux_agent_ping"}
		}
		return proxyCapability{Status: proxyCapabilityUnavailable, Source: proxyCapabilityRuntime, Reason: "linux_agent_probe_failed"}
	}
	resp, err := ctlSendRequest(sock, &controlpb.ControlRequest{
		Type: "agent-user-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: []string{"/usr/bin/true"}},
		},
	}, timeout, "agent-user-exec")
	if err == nil && resp.Success {
		if result := resp.GetAgentExecResult(); result == nil || result.GetExitCode() == 0 {
			return proxyCapability{Status: proxyCapabilityReady, Source: proxyCapabilityRuntime, Reason: "macos_user_agent_ready"}
		}
	}
	return proxyCapability{Status: proxyCapabilityUnavailable, Source: proxyCapabilityRuntime, Reason: "macos_user_agent_unavailable"}
}

func proxyCapabilityError(cfg proxyFlags, capability proxyCapability) error {
	if cfg.Linux {
		switch capability.Reason {
		case "linux_agent_requested_false":
			return fmt.Errorf("-proxy requires vz-agent on Linux guests; run 'vz-macos provision-agent' or reinstall without -no-agent")
		default:
			return fmt.Errorf("-proxy requires vz-agent on Linux guests; runtime probe failed against the running VM")
		}
	}
	switch capability.Reason {
	case "macos_user_agent_unavailable":
		return fmt.Errorf("-proxy requires the macOS user agent for networksetup; log in graphically or provision the guest agent")
	default:
		return fmt.Errorf("-proxy requires a guest-agent-capable macOS VM; provision the guest or use an existing agent-capable VM")
	}
}

func parseProxySpec(raw string) (proxySpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return proxySpec{}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return proxySpec{}, fmt.Errorf("parse proxy url: %w", err)
	}
	if u.User != nil {
		return proxySpec{}, fmt.Errorf("proxy url credentials are not supported")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "http", "https":
	default:
		return proxySpec{}, fmt.Errorf("unsupported proxy scheme %q (use http or https)", u.Scheme)
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return proxySpec{}, fmt.Errorf("proxy url %q is missing a host", raw)
	}
	portStr := strings.TrimSpace(u.Port())
	if portStr == "" {
		return proxySpec{}, fmt.Errorf("proxy url %q is missing a port", raw)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return proxySpec{}, fmt.Errorf("parse proxy port %q: %w", portStr, err)
	}
	if port < 1 || port > 65535 {
		return proxySpec{}, fmt.Errorf("proxy port %d out of range", port)
	}
	if u.Path != "" && u.Path != "/" {
		return proxySpec{}, fmt.Errorf("proxy url %q must not include a path", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return proxySpec{}, fmt.Errorf("proxy url %q must not include a query or fragment", raw)
	}
	return proxySpec{
		Raw:    raw,
		URL:    u,
		Scheme: scheme,
		Host:   host,
		Port:   port,
	}, nil
}

func configureGuestProxy(ctx context.Context, cs *ControlServer, rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	if cs == nil {
		return fmt.Errorf("proxy runtime unavailable")
	}
	flags, err := currentProxyFlags()
	if err != nil {
		return err
	}
	flags.RawURL = rawURL
	flags.Running = false
	flags.RunningKnown = false
	rt := &proxyRuntimeClient{server: cs, linux: linuxMode}
	return configureGuestProxyOnRuntime(ctx, rt, rawURL, flags)
}

func configureRequestedProxy(cs *ControlServer) error {
	if strings.TrimSpace(proxyURL) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyRuntimeWaitTimeout)
	defer cancel()
	return configureGuestProxy(ctx, cs, proxyURL)
}

func configureRequestedProxyAfterBoot(cs *ControlServer) {
	if strings.TrimSpace(proxyURL) == "" || cs == nil {
		return
	}
	go func() {
		if proxyLastValidation != nil && proxyLastValidation.Capability.Status == proxyCapabilityUnknown {
			fmt.Println("Proxy preflight unknown: proceeding to runtime probe.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), proxyRuntimeWaitTimeout)
		defer cancel()
		if err := waitForProxyRuntime(ctx, cs); err != nil {
			fmt.Printf("warning: configure guest proxy: %v\n", err)
			return
		}
		if err := configureGuestProxy(ctx, cs, proxyURL); err != nil {
			fmt.Printf("warning: configure guest proxy: %v\n", err)
			return
		}
		fmt.Printf("Guest proxy configured: %s\n", proxyURL)
	}()
}

func teardownGuestProxy(ctx context.Context, cs *ControlServer) error {
	if cs == nil {
		return nil
	}
	rt := &proxyRuntimeClient{server: cs, linux: linuxMode}
	return teardownGuestProxyOnRuntime(ctx, rt)
}

func teardownRequestedProxy(cs *ControlServer) {
	if strings.TrimSpace(proxyURL) == "" || cs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyRuntimeTeardownTimeout)
	defer cancel()
	if err := teardownGuestProxy(ctx, cs); err != nil {
		printProxyRestoreFailure(cs.effectiveVMDir(), err)
	}
}

func waitForProxyRuntime(ctx context.Context, cs *ControlServer) error {
	if cs == nil {
		return fmt.Errorf("proxy runtime unavailable")
	}
	ticker := time.NewTicker(proxyRuntimePollInterval)
	defer ticker.Stop()
	for {
		var err error
		if linuxMode {
			_, err = cs.getAgent()
		} else {
			_, err = cs.getUserAgent()
		}
		if err == nil {
			if markErr := markVMAgentVerified(cs.effectiveVMDir(), currentVMAgentPlatform(), vmAgentSourceRuntime, time.Now()); markErr != nil && verbose {
				fmt.Printf("warning: record guest agent capability: %v\n", markErr)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("guest agent not ready before proxy setup: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func configureGuestProxyOnRuntime(ctx context.Context, rt proxyRuntime, rawURL string, flags proxyFlags) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	flags.RawURL = rawURL
	validation, err := resolveProxyValidationFor(ctx, flags)
	if err != nil {
		return err
	}
	spec := validation.Spec

	state, err := captureProxyState(ctx, rt)
	if err != nil {
		return err
	}
	state.Version = proxyStateVersion
	state.Stage = proxyStateCaptured
	if rt.IsLinux() {
		state.Platform = proxyPlatformLinux
	} else {
		state.Platform = proxyPlatformMacOS
	}
	state.Spec = rawURL
	state.AppliedAt = time.Now().UTC()
	if err := saveProxyState(rt.VMDir(), state); err != nil {
		return err
	}
	state.Stage = proxyStateRollback
	if err := saveProxyState(rt.VMDir(), state); err != nil {
		return err
	}

	if rt.IsLinux() {
		err = applyLinuxProxy(ctx, rt, state.Linux, spec)
	} else {
		_, err = applyMacOSProxy(ctx, rt, state.Mac, spec)
	}
	if err != nil {
		if rollbackErr := restoreProxyState(ctx, rt, state); rollbackErr != nil {
			if saveErr := saveProxyState(rt.VMDir(), state); saveErr != nil && verbose {
				fmt.Printf("warning: persist proxy rollback state: %v\n", saveErr)
			}
			return fmt.Errorf("configure guest proxy: %w (rollback failed: %v; state retained at %s)", err, rollbackErr, proxyStatePath(rt.VMDir()))
		}
		if removeErr := os.Remove(proxyStatePath(rt.VMDir())); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("configure guest proxy: %w (cleanup state: %v)", err, removeErr)
		}
		return fmt.Errorf("configure guest proxy: %w", err)
	}

	state.Stage = proxyStateApplied
	if err := saveProxyState(rt.VMDir(), state); err != nil {
		return err
	}
	return nil
}

func teardownGuestProxyOnRuntime(ctx context.Context, rt proxyRuntime) error {
	state, err := loadProxyState(rt.VMDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	switch state.currentStage() {
	case proxyStateCaptured:
		if err := os.Remove(proxyStatePath(rt.VMDir())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove proxy state: %w", err)
		}
		return nil
	case proxyStateApplied, proxyStateRollback:
	default:
		return fmt.Errorf("unknown proxy state stage %q", state.Stage)
	}

	if err := restoreProxyState(ctx, rt, state); err != nil {
		state.Stage = proxyStateRollback
		if saveErr := saveProxyState(rt.VMDir(), state); saveErr != nil && verbose {
			fmt.Printf("warning: persist proxy rollback state: %v\n", saveErr)
		}
		return err
	}
	if err := os.Remove(proxyStatePath(rt.VMDir())); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove proxy state: %w", err)
	}
	return nil
}

func (s *proxyState) currentStage() string {
	if s == nil || s.Stage == "" {
		return proxyStateApplied
	}
	return s.Stage
}

func restoreProxyState(ctx context.Context, rt proxyRuntime, state *proxyState) error {
	switch state.Platform {
	case proxyPlatformLinux:
		if err := restoreLinuxProxy(ctx, rt, state.Linux); err != nil {
			return err
		}
	case proxyPlatformMacOS:
		if err := restoreMacOSProxy(ctx, rt, state.Mac); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown proxy state platform %q", state.Platform)
	}
	return nil
}

func captureProxyState(ctx context.Context, rt proxyRuntime) (*proxyState, error) {
	state := &proxyState{}
	if rt.IsLinux() {
		linuxState, err := captureLinuxProxyState(ctx, rt)
		if err != nil {
			return nil, err
		}
		state.Linux = linuxState
		return state, nil
	}
	macState, err := captureMacOSProxyState(ctx, rt)
	if err != nil {
		return nil, err
	}
	state.Mac = macState
	return state, nil
}

func captureMacOSProxyState(ctx context.Context, rt proxyRuntime) (*macOSProxyState, error) {
	services, err := listMacOSNetworkServices(ctx, rt)
	if err != nil {
		return nil, err
	}
	state := &macOSProxyState{}
	for _, service := range services {
		web, err := getMacOSProxyStatus(ctx, rt, service, "web")
		if err != nil {
			return nil, err
		}
		secure, err := getMacOSProxyStatus(ctx, rt, service, "secure")
		if err != nil {
			return nil, err
		}
		state.Services = append(state.Services, macOSProxyServiceState{
			Name:          service,
			WebEnabled:    web.Enabled,
			WebServer:     web.Server,
			WebPort:       web.Port,
			SecureEnabled: secure.Enabled,
			SecureServer:  secure.Server,
			SecurePort:    secure.Port,
		})
	}
	return state, nil
}

func captureLinuxProxyState(ctx context.Context, rt proxyRuntime) (*linuxProxyState, error) {
	files := []string{
		filepath.Join("/etc", "environment.d", proxyEnvFileName),
		filepath.Join("/etc", "profile.d", proxyProfileFileName),
	}
	state := &linuxProxyState{}
	for _, path := range files {
		backup, err := readProxyFileBackup(ctx, rt, path)
		if err != nil {
			return nil, err
		}
		state.Files = append(state.Files, backup)
	}
	return state, nil
}

func applyMacOSProxy(ctx context.Context, rt proxyRuntime, state *macOSProxyState, spec proxySpec) (*macOSProxyState, error) {
	if state == nil {
		state = &macOSProxyState{}
	}
	if len(state.Services) == 0 {
		services, err := listMacOSNetworkServices(ctx, rt)
		if err != nil {
			return nil, err
		}
		for _, service := range services {
			web, err := getMacOSProxyStatus(ctx, rt, service, "web")
			if err != nil {
				return nil, err
			}
			secure, err := getMacOSProxyStatus(ctx, rt, service, "secure")
			if err != nil {
				return nil, err
			}
			state.Services = append(state.Services, macOSProxyServiceState{
				Name:          service,
				WebEnabled:    web.Enabled,
				WebServer:     web.Server,
				WebPort:       web.Port,
				SecureEnabled: secure.Enabled,
				SecureServer:  secure.Server,
				SecurePort:    secure.Port,
			})
			if err := setMacOSProxyService(ctx, rt, service, spec); err != nil {
				return nil, err
			}
		}
		return state, nil
	}
	for _, service := range state.Services {
		if err := setMacOSProxyService(ctx, rt, service.Name, spec); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func restoreMacOSProxy(ctx context.Context, rt proxyRuntime, state *macOSProxyState) error {
	if state == nil {
		return nil
	}
	for _, service := range state.Services {
		if err := restoreMacOSProxyService(ctx, rt, service); err != nil {
			return err
		}
	}
	return nil
}

func applyLinuxProxy(ctx context.Context, rt proxyRuntime, state *linuxProxyState, spec proxySpec) error {
	if state == nil {
		state = &linuxProxyState{}
	}
	files := linuxProxyFiles(spec)
	for _, file := range state.Files {
		content, ok := files[file.Path]
		if !ok {
			continue
		}
		if err := rt.WriteFile(ctx, file.Path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write proxy file %s: %w", file.Path, err)
		}
	}
	return nil
}

func restoreLinuxProxy(ctx context.Context, rt proxyRuntime, state *linuxProxyState) error {
	if state == nil {
		return nil
	}
	for _, file := range state.Files {
		if !file.Present {
			if err := removeProxyFile(ctx, rt, file.Path); err != nil {
				return err
			}
			continue
		}
		if err := rt.WriteFile(ctx, file.Path, file.Data, file.Mode); err != nil {
			return fmt.Errorf("restore proxy file %s: %w", file.Path, err)
		}
	}
	return nil
}

func linuxProxyFiles(spec proxySpec) map[string]string {
	url := spec.canonicalURL()
	env := strings.Join([]string{
		"HTTP_PROXY=" + url,
		"http_proxy=" + url,
		"HTTPS_PROXY=" + url,
		"https_proxy=" + url,
		"ALL_PROXY=" + url,
		"all_proxy=" + url,
		"NO_PROXY=localhost,127.0.0.1,::1",
		"no_proxy=localhost,127.0.0.1,::1",
	}, "\n") + "\n"
	profile := strings.Join([]string{
		"export HTTP_PROXY=" + shellQuote(url),
		"export http_proxy=" + shellQuote(url),
		"export HTTPS_PROXY=" + shellQuote(url),
		"export https_proxy=" + shellQuote(url),
		"export ALL_PROXY=" + shellQuote(url),
		"export all_proxy=" + shellQuote(url),
		"export NO_PROXY=" + shellQuote("localhost,127.0.0.1,::1"),
		"export no_proxy=" + shellQuote("localhost,127.0.0.1,::1"),
	}, "\n") + "\n"
	return map[string]string{
		filepath.Join("/etc", "environment.d", proxyEnvFileName): env,
		filepath.Join("/etc", "profile.d", proxyProfileFileName): profile,
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

type proxyServiceStatus struct {
	Enabled bool
	Server  string
	Port    int
}

func listMacOSNetworkServices(ctx context.Context, rt proxyRuntime) ([]string, error) {
	out, err := runProxyUserCommand(ctx, rt, "/usr/sbin/networksetup", "-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var services []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		if strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("no enabled network services found")
	}
	return services, nil
}

func getMacOSProxyStatus(ctx context.Context, rt proxyRuntime, service, kind string) (proxyServiceStatus, error) {
	var args []string
	switch kind {
	case "web":
		args = []string{"/usr/sbin/networksetup", "-getwebproxy", service}
	case "secure":
		args = []string{"/usr/sbin/networksetup", "-getsecurewebproxy", service}
	default:
		return proxyServiceStatus{}, fmt.Errorf("unknown proxy kind %q", kind)
	}
	out, err := runProxyUserCommand(ctx, rt, args...)
	if err != nil {
		return proxyServiceStatus{}, err
	}
	return parseNetworkSetupProxyStatus(out)
}

func setMacOSProxyService(ctx context.Context, rt proxyRuntime, service string, spec proxySpec) error {
	port := strconv.Itoa(spec.Port)
	for _, args := range [][]string{
		{"/usr/sbin/networksetup", "-setwebproxy", service, spec.Host, port, "on"},
		{"/usr/sbin/networksetup", "-setsecurewebproxy", service, spec.Host, port, "on"},
		{"/usr/sbin/networksetup", "-setwebproxystate", service, "on"},
		{"/usr/sbin/networksetup", "-setsecurewebproxystate", service, "on"},
	} {
		if _, err := runProxyUserCommand(ctx, rt, args...); err != nil {
			return fmt.Errorf("configure %s: %w", service, err)
		}
	}
	return nil
}

func restoreMacOSProxyService(ctx context.Context, rt proxyRuntime, state macOSProxyServiceState) error {
	restore := func(kind string, enabled bool, server string, port int) error {
		if enabled {
			if server == "" || port == 0 {
				return fmt.Errorf("restore %s proxy for %s: missing state", kind, state.Name)
			}
			portStr := strconv.Itoa(port)
			var args []string
			switch kind {
			case "web":
				args = []string{"/usr/sbin/networksetup", "-setwebproxy", state.Name, server, portStr, "on"}
			case "secure":
				args = []string{"/usr/sbin/networksetup", "-setsecurewebproxy", state.Name, server, portStr, "on"}
			default:
				return fmt.Errorf("unknown proxy kind %q", kind)
			}
			if _, err := runProxyUserCommand(ctx, rt, args...); err != nil {
				return err
			}
		}
		stateArgs := []string{"/usr/sbin/networksetup"}
		switch kind {
		case "web":
			stateArgs = append(stateArgs, "-setwebproxystate", state.Name)
		case "secure":
			stateArgs = append(stateArgs, "-setsecurewebproxystate", state.Name)
		default:
			return fmt.Errorf("unknown proxy kind %q", kind)
		}
		if enabled {
			stateArgs = append(stateArgs, "on")
		} else {
			stateArgs = append(stateArgs, "off")
		}
		_, err := runProxyUserCommand(ctx, rt, stateArgs...)
		return err
	}

	if err := restore("web", state.WebEnabled, state.WebServer, state.WebPort); err != nil {
		return fmt.Errorf("restore web proxy for %s: %w", state.Name, err)
	}
	if err := restore("secure", state.SecureEnabled, state.SecureServer, state.SecurePort); err != nil {
		return fmt.Errorf("restore secure proxy for %s: %w", state.Name, err)
	}
	return nil
}

func parseNetworkSetupProxyStatus(out string) (proxyServiceStatus, error) {
	status := proxyServiceStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "Enabled":
			status.Enabled = strings.EqualFold(val, "Yes")
		case "Server":
			status.Server = val
		case "Port":
			n, err := strconv.Atoi(val)
			if err != nil {
				return proxyServiceStatus{}, fmt.Errorf("parse proxy port %q: %w", val, err)
			}
			status.Port = n
		}
	}
	return status, nil
}

func runProxyUserCommand(ctx context.Context, rt proxyRuntime, args ...string) (string, error) {
	resp, err := rt.UserExec(ctx, args, nil, "")
	if err != nil {
		return "", err
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(string(resp.GetStderr()))
		if msg == "" {
			msg = strings.TrimSpace(string(resp.GetStdout()))
		}
		if msg == "" {
			msg = "unknown error"
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(resp.GetStdout()), nil
}

func readProxyFileBackup(ctx context.Context, rt proxyRuntime, path string) (proxyFileBackup, error) {
	data, err := rt.ReadFile(ctx, path)
	if err != nil {
		if isNotFoundError(err) {
			return proxyFileBackup{Path: path, Present: false}, nil
		}
		return proxyFileBackup{}, err
	}
	return proxyFileBackup{
		Path:    path,
		Present: true,
		Mode:    0644,
		Data:    data,
	}, nil
}

func removeProxyFile(ctx context.Context, rt proxyRuntime, path string) error {
	_, err := rt.Exec(ctx, []string{"/bin/rm", "-f", path}, nil, "")
	return err
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") ||
		strings.Contains(s, "no such file") ||
		strings.Contains(s, "does not exist")
}

func proxyStatePath(vmDirectory string) string {
	return filepath.Join(vmDirectory, proxyStateFileName)
}

func saveProxyState(vmDirectory string, state *proxyState) error {
	if state == nil {
		return nil
	}
	if err := os.MkdirAll(vmDirectory, 0755); err != nil {
		return fmt.Errorf("create vm dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal proxy state: %w", err)
	}
	if err := os.WriteFile(proxyStatePath(vmDirectory), append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write proxy state: %w", err)
	}
	return nil
}

func loadProxyState(vmDirectory string) (*proxyState, error) {
	data, err := os.ReadFile(proxyStatePath(vmDirectory))
	if err != nil {
		return nil, err
	}
	var state proxyState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse proxy state: %w", err)
	}
	return &state, nil
}

func printProxyRestoreFailure(vmDirectory string, err error) {
	statePath := proxyStatePath(vmDirectory)
	spec := ""
	if state, loadErr := loadProxyState(vmDirectory); loadErr == nil {
		spec = strings.TrimSpace(state.Spec)
	}
	fmt.Printf("warning: guest proxy restore failed; manual recovery needed: %v\n", err)
	fmt.Printf("  vm dir: %s\n", vmDirectory)
	fmt.Printf("  state file: %s\n", statePath)
	if spec != "" {
		fmt.Printf("  rerun after boot: vz-macos -vm %s run -proxy %s\n", filepath.Base(vmDirectory), spec)
	}
}
