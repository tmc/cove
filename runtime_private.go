package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	debugstubx "github.com/tmc/apple/x/vzkit/debugstub"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"
	vncx "github.com/tmc/apple/x/vzkit/vnc"
	"github.com/tmc/cove/internal/vmrun"
)

type runtimeFeatureSnapshot struct {
	VNC       vncStatus       `json:"vnc,omitempty"`
	DebugStub debugStubStatus `json:"debug_stub,omitempty"`
}

type vncStatus struct {
	Requested         bool   `json:"requested,omitempty"`
	Started           bool   `json:"started,omitempty"`
	Port              uint16 `json:"port,omitempty"`
	PasswordProtected bool   `json:"password_protected,omitempty"`
	BonjourService    string `json:"bonjour_service,omitempty"`
	DisplayAttached   bool   `json:"display_attached,omitempty"`
	RawState          int64  `json:"raw_state,omitempty"`
	Description       string `json:"description,omitempty"`
	Error             string `json:"error,omitempty"`
}

type debugStubStatus struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Port      uint16 `json:"port,omitempty"`
	ListenAll bool   `json:"listen_all,omitempty"`
}

type runtimeFeatureState struct {
	mu    sync.Mutex
	vnc   vncStatus
	debug debugStubStatus

	vncServer *vncx.Server
}

var controlRuntimeFeatures sync.Map

func (s *ControlServer) SetRuntimeFeatureState(state *runtimeFeatureState) {
	if state == nil {
		controlRuntimeFeatures.Delete(s)
		return
	}
	controlRuntimeFeatures.Store(s, state)
	s.SetVNCStatus(state.controlVNCStatus())
	s.SetDebugStubStatus(state.controlDebugStubStatus())
}

func (s *ControlServer) RuntimeFeatureSnapshot() runtimeFeatureSnapshot {
	if state := runtimeFeatureStateFor(s); state != nil {
		return state.snapshot()
	}
	return runtimeFeatureSnapshot{}
}

func (s *ControlServer) StopRuntimeFeatureState() {
	if state := runtimeFeatureStateFor(s); state != nil {
		s.SetVNCStatus(state.controlVNCStatus())
		s.SetDebugStubStatus(state.controlDebugStubStatus())
		state.stop()
		controlRuntimeFeatures.Delete(s)
	}
}

func runtimeFeatureStateFor(s *ControlServer) *runtimeFeatureState {
	value, ok := controlRuntimeFeatures.Load(s)
	if !ok {
		return nil
	}
	state, _ := value.(*runtimeFeatureState)
	return state
}

func newRuntimeFeatureState() (*runtimeFeatureState, error) {
	state := &runtimeFeatureState{}

	if vncEnabled() {
		port, err := parsePortSpec(vncAddress)
		if err != nil {
			return nil, fmt.Errorf("parse vnc port: %w", err)
		}
		if port == 0 {
			port = 5900
		}
		state.vnc = vncStatus{
			Requested:         true,
			Port:              port,
			PasswordProtected: strings.TrimSpace(vncPassword) != "",
			BonjourService:    strings.TrimSpace(vncBonjourService),
		}
	}

	if debugStubEnabled() {
		port, err := parsePortSpec(gdbAddress)
		if err != nil {
			return nil, fmt.Errorf("parse gdb port: %w", err)
		}
		state.debug = debugStubStatus{
			Enabled:   true,
			Kind:      "gdb",
			Port:      port,
			ListenAll: gdbListenAll,
		}
	}

	if !state.vnc.Requested && !state.debug.Enabled {
		return nil, nil
	}
	return state, nil
}

func (s *runtimeFeatureState) snapshot() runtimeFeatureSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := runtimeFeatureSnapshot{
		VNC:       s.vnc,
		DebugStub: s.debug,
	}
	if s.vncServer != nil {
		snapshot.VNC.RawState = s.vncServer.State()
		if port := s.vncServer.Port(); port != 0 {
			snapshot.VNC.Port = port
		}
		if desc := s.vncServer.Description(); desc != "" {
			snapshot.VNC.Description = desc
		}
	}
	return snapshot
}

func (s *runtimeFeatureState) controlVNCStatus() VNCStatus {
	snapshot := s.snapshot().VNC
	status := VNCStatus{
		Enabled:           snapshot.Requested,
		Port:              snapshot.Port,
		Endpoint:          localhostEndpoint(snapshot.Port),
		PasswordProtected: snapshot.PasswordProtected,
		ServiceName:       snapshot.BonjourService,
		Description:       snapshot.Description,
	}
	switch {
	case snapshot.Error != "":
		status.State = "error"
		if status.Description == "" {
			status.Description = snapshot.Error
		}
	case snapshot.Started:
		status.State = "running"
	case snapshot.Requested:
		status.State = "requested"
	default:
		status.State = "disabled"
	}
	return status
}

func (s *runtimeFeatureState) controlDebugStubStatus() DebugStubStatus {
	status := DebugStubStatus{
		Enabled:   s.debug.Enabled,
		Kind:      s.debug.Kind,
		Port:      s.debug.Port,
		Endpoint:  debugStubEndpoint(s.debug.Port, s.debug.ListenAll),
		Connect:   debugStubConnectCommand(s.debug.Port),
		ListenAll: s.debug.ListenAll,
	}
	if s.debug.Enabled {
		status.State = "attached"
		status.Description = fmt.Sprintf("GDB debug stub listening on port %d", s.debug.Port)
	} else {
		status.State = "disabled"
	}
	return status
}

func localhostEndpoint(port uint16) string {
	if port == 0 {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func debugStubEndpoint(port uint16, listenAll bool) string {
	if port == 0 {
		return ""
	}
	if listenAll {
		return fmt.Sprintf("0.0.0.0:%d", port)
	}
	return localhostEndpoint(port)
}

func debugStubConnectCommand(port uint16) string {
	if port == 0 {
		return ""
	}
	return fmt.Sprintf("lldb -o 'gdb-remote 127.0.0.1:%d'", port)
}

func (s *runtimeFeatureState) startVMServices(machine vz.VZVirtualMachine, queue dispatch.Queue) error {
	if s == nil {
		return nil
	}
	if err := s.ensureVNCStarted(machine, queue); err != nil {
		return err
	}
	return nil
}

func (s *runtimeFeatureState) ensureVNCStarted(machine vz.VZVirtualMachine, queue dispatch.Queue) error {
	s.mu.Lock()
	if !s.vnc.Requested || s.vnc.Started || s.vnc.Error != "" {
		errMsg := s.vnc.Error
		s.mu.Unlock()
		if errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		return nil
	}
	s.mu.Unlock()

	cfg := vncx.Config{
		Port:        s.vnc.Port,
		ServiceName: s.vnc.BonjourService,
		Queue:       vmruntime.WrapQueue(queue),
	}
	if s.vnc.PasswordProtected {
		cfg.Mode = vncx.SecurityPassword
		cfg.Password = vncPassword
	}

	server, err := vncx.New(cfg)
	if err != nil {
		s.mu.Lock()
		s.vnc.Error = err.Error()
		s.mu.Unlock()
		return fmt.Errorf("create vnc server: %w", err)
	}

	result, err := server.StartVirtualMachine(machine)
	if err != nil && verbose {
		fmt.Printf("warning: vnc display: %v\n", err)
	}

	s.mu.Lock()
	s.vnc.Started = true
	s.vnc.DisplayAttached = result.DisplayAttached
	s.vnc.RawState = result.State
	if port := result.Port; port != 0 {
		s.vnc.Port = port
	}
	if desc := result.Description; desc != "" {
		s.vnc.Description = desc
	}
	s.vncServer = server
	status := s.vnc
	s.mu.Unlock()

	fmt.Printf("VNC server: port %d\n", status.Port)
	if status.PasswordProtected {
		fmt.Println("VNC server: password authentication enabled")
	}
	if status.BonjourService != "" {
		fmt.Printf("VNC server: bonjour service %q\n", status.BonjourService)
	}
	return nil
}

func (s *runtimeFeatureState) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	server := s.vncServer
	s.vncServer = nil
	s.mu.Unlock()

	if server != nil {
		server.Stop()
		s.mu.Lock()
		s.vnc.RawState = server.State()
		s.mu.Unlock()
	}
}

func parsePortSpec(spec string) (uint16, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, nil
	}
	if strings.Contains(spec, "/") {
		return 0, fmt.Errorf("expected port, got %q", spec)
	}
	spec = strings.TrimPrefix(spec, ":")
	if strings.Contains(spec, ":") {
		return 0, fmt.Errorf("host-qualified address %q is not supported; use :port or port", spec)
	}
	n, err := strconv.Atoi(spec)
	if err != nil {
		return 0, fmt.Errorf("parse port %q: %w", spec, err)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %d out of range", n)
	}
	return uint16(n), nil
}

func vncEnabled() bool {
	return strings.TrimSpace(vncAddress) != "" || strings.TrimSpace(vncBonjourService) != ""
}

func debugStubEnabled() bool {
	return strings.TrimSpace(gdbAddress) != ""
}

func privateMacStartOptionsEnabledForRun(rc vmrun.RunConfig) bool {
	return rc.ForceDFU || rc.StopIBoot1 || rc.StopIBoot2
}

func privateSaveOptionsEnabledForRun(rc vmrun.RunConfig) bool {
	return rc.SaveCompress || rc.SaveEncrypt
}

func applyPrivateVMConfigurationWithRunConfig(config vz.VZVirtualMachineConfiguration, rc vmrun.RunConfig) error {
	if len(rc.BlockDevices) > 0 {
		devices := make(blockDeviceSlice, 0, len(rc.BlockDevices))
		for _, b := range rc.BlockDevices {
			devices = append(devices, blockDeviceSpec{
				Path:     b.Path,
				ReadOnly: b.ReadOnly,
				Sync:     b.Cache,
			})
		}
		if err := addBlockDevicesToConfig(config, devices); err != nil {
			return fmt.Errorf("add block devices: %w", err)
		}
	}
	if strings.TrimSpace(rc.GDBAddress) != "" {
		port, err := parsePortSpec(rc.GDBAddress)
		if err != nil {
			return err
		}
		privConfig := pvz.VZVirtualMachineConfigurationFromID(config.ID)
		if err := debugstubx.AttachGDB(privConfig, port, rc.GDBListenAll); err != nil {
			return fmt.Errorf("attach gdb debug stub: %w", err)
		}
	}
	return nil
}

func startVMWithRunConfig(machine vz.VZVirtualMachine, rc vmrun.RunConfig, completion func(error)) {
	if rc.OS == vmrun.GuestMacOS && (rc.RecoveryMode || privateMacStartOptionsEnabledForRun(rc)) {
		opts := vz.NewVZMacOSVirtualMachineStartOptions()
		opts.SetStartUpFromMacOSRecovery(rc.RecoveryMode)
		if privateMacStartOptionsEnabledForRun(rc) {
			privateOpts := pvz.VZMacOSVirtualMachineStartOptionsFromID(opts.ID)
			privateOpts.SetForceDFU(rc.ForceDFU)
			privateOpts.SetStopInIBootStage1(rc.StopIBoot1)
			privateOpts.SetStopInIBootStage2(rc.StopIBoot2)
		}
		machine.StartWithOptionsCompletionHandler(&opts.VZVirtualMachineStartOptions, completion)
		return
	}
	machine.StartWithCompletionHandler(completion)
}

func saveMachineStateWithRunConfig(machine vz.VZVirtualMachine, url foundation.NSURL, rc vmrun.RunConfig, completion func(error)) {
	if !privateSaveOptionsEnabledForRun(rc) {
		machine.SaveMachineStateToURLCompletionHandler(url, completion)
		return
	}

	options := pvz.NewVZVirtualMachineSaveOptions()
	options.SetCompress(rc.SaveCompress)
	options.SetEncrypt(rc.SaveEncrypt)
	pvz.VZVirtualMachineFromID(machine.ID).SaveMachineStateToURLOptionsCompletionHandler(url, options, completion)
}

func privateRuntimeSummaryForRun(rc vmrun.RunConfig) string {
	parts := make([]string, 0, 3)
	switch {
	case rc.RecoveryMode:
		parts = append(parts, "recovery")
	case rc.ForceDFU:
		parts = append(parts, "dfu")
	case rc.StopIBoot1:
		parts = append(parts, "iboot-stage1")
	case rc.StopIBoot2:
		parts = append(parts, "iboot-stage2")
	}
	if debugStubEnabled() {
		parts = append(parts, "gdb")
	}
	if vncEnabled() {
		parts = append(parts, "vnc")
	}
	return strings.Join(parts, ", ")
}

func validatePrivateRuntimeOptionsForOptions(opts runtimeOptions) error {
	if _, err := parsePortSpec(opts.VNCAddress); err != nil {
		return fmt.Errorf("invalid -vnc: %w", err)
	}
	vncRequested := strings.TrimSpace(opts.VNCAddress) != "" || strings.TrimSpace(opts.VNCBonjourService) != ""
	if strings.TrimSpace(opts.VNCPassword) != "" && !vncRequested {
		return fmt.Errorf("-vnc-password requires -vnc or -vnc-bonjour")
	}
	if strings.TrimSpace(opts.VNCBonjourService) != "" && strings.TrimSpace(opts.VNCPassword) == "" {
		return fmt.Errorf("-vnc-bonjour requires -vnc-password so advertised VNC is not unauthenticated")
	}
	if _, err := parsePortSpec(opts.GDBAddress); err != nil {
		return fmt.Errorf("invalid -gdb: %w", err)
	}
	if opts.GDBListenAll && strings.TrimSpace(opts.GDBAddress) == "" {
		return fmt.Errorf("-gdb-listen-all requires -gdb")
	}
	if (opts.Linux || opts.Windows) && opts.RecoveryMode {
		return fmt.Errorf("-recovery is only valid for macOS VMs")
	}
	if (opts.Linux || opts.Windows) && (opts.ForceDFU || opts.StopIBoot1 || opts.StopIBoot2) {
		return fmt.Errorf("macOS-only start options require a macOS VM")
	}
	if opts.StopIBoot1 && opts.StopIBoot2 {
		return fmt.Errorf("-iboot-stage1 and -iboot-stage2 are mutually exclusive")
	}
	if opts.RecoveryMode && (opts.ForceDFU || opts.StopIBoot1 || opts.StopIBoot2) {
		return fmt.Errorf("recovery mode cannot be combined with private macOS boot-stop options")
	}
	if opts.ForceDFU && (opts.StopIBoot1 || opts.StopIBoot2) {
		return fmt.Errorf("-force-dfu cannot be combined with -iboot-stage1 or -iboot-stage2")
	}
	return nil
}
