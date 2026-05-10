package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestParseSandboxLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    SandboxLevel
		wantErr bool
	}{
		{name: "empty", input: "", want: SandboxLevelUnset},
		{name: "minimal", input: "minimal", want: SandboxLevelMinimal},
		{name: "strict", input: "strict", want: SandboxLevelStrict},
		{name: "none alias", input: "none", want: SandboxLevelUnset},
		{name: "invalid", input: "hard", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseSandboxLevel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSandboxLevel(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSandboxLevel(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseSandboxLevel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSandboxPolicyEffectiveBehavior(t *testing.T) {
	t.Parallel()

	cliVolumes := []vmconfig.VolumeMount{
		{HostPath: "/tmp/cli", Tag: "cli"},
	}
	savedVolumes := []vmconfig.VolumeMount{
		{HostPath: "/tmp/saved", Tag: "saved"},
	}
	savedFolders := []SharedFolderEntry{
		{Path: "/tmp/share", Tag: "share"},
	}

	tests := []struct {
		name          string
		policy        SandboxPolicy
		requestedNet  string
		explicitNet   bool
		wantNet       string
		wantNetErr    bool
		wantVolumes   int
		wantFolders   int
		wantVsock     bool
		wantRosetta   bool
		wantProxy     bool
		wantUpgrade   bool
		wantProvision bool
	}{
		{
			name:          "no sandbox keeps explicit settings",
			policy:        SandboxPolicy{},
			requestedNet:  "nat",
			explicitNet:   true,
			wantNet:       "nat",
			wantVolumes:   1,
			wantFolders:   1,
			wantVsock:     true,
			wantRosetta:   true,
			wantProxy:     true,
			wantUpgrade:   true,
			wantProvision: true,
		},
		{
			name:          "minimal defaults network to none and drops sharing",
			policy:        SandboxPolicy{Level: SandboxLevelMinimal},
			requestedNet:  "nat",
			explicitNet:   false,
			wantNet:       "none",
			wantVolumes:   0,
			wantFolders:   0,
			wantVsock:     true,
			wantRosetta:   true,
			wantProxy:     true,
			wantUpgrade:   false,
			wantProvision: false,
		},
		{
			name:          "minimal preserves explicit network choice",
			policy:        SandboxPolicy{Level: SandboxLevelMinimal},
			requestedNet:  "bridged:en0",
			explicitNet:   true,
			wantNet:       "bridged:en0",
			wantVolumes:   0,
			wantFolders:   0,
			wantVsock:     true,
			wantRosetta:   true,
			wantProxy:     true,
			wantUpgrade:   false,
			wantProvision: false,
		},
		{
			name:          "strict rejects non-none network",
			policy:        SandboxPolicy{Level: SandboxLevelStrict},
			requestedNet:  "nat",
			explicitNet:   true,
			wantNetErr:    true,
			wantVolumes:   0,
			wantFolders:   0,
			wantVsock:     false,
			wantRosetta:   false,
			wantProxy:     false,
			wantUpgrade:   false,
			wantProvision: false,
		},
		{
			name:          "strict allows explicit none",
			policy:        SandboxPolicy{Level: SandboxLevelStrict},
			requestedNet:  "none",
			explicitNet:   true,
			wantNet:       "none",
			wantVolumes:   0,
			wantFolders:   0,
			wantVsock:     false,
			wantRosetta:   false,
			wantProxy:     false,
			wantUpgrade:   false,
			wantProvision: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotNet, err := tt.policy.EffectiveNetworkMode(tt.requestedNet, tt.explicitNet)
			if tt.wantNetErr {
				if err == nil {
					t.Fatalf("EffectiveNetworkMode() error = nil, want error")
				}
			} else if err != nil {
				t.Fatalf("EffectiveNetworkMode() error = %v", err)
			} else if gotNet != tt.wantNet {
				t.Fatalf("EffectiveNetworkMode() = %q, want %q", gotNet, tt.wantNet)
			}

			gotVolumes := tt.policy.EffectiveVolumes(cliVolumes, savedVolumes)
			if len(gotVolumes) != tt.wantVolumes {
				t.Fatalf("EffectiveVolumes() len = %d, want %d", len(gotVolumes), tt.wantVolumes)
			}
			gotFolders := tt.policy.EffectiveSharedFolders(savedFolders)
			if len(gotFolders) != tt.wantFolders {
				t.Fatalf("EffectiveSharedFolders() len = %d, want %d", len(gotFolders), tt.wantFolders)
			}
			if got := tt.policy.EffectiveVsock(true); got != tt.wantVsock {
				t.Fatalf("EffectiveVsock(true) = %v, want %v", got, tt.wantVsock)
			}
			if got := tt.policy.EffectiveRosetta(true); got != tt.wantRosetta {
				t.Fatalf("EffectiveRosetta(true) = %v, want %v", got, tt.wantRosetta)
			}
			if got := tt.policy.EffectiveProxy(true); got != tt.wantProxy {
				t.Fatalf("EffectiveProxy(true) = %v, want %v", got, tt.wantProxy)
			}
			if got := tt.policy.EffectiveAgentUpgrade(true); got != tt.wantUpgrade {
				t.Fatalf("EffectiveAgentUpgrade(true) = %v, want %v", got, tt.wantUpgrade)
			}
			if got := tt.policy.AllowsAgentProvision(); got != tt.wantProvision {
				t.Fatalf("AllowsAgentProvision() = %v, want %v", got, tt.wantProvision)
			}
		})
	}
}

func TestSandboxPolicyEffectiveReturnsFalseWhenNotRequested(t *testing.T) {
	t.Parallel()
	policies := []SandboxPolicy{
		{},
		{Level: SandboxLevelMinimal},
		{Level: SandboxLevelStrict},
	}
	for _, p := range policies {
		if got := p.EffectiveVsock(false); got {
			t.Errorf("EffectiveVsock(false) on %v = true, want false", p.Level)
		}
		if got := p.EffectiveRosetta(false); got {
			t.Errorf("EffectiveRosetta(false) on %v = true, want false", p.Level)
		}
		if got := p.EffectiveProxy(false); got {
			t.Errorf("EffectiveProxy(false) on %v = true, want false", p.Level)
		}
		if got := p.EffectiveAgentUpgrade(false); got {
			t.Errorf("EffectiveAgentUpgrade(false) on %v = true, want false", p.Level)
		}
	}
}

func TestSandboxPolicyCopiesInput(t *testing.T) {
	t.Parallel()

	policy := SandboxPolicy{}
	cliVolumes := []vmconfig.VolumeMount{{HostPath: "/tmp/cli", Tag: "cli"}}
	savedVolumes := []vmconfig.VolumeMount{{HostPath: "/tmp/saved", Tag: "saved"}}
	got := policy.EffectiveVolumes(cliVolumes, savedVolumes)
	if !reflect.DeepEqual(got, cliVolumes) {
		t.Fatalf("EffectiveVolumes() = %#v, want %#v", got, cliVolumes)
	}
	got[0].Tag = "mutated"
	if cliVolumes[0].Tag != "cli" {
		t.Fatalf("EffectiveVolumes() returned alias of input slice")
	}
}

func TestGetEffectiveVolumesIncludesLegacyShareDir(t *testing.T) {
	oldVMDir := vmDir
	oldVolumes := volumes
	oldShareDir := shareDir
	oldSandboxLevel := sandboxLevel
	t.Cleanup(func() {
		vmDir = oldVMDir
		volumes = oldVolumes
		shareDir = oldShareDir
		sandboxLevel = oldSandboxLevel
	})
	vmDir = t.TempDir()
	volumes = nil
	sandboxLevel = ""

	share := t.TempDir()
	shareDir = share

	got := getEffectiveVolumes()
	if len(got) != 1 {
		t.Fatalf("getEffectiveVolumes() = %#v, want 1 entry from shareDir", got)
	}
	abs, err := filepath.EvalSymlinks(share)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got[0].HostPath != abs {
		t.Fatalf("HostPath = %q, want %q", got[0].HostPath, abs)
	}
	if got[0].ReadOnly {
		t.Fatalf("ReadOnly = true, want false for shareDir-derived mount")
	}
}

func TestGetEffectiveVolumesRespectsSandboxForSavedConfig(t *testing.T) {
	oldVMDir := vmDir
	oldVolumes := volumes
	oldShareDir := shareDir
	oldSandboxLevel := sandboxLevel
	t.Cleanup(func() {
		vmDir = oldVMDir
		volumes = oldVolumes
		shareDir = oldShareDir
		sandboxLevel = oldSandboxLevel
	})

	vmDir = t.TempDir()
	volumes = nil
	shareDir = ""
	sandboxLevel = ""

	want := vmconfig.VolumeMount{HostPath: "/tmp/saved", Tag: "saved"}
	if err := vmconfig.Save(vmDir, &vmconfig.Config{Volumes: []vmconfig.VolumeMount{want}}); err != nil {
		t.Fatalf("vmconfig.Save() error = %v", err)
	}

	got := getEffectiveVolumes()
	if !reflect.DeepEqual(got, []vmconfig.VolumeMount{want}) {
		t.Fatalf("getEffectiveVolumes() = %#v, want %#v", got, []vmconfig.VolumeMount{want})
	}

	sandboxLevel = "minimal"
	got = getEffectiveVolumes()
	if len(got) != 0 {
		t.Fatalf("getEffectiveVolumes() with sandbox = %#v, want nil", got)
	}
}

func TestEffectiveSharedFoldersRespectsSandboxForSavedState(t *testing.T) {
	oldSandboxLevel := sandboxLevel
	t.Cleanup(func() {
		sandboxLevel = oldSandboxLevel
	})

	vmDirectory := t.TempDir()
	want := []SharedFolderEntry{{Path: filepath.Join(vmDirectory, "share"), Tag: "share", ReadOnly: true}}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal shared folders: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDirectory, "shared_folders.json"), append(data, '\n'), 0644); err != nil {
		t.Fatalf("write shared_folders.json: %v", err)
	}

	sandboxLevel = ""
	if got := effectiveSharedFolders(vmDirectory); !reflect.DeepEqual(got, want) {
		t.Fatalf("effectiveSharedFolders() = %#v, want %#v", got, want)
	}

	sandboxLevel = "strict"
	if got := effectiveSharedFolders(vmDirectory); len(got) != 0 {
		t.Fatalf("effectiveSharedFolders() with sandbox = %#v, want nil", got)
	}
}

func TestSandboxPolicyStrict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		level SandboxLevel
		want  bool
	}{
		{SandboxLevelUnset, false},
		{SandboxLevelMinimal, false},
		{SandboxLevelStrict, true},
	}
	for _, tc := range cases {
		if got := (SandboxPolicy{Level: tc.level}).Strict(); got != tc.want {
			t.Fatalf("Strict(%q) = %v, want %v", tc.level, got, tc.want)
		}
	}
}

func TestSandboxLevelHelpers(t *testing.T) {
	oldLevel := sandboxLevel
	oldUpgrade := autoUpgradeAgent
	t.Cleanup(func() {
		sandboxLevel = oldLevel
		autoUpgradeAgent = oldUpgrade
	})

	cases := []struct {
		level             string
		wantActive        bool
		wantStrict        bool
		wantAgentProv     bool
		wantAgentUpgrade  bool
		upgradeFlag       bool
	}{
		{level: "", wantActive: false, wantStrict: false, wantAgentProv: true, wantAgentUpgrade: true, upgradeFlag: true},
		{level: "minimal", wantActive: true, wantStrict: false, wantAgentProv: false, wantAgentUpgrade: false, upgradeFlag: true},
		{level: "strict", wantActive: true, wantStrict: true, wantAgentProv: false, wantAgentUpgrade: false, upgradeFlag: true},
	}
	for _, tc := range cases {
		sandboxLevel = tc.level
		autoUpgradeAgent = tc.upgradeFlag
		if got := sandboxActive(); got != tc.wantActive {
			t.Fatalf("sandboxActive(%q) = %v, want %v", tc.level, got, tc.wantActive)
		}
		if got := sandboxStrict(); got != tc.wantStrict {
			t.Fatalf("sandboxStrict(%q) = %v, want %v", tc.level, got, tc.wantStrict)
		}
		if got := sandboxAllowsAgentProvision(); got != tc.wantAgentProv {
			t.Fatalf("sandboxAllowsAgentProvision(%q) = %v, want %v", tc.level, got, tc.wantAgentProv)
		}
		if got := sandboxAllowsAgentUpgrade(); got != tc.wantAgentUpgrade {
			t.Fatalf("sandboxAllowsAgentUpgrade(%q) = %v, want %v", tc.level, got, tc.wantAgentUpgrade)
		}
	}
}

func TestSharedFolderCommandBlocked(t *testing.T) {
	oldLevel := sandboxLevel
	t.Cleanup(func() { sandboxLevel = oldLevel })

	sandboxLevel = ""
	if sharedFolderCommandBlocked([]string{"add", "/foo"}) {
		t.Fatalf("sharedFolderCommandBlocked without sandbox: want false")
	}

	sandboxLevel = "strict"
	if sharedFolderCommandBlocked(nil) {
		t.Fatalf("sharedFolderCommandBlocked(nil): want false")
	}
	for _, allowed := range []string{"list", "status", "mount", "help", "-h", "--help"} {
		if sharedFolderCommandBlocked([]string{allowed}) {
			t.Fatalf("sharedFolderCommandBlocked(%q): want false", allowed)
		}
	}
	if !sharedFolderCommandBlocked([]string{"add", "/foo"}) {
		t.Fatalf("sharedFolderCommandBlocked(add) under sandbox: want true")
	}
}
