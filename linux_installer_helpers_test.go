package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxVariantDisplayName(t *testing.T) {
	tests := []struct {
		v    LinuxVariant
		want string
	}{
		{LinuxVariantServer, "Ubuntu Server"},
		{LinuxVariantDesktop, "Ubuntu Desktop"},
		{LinuxVariantDebian, "Debian"},
		{LinuxVariantFedora, "Fedora"},
		{LinuxVariantAlpine, "Alpine"},
		{LinuxVariantNixOS, "NixOS"},
		{LinuxVariant("bogus"), "Ubuntu Server"},
	}
	for _, tc := range tests {
		if got := tc.v.displayName(); got != tc.want {
			t.Errorf("(%q).displayName() = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestValidateLinuxVariant(t *testing.T) {
	tests := []struct {
		v       LinuxVariant
		wantErr bool
	}{
		{LinuxVariantServer, false},
		{LinuxVariantDebian, false},
		{LinuxVariantFedora, false},
		{LinuxVariantAlpine, false},
		{LinuxVariantNixOS, false},
		{LinuxVariant(""), false},
		{LinuxVariant("plan9"), true},
	}
	for _, tc := range tests {
		err := validateLinuxVariant(tc.v)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateLinuxVariant(%q) err=%v, wantErr=%v", tc.v, err, tc.wantErr)
		}
	}
}

func TestHashPasswordFallbackFormat(t *testing.T) {
	// hashPassword shells to openssl; we cannot guarantee output format,
	// but we can assert it returns a non-empty SHA-512 crypt-style string
	// (either "$6$..." from openssl or the documented fallback "$6$vz.macos$...").
	got := hashPassword("hunter2")
	if got == "" {
		t.Fatal("hashPassword returned empty string")
	}
	if !strings.HasPrefix(got, "$6$") {
		t.Errorf("hashPassword = %q, want $6$ prefix", got)
	}
	if strings.ContainsAny(got, " \n\t") {
		t.Errorf("hashPassword contains whitespace: %q", got)
	}
}

func TestGenerateMetaData(t *testing.T) {
	cfg := LinuxProvisionConfig{Hostname: "myhost"}
	got := generateMetaData(cfg)
	want := "instance-id: myhost\nlocal-hostname: myhost\n"
	if got != want {
		t.Errorf("generateMetaData = %q, want %q", got, want)
	}
}

func TestGenerateDebianPreseed(t *testing.T) {
	cfg := LinuxProvisionConfig{
		Username: "deb", Password: "pw", Hostname: "h",
		TimeZone: "UTC", Locale: "en_US.UTF-8",
	}
	noAgent := generateDebianPreseed(cfg, false, "")
	for _, s := range []string{
		"d-i passwd/username string deb",
		"d-i passwd/user-password password pw",
		"d-i netcfg/get_hostname string h",
		"d-i time/zone string UTC",
		"in-target systemctl enable ssh",
	} {
		if !strings.Contains(noAgent, s) {
			t.Errorf("no-agent missing %q", s)
		}
	}
	withAgent := generateDebianPreseed(cfg, true, "https://example.test/agent")
	if !strings.Contains(withAgent, "https://example.test/agent") {
		t.Errorf("with-agent missing agent URL")
	}
}

func TestGenerateFedoraKickstart(t *testing.T) {
	cfg := LinuxProvisionConfig{
		Username: "fed", Password: "pw", Hostname: "h",
		TimeZone: "UTC", Locale: "en_US.UTF-8",
	}
	got := generateFedoraKickstart(cfg, true, "https://example.test/agent")
	for _, s := range []string{
		"lang en_US.UTF-8",
		"timezone UTC --utc",
		"--hostname=h",
		"user --name=fed --password=pw --plaintext --groups=wheel",
		"systemctl enable sshd",
		"%post",
		"%end",
		"https://example.test/agent",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
}

func TestBuildAlpineAPKOVL(t *testing.T) {
	data, err := buildAlpineAPKOVL("HOSTNAMEOPTS=\"-n alpine-vm\"\n")
	if err != nil {
		t.Fatalf("buildAlpineAPKOVL: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	saw := map[string]*tar.Header{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		saw[hdr.Name] = hdr
	}
	if h, ok := saw["etc/local.d/cove.start"]; !ok || h.Mode != 0755 {
		t.Errorf("cove.start: ok=%v hdr=%+v", ok, h)
	}
	if h, ok := saw["etc/cove/setup-alpine.answers"]; !ok || h.Mode != 0644 {
		t.Errorf("embedded answers: ok=%v hdr=%+v", ok, h)
	}
	if h, ok := saw["etc/runlevels/default/local"]; !ok || h.Typeflag != tar.TypeSymlink || h.Linkname != "/etc/init.d/local" {
		t.Errorf("runlevels symlink: ok=%v hdr=%+v", ok, h)
	}
}

func TestStageInstalledLinuxBootArtifactsAllowsNoInitrdAndRootDevice(t *testing.T) {
	vmDir := t.TempDir()
	mountPoint := t.TempDir()
	artifactDir := filepath.Join(mountPoint, filepath.FromSlash(linuxEFIBootArtifactsDir))
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "vmlinuz"), []byte("MZkernel"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, linuxRootUUIDFileName), []byte("uuid-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, linuxRootDeviceFileName), []byte("/dev/vda1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "initrd"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := stageInstalledLinuxBootArtifacts(vmDir, mountPoint); err != nil {
		t.Fatalf("stageInstalledLinuxBootArtifacts: %v", err)
	}
	for _, tt := range []struct {
		name string
		want string
	}{
		{"vmlinuz", "MZkernel"},
		{linuxRootUUIDFileName, "uuid-1\n"},
		{linuxRootDeviceFileName, "/dev/vda1\n"},
	} {
		data, err := os.ReadFile(filepath.Join(vmDir, tt.name))
		if err != nil {
			t.Fatalf("read %s: %v", tt.name, err)
		}
		if string(data) != tt.want {
			t.Fatalf("%s = %q, want %q", tt.name, data, tt.want)
		}
	}
	if _, err := os.Stat(filepath.Join(vmDir, "initrd")); !os.IsNotExist(err) {
		t.Fatalf("initrd stat = %v, want not exist", err)
	}
}
