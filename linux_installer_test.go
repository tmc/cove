package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateUserDataServer(t *testing.T) {
	config := LinuxProvisionConfig{
		Username:  "ubuntu",
		Password:  "secret",
		Hostname:  "server-vm",
		TimeZone:  "UTC",
		Locale:    "en_US.UTF-8",
		Variant:   LinuxVariantServer,
		AutoLogin: false,
	}

	got := generateUserData(config, false, "")
	if !strings.HasPrefix(got, "#cloud-config\n") {
		t.Fatalf("generateUserData(server) missing cloud-config header\n%s", got)
	}

	for _, want := range []string{
		"id: ubuntu-server",
		"search_drivers: false",
		"shutdown: poweroff",
		"install-server: true",
		"allow-pw: true",
		"systemctl enable ssh",
		"path: /boot/efi",
		"fstype: fat32",
		"size: 1G",
		"grub-efi-arm64",
		"--target=arm64-efi",
		"--removable",
		"--no-nvram",
		"findmnt -no UUID /",
		"search --no-floppy --fs-uuid --set=root",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generateUserData(server) missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "AutomaticLoginEnable=true") {
		t.Fatalf("generateUserData(server) unexpectedly enabled desktop autologin\n%s", got)
	}
	if strings.Contains(got, "/vz-agent") {
		t.Fatalf("generateUserData(server) unexpectedly referenced vz-agent download\n%s", got)
	}
	for _, unwanted := range []string{"<<EOF", "GDMEOF", "SVCEOF"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generateUserData(server) unexpectedly contains heredoc marker %q\n%s", unwanted, got)
		}
	}
}

func TestGenerateUserDataDesktopWithAgent(t *testing.T) {
	old := linuxDesktopInstaller
	t.Cleanup(func() { linuxDesktopInstaller = old })
	linuxDesktopInstaller = "server"

	config := LinuxProvisionConfig{
		Username:  "me",
		Password:  "secret",
		Hostname:  "desktop-vm",
		TimeZone:  "UTC",
		Locale:    "en_US.UTF-8",
		Variant:   LinuxVariantDesktop,
		AutoLogin: true,
	}

	got := generateUserData(config, true, "http://192.168.64.1:1234/vz-agent")
	if !strings.HasPrefix(got, "#cloud-config\n") {
		t.Fatalf("generateUserData(desktop) missing cloud-config header\n%s", got)
	}

	for _, want := range []string{
		"packages:",
		"- ubuntu-desktop",
		"sed -i -e",
		"update-grub",
		"rm -f /target/etc/netplan/00-installer-config*yaml",
		"renderer: NetworkManager",
		"shutdown: poweroff",
		"path: /boot/efi",
		"fstype: fat32",
		"grub-efi-arm64",
		"--target=arm64-efi",
		"findmnt -no UUID /",
		"search --no-floppy --fs-uuid --set=root",
		"AutomaticLoginEnable=true",
		"AutomaticLogin=me",
		"http://192.168.64.1:1234/vz-agent",
		"blkid -L CIDATA",
		"systemctl enable vz-agent",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generateUserData(desktop) missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "id: ubuntu-desktop") {
		t.Fatalf("generateUserData(desktop) unexpectedly selected ubuntu-desktop source\n%s", got)
	}
	if strings.Contains(got, "linux-generic-hwe-24.04") {
		t.Fatalf("generateUserData(desktop) unexpectedly forced HWE kernel\n%s", got)
	}
	for _, unwanted := range []string{"<<EOF", "GDMEOF", "SVCEOF"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generateUserData(desktop) unexpectedly contains heredoc marker %q\n%s", unwanted, got)
		}
	}
}

// TestGenerateUserDataDeclaresNoInteractiveSections verifies the autoinstall
// YAML carries an explicit `interactive-sections: []`. Without this,
// Subiquity defaults to inferring interactive sections from missing config
// keys and may prompt mid-install — defeats the point of autoinstall.
// See ROADMAP #26.
func TestGenerateUserDataDeclaresNoInteractiveSections(t *testing.T) {
	got := generateUserData(LinuxProvisionConfig{
		Username: "u", Password: "p", Hostname: "h",
		TimeZone: "UTC", Locale: "en_US.UTF-8",
		Variant: LinuxVariantServer,
	}, false, "")
	if !strings.Contains(got, "interactive-sections: []") {
		t.Fatalf("autoinstall YAML missing `interactive-sections: []`\n%s", got)
	}
}

// TestLinuxInstallCommandLinePlacesAutoinstallAfterSeparator verifies the
// kernel cmdline puts `autoinstall` AFTER the `---` separator, which is
// where Subiquity (Ubuntu 22.04+) expects to find it for prompt
// suppression. Args before `---` go to the kernel; args after `---` go to
// userspace init / Subiquity.
func TestLinuxInstallCommandLinePlacesAutoinstallAfterSeparator(t *testing.T) {
	got := linuxInstallCommandLine("192.168.64.1:1234")
	sepIdx := strings.Index(got, " --- ")
	if sepIdx < 0 {
		t.Fatalf("cmdline missing `---` separator: %q", got)
	}
	postSep := got[sepIdx+len(" --- "):]
	if !strings.Contains(postSep, "autoinstall") {
		t.Fatalf("autoinstall must appear after `---`; got %q (post-sep: %q)", got, postSep)
	}
	preSep := got[:sepIdx]
	if strings.Contains(preSep, " autoinstall") || strings.HasPrefix(preSep, "autoinstall ") {
		t.Errorf("autoinstall must NOT appear before `---` (Subiquity ignores it there); got %q", got)
	}
	// The cloud-init datasource pointer (ds=nocloud-net) belongs in the
	// kernel arg portion so cloud-init's initramfs hook picks it up.
	if !strings.Contains(preSep, "ds=nocloud-net") {
		t.Errorf("ds=nocloud-net must remain in kernel args (pre-`---`); got %q", got)
	}
}

func TestBuildLinuxCloudInitData(t *testing.T) {
	config := LinuxProvisionConfig{
		Username: "ubuntu",
		Password: "secret",
		Hostname: "seed-vm",
		TimeZone: "UTC",
		Locale:   "en_US.UTF-8",
		Variant:  LinuxVariantServer,
	}

	seed := buildLinuxCloudInitData(config, true, "http://192.168.64.1:9999/vz-agent")

	if !strings.Contains(seed.userData, "http://192.168.64.1:9999/vz-agent") {
		t.Fatalf("buildLinuxCloudInitData user-data missing agent URL\n%s", seed.userData)
	}
	if strings.HasPrefix(seed.autoinstallData, "#cloud-config\n") {
		t.Fatalf("buildLinuxCloudInitData autoinstallData unexpectedly has cloud-config header\n%s", seed.autoinstallData)
	}
	if !strings.Contains(seed.autoinstallData, "autoinstall:\n") {
		t.Fatalf("buildLinuxCloudInitData autoinstallData missing autoinstall root\n%s", seed.autoinstallData)
	}
	if !strings.Contains(seed.autoinstallData, "http://192.168.64.1:9999/vz-agent") {
		t.Fatalf("buildLinuxCloudInitData autoinstallData missing agent URL\n%s", seed.autoinstallData)
	}
	if !strings.Contains(seed.autoinstallData, "blkid -L CIDATA") {
		t.Fatalf("buildLinuxCloudInitData autoinstallData missing CIDATA fallback\n%s", seed.autoinstallData)
	}
	if !strings.Contains(seed.metaData, "instance-id: seed-vm") {
		t.Fatalf("buildLinuxCloudInitData meta-data missing hostname\n%s", seed.metaData)
	}
	if seed.vendorData != "#cloud-config\n{}\n" {
		t.Fatalf("buildLinuxCloudInitData vendor-data = %q, want %q", seed.vendorData, "#cloud-config\n{}\n")
	}
}

func TestBuildLinuxInstallSeedDataForDistros(t *testing.T) {
	tests := []struct {
		name    string
		variant LinuxVariant
		file    string
		want    []string
	}{
		{
			name:    "debian",
			variant: LinuxVariantDebian,
			file:    "preseed.cfg",
			want:    []string{"d-i netcfg/get_hostname string debian-vm", "pkgsel/include string openssh-server", "partman-auto/method string regular"},
		},
		{
			name:    "fedora",
			variant: LinuxVariantFedora,
			file:    "ks.cfg",
			want:    []string{"text", "network --bootproto=dhcp", "user --name=fedora"},
		},
		{
			name:    "alpine",
			variant: LinuxVariantAlpine,
			file:    "setup-alpine.answers",
			want:    []string{"HOSTNAMEOPTS=\"-n alpine-vm\"", "DISKOPTS=\"-m sys /dev/vda\"", "USEROPTS=\"-a -u alpine\""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			user := defaultLinuxUser(tc.variant)
			seed := buildLinuxCloudInitData(LinuxProvisionConfig{
				Username: user,
				Password: user,
				Hostname: tc.name + "-vm",
				TimeZone: "UTC",
				Locale:   "en_US.UTF-8",
				Variant:  tc.variant,
			}, false, "")
			got := string(seed.files[tc.file])
			if got == "" {
				t.Fatalf("missing seed file %s in %#v", tc.file, seed.files)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("%s missing %q\n%s", tc.file, want, got)
				}
			}
			if seed.files["user-data"] == nil || seed.files["meta-data"] == nil {
				t.Fatalf("seed files missing cloud-init compatibility files")
			}
		})
	}
}

func TestLinuxInstallCommandLineForVariant(t *testing.T) {
	tests := []struct {
		name    string
		variant LinuxVariant
		want    string
	}{
		{"ubuntu", LinuxVariantServer, "ds=nocloud-net;s=http://192.168.64.1:3003/"},
		{"debian", LinuxVariantDebian, "preseed/url=http://192.168.64.1:3003/preseed.cfg"},
		{"fedora", LinuxVariantFedora, "inst.ks=http://192.168.64.1:3003/ks.cfg"},
		{"alpine", LinuxVariantAlpine, "apkovl=http://192.168.64.1:3003/cove.apkovl.tar.gz"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := linuxInstallCommandLineForVariant(tc.variant, "192.168.64.1:3003")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("linuxInstallCommandLineForVariant(%s) missing %q in %q", tc.variant, tc.want, got)
			}
		})
	}
}

func TestLinuxInstallCommandLine(t *testing.T) {
	if got, want := linuxInstallCommandLine("192.168.64.1:3003"), "boot=casper ip=dhcp ds=nocloud-net;s=http://192.168.64.1:3003/ console=tty0 --- autoinstall"; got != want {
		t.Fatalf("linuxInstallCommandLine() = %q, want %q", got, want)
	}
}

func TestDefaultLinuxProvisionConfig(t *testing.T) {
	oldDistro, oldDesktop := linuxDistro, linuxDesktop
	linuxDistro, linuxDesktop = "", false
	defer func() {
		linuxDistro, linuxDesktop = oldDistro, oldDesktop
	}()
	got := DefaultLinuxProvisionConfig()
	if got.InstallAgent {
		t.Fatalf("DefaultLinuxProvisionConfig InstallAgent = true, want false")
	}
	if got.AutoLogin {
		t.Fatalf("DefaultLinuxProvisionConfig AutoLogin = true, want false")
	}
}

func TestParseLinuxVariant(t *testing.T) {
	tests := []struct {
		name    string
		distro  string
		desktop bool
		want    LinuxVariant
		wantErr bool
	}{
		{"default", "", false, LinuxVariantServer, false},
		{"ubuntu", "ubuntu", false, LinuxVariantServer, false},
		{"desktop", "ubuntu", true, LinuxVariantDesktop, false},
		{"debian", "debian", false, LinuxVariantDebian, false},
		{"fedora", "fedora", false, LinuxVariantFedora, false},
		{"alpine", "alpine", false, LinuxVariantAlpine, false},
		{"desktop mismatch", "fedora", true, "", true},
		{"unknown", "arch", false, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLinuxVariant(tc.distro, tc.desktop)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseLinuxVariant(%q, %v) err = nil, want error", tc.distro, tc.desktop)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLinuxVariant(%q, %v): %v", tc.distro, tc.desktop, err)
			}
			if got != tc.want {
				t.Fatalf("parseLinuxVariant(%q, %v) = %q, want %q", tc.distro, tc.desktop, got, tc.want)
			}
		})
	}
}

func TestParseUpFlagsLinuxDistroDefaultUser(t *testing.T) {
	cfg, err := parseUpFlags([]string{"-linux", "-distro", "alpine", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if cfg.user != "alpine" {
		t.Fatalf("cfg.user = %q, want alpine", cfg.user)
	}
	if cfg.password != "alpine" {
		t.Fatalf("cfg.password = %q, want alpine", cfg.password)
	}
	if cfg.distro != "alpine" {
		t.Fatalf("cfg.distro = %q, want alpine", cfg.distro)
	}
	if cfg.gui {
		t.Fatalf("cfg.gui = true, want false")
	}
}

func TestLinuxVariantInstallISOVariant(t *testing.T) {
	if got, want := LinuxVariantDesktop.installISOVariant(), LinuxVariantDesktop; got != want {
		t.Fatalf("LinuxVariantDesktop.installISOVariant() = %q, want %q", got, want)
	}
	if got, want := LinuxVariantServer.installISOVariant(), LinuxVariantServer; got != want {
		t.Fatalf("LinuxVariantServer.installISOVariant() = %q, want %q", got, want)
	}
	for _, variant := range []LinuxVariant{LinuxVariantDebian, LinuxVariantFedora, LinuxVariantAlpine} {
		if got := variant.installISOVariant(); got != variant {
			t.Fatalf("%s.installISOVariant() = %q, want %q", variant, got, variant)
		}
	}
}

// TestLinuxVariantInstallISOVariantOEM verifies that opting in to the OEM
// installer keeps the Desktop ISO instead of falling back to the Server ISO.
func TestLinuxVariantInstallISOVariantOEM(t *testing.T) {
	old := linuxDesktopInstaller
	t.Cleanup(func() { linuxDesktopInstaller = old })
	linuxDesktopInstaller = "oem"
	if got, want := LinuxVariantDesktop.installISOVariant(), LinuxVariantDesktop; got != want {
		t.Fatalf("oem mode: LinuxVariantDesktop.installISOVariant() = %q, want %q", got, want)
	}
	// Server variant unaffected.
	if got, want := LinuxVariantServer.installISOVariant(), LinuxVariantServer; got != want {
		t.Fatalf("oem mode: LinuxVariantServer.installISOVariant() = %q, want %q", got, want)
	}
}

// TestGenerateUserDataDesktopOEM verifies that the OEM-mode autoinstall YAML
// emits `oem: install: true`, drops the `packages: ubuntu-desktop` block
// (already in the Desktop ISO), and selects `source: id: ubuntu-desktop`.
func TestGenerateUserDataDesktopOEM(t *testing.T) {
	old := linuxDesktopInstaller
	t.Cleanup(func() { linuxDesktopInstaller = old })
	linuxDesktopInstaller = "oem"

	config := LinuxProvisionConfig{
		Username:  "me",
		Password:  "secret",
		Hostname:  "desktop-vm",
		TimeZone:  "UTC",
		Locale:    "en_US.UTF-8",
		Variant:   LinuxVariantDesktop,
		AutoLogin: true,
	}
	got := generateUserData(config, false, "")

	for _, want := range []string{
		"oem:",
		"install: true",
		"id: ubuntu-desktop",
		"useradd -m -s /bin/bash",
		"usermod -aG adm,cdrom,sudo,dip,plugdev,users,lpadmin",
		"gnome-initial-setup-done",
		"AutomaticLoginEnable=true",
		"AutomaticLogin=me",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("oem-mode YAML missing %q\n%s", want, got)
		}
	}
	// The Server-ISO `packages: ubuntu-desktop` block must NOT appear in OEM mode
	// — the desktop is already on the live ISO.
	if strings.Contains(got, "- ubuntu-desktop\n") {
		t.Fatalf("oem-mode YAML unexpectedly contains apt-install of ubuntu-desktop\n%s", got)
	}
}

func TestLinuxISODescriptorForVariant(t *testing.T) {
	tests := []struct {
		variant LinuxVariant
		cache   string
		minSize int64
	}{
		{LinuxVariantServer, "linux-ubuntu.iso", 500 * 1024 * 1024},
		{LinuxVariantDesktop, "linux-ubuntu-desktop.iso", 2 * 1024 * 1024 * 1024},
		{LinuxVariantDebian, "linux-debian.iso", 300 * 1024 * 1024},
		{LinuxVariantFedora, "linux-fedora.iso", 500 * 1024 * 1024},
		{LinuxVariantAlpine, "linux-alpine.iso", 30 * 1024 * 1024},
	}
	for _, tc := range tests {
		desc, err := linuxISODescriptorForVariant(tc.variant)
		if err != nil {
			t.Fatalf("linuxISODescriptorForVariant(%s): %v", tc.variant, err)
		}
		if desc.cacheName != tc.cache {
			t.Fatalf("linuxISODescriptorForVariant(%s).cacheName = %q, want %q", tc.variant, desc.cacheName, tc.cache)
		}
		if desc.minSize != tc.minSize {
			t.Fatalf("linuxISODescriptorForVariant(%s).minSize = %d, want %d", tc.variant, desc.minSize, tc.minSize)
		}
		if desc.url == "" {
			t.Fatalf("linuxISODescriptorForVariant(%s).url is empty", tc.variant)
		}
	}
}

func TestInjectAutoinstallIntoInitrd(t *testing.T) {
	if _, err := exec.LookPath("bsdtar"); err != nil {
		t.Skip("bsdtar not installed")
	}
	if _, err := exec.LookPath("cpio"); err != nil {
		t.Skip("cpio not installed")
	}

	tmpDir := t.TempDir()
	rootDir := filepath.Join(tmpDir, "root")
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "existing"), []byte("keep me\n"), 0644); err != nil {
		t.Fatal(err)
	}

	initrdPath := filepath.Join(tmpDir, "initrd")
	createInitrd := exec.Command("sh", "-c", "find . -print | cpio -o -H newc --quiet > ../initrd")
	createInitrd.Dir = rootDir
	if output, err := createInitrd.CombinedOutput(); err != nil {
		t.Fatalf("create initrd: %v: %s", err, output)
	}

	autoinstallConfig := filepath.Join(tmpDir, "user-data")
	const wantAutoinstall = "autoinstall:\n  version: 1\n"
	if err := os.WriteFile(autoinstallConfig, []byte(wantAutoinstall), 0644); err != nil {
		t.Fatal(err)
	}

	gotPath, err := injectAutoinstallIntoInitrd(initrdPath, autoinstallConfig)
	if err != nil {
		t.Fatalf("injectAutoinstallIntoInitrd: %v", err)
	}

	originalBytes, err := os.ReadFile(initrdPath)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(gotBytes), string(originalBytes)) {
		t.Fatalf("injected initrd does not preserve original initrd prefix")
	}
	if !strings.Contains(string(gotBytes[len(originalBytes):]), linuxAutoinstallPath) {
		t.Fatalf("injected initrd missing %q", linuxAutoinstallPath)
	}
	if !strings.Contains(string(gotBytes[len(originalBytes):]), wantAutoinstall) {
		t.Fatalf("injected initrd missing autoinstall contents %q", wantAutoinstall)
	}
}
