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

func TestLinuxInstallCommandLine(t *testing.T) {
	if got, want := linuxInstallCommandLine("192.168.64.1:3003"), "boot=casper ip=dhcp ds=nocloud-net;s=http://192.168.64.1:3003/ console=tty0 --- autoinstall"; got != want {
		t.Fatalf("linuxInstallCommandLine() = %q, want %q", got, want)
	}
}

func TestDefaultLinuxProvisionConfig(t *testing.T) {
	got := DefaultLinuxProvisionConfig()
	if got.InstallAgent {
		t.Fatalf("DefaultLinuxProvisionConfig InstallAgent = true, want false")
	}
	if got.AutoLogin {
		t.Fatalf("DefaultLinuxProvisionConfig AutoLogin = true, want false")
	}
}

func TestLinuxVariantInstallISOVariant(t *testing.T) {
	if got, want := LinuxVariantDesktop.installISOVariant(), LinuxVariantServer; got != want {
		t.Fatalf("LinuxVariantDesktop.installISOVariant() = %q, want %q", got, want)
	}
	if got, want := LinuxVariantServer.installISOVariant(), LinuxVariantServer; got != want {
		t.Fatalf("LinuxVariantServer.installISOVariant() = %q, want %q", got, want)
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
