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
		{name: "local admin", want: `<Group>Administrators</Group>`},
		{name: "autologon", want: `<AutoLogon>`},
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

func TestGenerateAutounattendXMLEscapesValues(t *testing.T) {
	xml := GenerateAutounattendXML(ProvisionConfig{
		Username:   `a&b<user>`,
		Password:   `"p&ss<word>"`,
		Hostname:   `HOST&<1>`,
		Locale:     `en&<US>`,
		TimeZone:   `UTC&<zone>`,
		OOBEBypass: true,
		AutoLogon:  true,
		LocalAdmin: true,
	})

	for _, bad := range []string{`a&b<user>`, `"p&ss<word>"`, `HOST&<1>`, `en&<US>`, `UTC&<zone>`} {
		if strings.Contains(xml, bad) {
			t.Fatalf("autounattend.xml contains unescaped value %q", bad)
		}
	}
	for _, want := range []string{`a&amp;b&lt;user&gt;`, `&#34;p&amp;ss&lt;word&gt;&#34;`, `HOST&amp;&lt;1&gt;`, `en&amp;&lt;US&gt;`, `UTC&amp;&lt;zone&gt;`} {
		if !strings.Contains(xml, want) {
			t.Fatalf("autounattend.xml does not contain escaped value %q", want)
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
