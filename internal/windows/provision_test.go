package windows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestGenerateAutounattendXMLDefaults(t *testing.T) {
	xml := GenerateAutounattendXML(ProvisionConfig{})

	tests := []struct {
		name string
		want string
	}{
		{name: "windows pe", want: `<settings pass="windowsPE">`},
		{name: "arm64", want: `processorArchitecture="arm64"`},
		{name: "oobe bypass", want: `<HideOnlineAccountScreens>true</HideOnlineAccountScreens>`},
		{name: "labconfig run synchronous", want: `<RunSynchronous>`},
		{name: "tpm bypass", want: `BypassTPMCheck`},
		{name: "secure boot bypass", want: `BypassSecureBootCheck`},
		{name: "netkvm driver", want: `NetKVM\w11\ARM64`},
		{name: "vioserial driver", want: `vioserial\w11\ARM64`},
		{name: "firewall rule", want: `Cove vz-agent`},
		{name: "generic install key", want: `<Key>YTMG3-N6DKC-DKB77-7M9GH-8HVX7</Key>`},
		{name: "hide product key ui", want: `<WillShowUI>Never</WillShowUI>`},
		{name: "local admin", want: `<Group>Administrators</Group>`},
		{name: "autologon", want: `<AutoLogon>`},
		{name: "persistent autologon", want: `DefaultPassword`},
		{name: "password never expires", want: `PasswordNeverExpires`},
		{name: "openssh", want: `Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0`},
		{name: "winrm", want: `Enable-PSRemoting -Force`},
		{name: "marker", want: `C:\ProgramData\cove\provisioned`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(xml, tt.want) {
				t.Fatalf("autounattend.xml does not contain %q", tt.want)
			}
		})
	}
}

func TestGenerateAutounattendXMLPersistentAutoLogonEscapesPowerShell(t *testing.T) {
	xml := GenerateAutounattendXML(ProvisionConfig{
		Username:  `o'brien`,
		Password:  `pa'ss`,
		AutoLogon: true,
	})
	for _, want := range []string{
		`Persist cove auto logon`,
		`DefaultUserName -Value &#39;o&#39;&#39;brien&#39;`,
		`DefaultPassword -Value &#39;pa&#39;&#39;ss&#39;`,
		`Set-LocalUser -Name &#39;o&#39;&#39;brien&#39; -PasswordNeverExpires $true`,
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("autounattend.xml does not contain %q", want)
		}
	}
}

func TestGenerateAutounattendXMLEscapesValues(t *testing.T) {
	xml := GenerateAutounattendXML(ProvisionConfig{
		Username:   `a&b<user>`,
		Password:   `"p&ss<word>"`,
		Hostname:   `HOST&<1>`,
		Locale:     `en&<US>`,
		TimeZone:   `UTC&<zone>`,
		ProductKey: `KEY&<1>`,
		OOBEBypass: true,
		AutoLogon:  true,
		LocalAdmin: true,
	})

	for _, bad := range []string{`a&b<user>`, `"p&ss<word>"`, `HOST&<1>`, `en&<US>`, `UTC&<zone>`, `KEY&<1>`} {
		if strings.Contains(xml, bad) {
			t.Fatalf("autounattend.xml contains unescaped value %q", bad)
		}
	}
	for _, want := range []string{`a&amp;b&lt;user&gt;`, `&#34;p&amp;ss&lt;word&gt;&#34;`, `HOST&amp;&lt;1&gt;`, `en&amp;&lt;US&gt;`, `UTC&amp;&lt;zone&gt;`, `KEY&amp;&lt;1&gt;`} {
		if !strings.Contains(xml, want) {
			t.Fatalf("autounattend.xml does not contain escaped value %q", want)
		}
	}
}

func TestGenerateAutounattendXMLInstallsAgentWhenProvided(t *testing.T) {
	xml := GenerateAutounattendXML(ProvisionConfig{
		AgentExecutable: "vz-agent.exe",
		AgentTCPPort:    2024,
	})

	for _, want := range []string{
		`Install cove vz-agent`,
		`install-vz-agent.ps1`,
		`Disable display sleep`,
		`powercfg /change monitor-timeout-ac 0`,
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("autounattend.xml does not contain %q", want)
		}
	}
}

func TestGenerateAutounattendXMLInstallsSpiceGuestToolsWhenProvided(t *testing.T) {
	xml := GenerateAutounattendXML(ProvisionConfig{
		SpiceGuestToolsExecutable: "spice-guest-tools.exe",
	})

	for _, want := range []string{
		`Install SPICE guest tools`,
		`install-spice-guest-tools.ps1`,
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("autounattend.xml does not contain %q", want)
		}
	}
}

func TestEnsureVirtIODriversISOReturnsCachedPath(t *testing.T) {
	cacheDir := t.TempDir()
	isoPath := filepath.Join(cacheDir, virtIODriversISOName)
	if err := os.WriteFile(isoPath, make([]byte, minVirtIODriversSize), 0644); err != nil {
		t.Fatalf("write cached iso: %v", err)
	}

	got, err := EnsureVirtIODriversISO(cacheDir)
	if err != nil {
		t.Fatalf("EnsureVirtIODriversISO: %v", err)
	}
	if got != isoPath {
		t.Fatalf("cached iso path = %q, want %q", got, isoPath)
	}
}

func TestCreateAutounattendISORejectsInvalidVMDir(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name    string
		vmDir   string
		want    string
		wantErr error
	}{
		{name: "empty", want: "vm dir is empty"},
		{name: "parent is file", vmDir: filepath.Join(blocker, "vm"), want: "create vm dir", wantErr: syscall.ENOTDIR},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateAutounattendISO(tt.vmDir, ProvisionConfig{})
			if err == nil {
				t.Fatal("CreateAutounattendISO succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, tt.wantErr)
			}
		})
	}
}

func TestCreateAutounattendISORejectsExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	isoPath := filepath.Join(dir, "autounattend.iso")
	if err := os.Mkdir(isoPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(isoPath, "child"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := CreateAutounattendISO(dir, ProvisionConfig{})
	if err == nil {
		t.Fatal("CreateAutounattendISO succeeded, want error")
	}
	if !strings.Contains(err.Error(), "remove existing autounattend iso") {
		t.Fatalf("error = %q, want remove existing autounattend iso", err.Error())
	}
}

func TestCreateAutounattendISOInvokesHdiutil(t *testing.T) {
	for _, tt := range []struct {
		name    string
		script  string
		wantErr string
	}{
		{
			name: "success",
			script: `#!/bin/sh
set -eu
out=
while [ "$#" -gt 0 ]; do
	if [ "$1" = "-o" ]; then
		shift
		out=$1
	fi
	last=$1
	shift
done
test -n "$out"
test -f "$last/autounattend.xml"
printf iso >"$out"
`,
		},
		{
			name: "failure",
			script: `#!/bin/sh
echo hdiutil failed
exit 2
`,
			wantErr: "create autounattend iso",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(t.TempDir(), "hdiutil")
			if err := os.WriteFile(bin, []byte(tt.script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(bin))

			got, err := CreateAutounattendISO(dir, ProvisionConfig{})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CreateAutounattendISO() error = %v, want %q", err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), "hdiutil failed") {
					t.Fatalf("CreateAutounattendISO() error = %v, want command output", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateAutounattendISO(): %v", err)
			}
			if got != filepath.Join(dir, "autounattend.iso") {
				t.Fatalf("iso path = %q, want autounattend.iso in vm dir", got)
			}
			if _, err := os.Stat(got); err != nil {
				t.Fatalf("stat iso: %v", err)
			}
		})
	}
}

func TestCreateAutounattendISOIncludesAgentArtifacts(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "vz-agent.exe")
	if err := os.WriteFile(agent, []byte("agent"), 0755); err != nil {
		t.Fatal(err)
	}
	spiceGuestTools := filepath.Join(dir, "spice-guest-tools.exe")
	if err := os.WriteFile(spiceGuestTools, []byte("spice"), 0755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "hdiutil")
	if err := os.WriteFile(bin, []byte(`#!/bin/sh
set -eu
out=
while [ "$#" -gt 0 ]; do
	if [ "$1" = "-o" ]; then
		shift
		out=$1
	fi
	last=$1
	shift
done
test -n "$out"
test -f "$last/autounattend.xml"
test -f "$last/vz-agent.exe"
test -f "$last/install-vz-agent.ps1"
test -f "$last/spice-guest-tools.exe"
test -f "$last/install-spice-guest-tools.ps1"
	grep -q -- '-tcp-listen 0.0.0.0:2024' "$last/install-vz-agent.ps1"
		grep -q -- '-tcp-listen 0.0.0.0:2025' "$last/install-vz-agent.ps1"
		grep -q -- 'Cove vz-agent executable' "$last/install-vz-agent.ps1"
		grep -q -- '-Program $Agent' "$last/install-vz-agent.ps1"
		grep -q -- 'Set-NetFirewallProfile -Profile Domain,Public,Private -Enabled False' "$last/install-vz-agent.ps1"
		grep -q -- 'cove-vz-agent-user' "$last/install-vz-agent.ps1"
grep -q -- 'CoveVZAgentUser' "$last/install-vz-agent.ps1"
grep -q -- 'Register-ScheduledTask' "$last/install-vz-agent.ps1"
grep -q -- 'spice-guest-tools.exe' "$last/install-spice-guest-tools.ps1"
grep -q -- 'cove-spice-guest-tools' "$last/install-spice-guest-tools.ps1"
grep -q -- 'NetKVM\\w11\\ARM64' "$last/autounattend.xml"
grep -q -- 'vioserial\\w11\\ARM64' "$last/autounattend.xml"
printf iso >"$out"
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+":/usr/bin:/bin")

	got, err := CreateAutounattendISO(dir, ProvisionConfig{
		AgentExecutable:           agent,
		AgentTCPPort:              2024,
		AgentUserTCPPort:          2025,
		SpiceGuestToolsExecutable: spiceGuestTools,
	})
	if err != nil {
		t.Fatalf("CreateAutounattendISO: %v", err)
	}
	if got != filepath.Join(dir, "autounattend.iso") {
		t.Fatalf("iso path = %q, want autounattend.iso in vm dir", got)
	}
}
