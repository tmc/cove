package nixos

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

type Config struct {
	Hostname  string
	Username  string
	Password  string
	SSHPubKey string
}

func (c Config) normalized() Config {
	if c.Hostname == "" {
		c.Hostname = "nixos-vm"
	}
	if c.Username == "" {
		c.Username = "nixos"
	}
	if c.Password == "" {
		c.Password = "nixos"
	}
	return c
}

func RenderConfiguration(c Config) (string, error) {
	c = c.normalized()
	var buf bytes.Buffer
	if err := configurationTemplate.Execute(&buf, c); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func RenderInstallScript(configText string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

disk="${1:-/dev/vda}"
parted "$disk" -- mklabel gpt
parted "$disk" -- mkpart ESP fat32 1MiB 1GiB
parted "$disk" -- set 1 esp on
parted "$disk" -- mkpart primary ext4 1GiB 100%%
mkfs.fat -F 32 "${disk}1"
mkfs.ext4 -F "${disk}2"
mount "${disk}2" /mnt
mkdir -p /mnt/boot /mnt/etc/nixos
mount "${disk}1" /mnt/boot
cat > /mnt/etc/nixos/configuration.nix <<'EOF'
%s
EOF
nixos-install --root /mnt --no-root-passwd
`, configText)
}

func ValidateConfiguration(s string) error {
	required := []string{
		`services.openssh.enable = true;`,
		`boot.loader.systemd-boot.enable = true;`,
		`boot.kernelModules = [`,
		`users.users.root.initialPassword`,
		`security.sudo.wheelNeedsPassword = false;`,
	}
	for _, want := range required {
		if !strings.Contains(s, want) {
			return fmt.Errorf("configuration missing %q", want)
		}
	}
	if !regexp.MustCompile(`users\.users\.[A-Za-z_][A-Za-z0-9_-]* =`).MatchString(s) {
		return fmt.Errorf("configuration missing provisioned user")
	}
	return nil
}

var configurationTemplate = template.Must(template.New("configuration.nix").Parse(`{ config, pkgs, ... }:

{
  imports = [ ];

  boot.loader.systemd-boot.enable = true;
  boot.loader.efi.canTouchEfiVariables = false;
  boot.kernelModules = [ "virtio_pci" "virtio_blk" "virtio_net" "vsock" "vmw_vsock_virtio_transport" ];

  networking.hostName = "{{ .Hostname }}";
  networking.useDHCP = true;
  networking.networkmanager.enable = true;

  time.timeZone = "UTC";
  i18n.defaultLocale = "en_US.UTF-8";

  services.openssh.enable = true;
  services.openssh.settings.PasswordAuthentication = true;

  users.users.root.initialPassword = "{{ .Password }}";
  users.users.{{ .Username }} = {
    isNormalUser = true;
    initialPassword = "{{ .Password }}";
    extraGroups = [ "wheel" "networkmanager" ];
{{- if .SSHPubKey }}
    openssh.authorizedKeys.keys = [ "{{ .SSHPubKey }}" ];
{{- end }}
  };
  security.sudo.wheelNeedsPassword = false;

  environment.systemPackages = with pkgs; [ curl jq vim ];

  system.stateVersion = "25.11";
}
`))
