package nixos

import (
	"strings"
	"testing"
)

func TestRenderConfigurationDefaults(t *testing.T) {
	got, err := RenderConfiguration(Config{})
	if err != nil {
		t.Fatalf("RenderConfiguration: %v", err)
	}
	for _, want := range []string{
		`networking.hostName = "nixos-vm";`,
		`users.users.nixos = {`,
		`initialPassword = "nixos";`,
		`users.users.root.initialPassword = "nixos";`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered config missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "openssh.authorizedKeys.keys") {
		t.Errorf("empty SSHPubKey should omit authorizedKeys block:\n%s", got)
	}
}

func TestRenderConfigurationFillsOnlyMissing(t *testing.T) {
	got, err := RenderConfiguration(Config{Hostname: "alpha"})
	if err != nil {
		t.Fatalf("RenderConfiguration: %v", err)
	}
	if !strings.Contains(got, `networking.hostName = "alpha";`) {
		t.Errorf("preserved Hostname missing\n%s", got)
	}
	if !strings.Contains(got, "users.users.nixos = {") {
		t.Errorf("default Username (nixos) missing when Username is empty\n%s", got)
	}
}

func TestValidateConfigurationMissingFields(t *testing.T) {
	full, err := RenderConfiguration(Config{})
	if err != nil {
		t.Fatalf("RenderConfiguration: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(string) string
		wantSub string
	}{
		{"missing openssh", func(s string) string { return strings.Replace(s, "services.openssh.enable = true;", "", 1) }, "services.openssh.enable"},
		{"missing systemd-boot", func(s string) string { return strings.Replace(s, "boot.loader.systemd-boot.enable = true;", "", 1) }, "systemd-boot"},
		{"missing kernelModules", func(s string) string { return strings.Replace(s, "boot.kernelModules = [", "", 1) }, "boot.kernelModules"},
		{"missing root password", func(s string) string { return strings.Replace(s, "users.users.root.initialPassword", "users.users.root.notTheKey", 1) }, "users.users.root.initialPassword"},
		{"missing wheel sudo", func(s string) string { return strings.Replace(s, "security.sudo.wheelNeedsPassword = false;", "", 1) }, "security.sudo.wheelNeedsPassword"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfiguration(tt.mutate(full))
			if err == nil {
				t.Fatalf("ValidateConfiguration accepted mutated input")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %v, want substring %q", err, tt.wantSub)
			}
		})
	}
}

func TestValidateConfigurationRejectsMissingProvisionedUser(t *testing.T) {
	template := `services.openssh.enable = true;
boot.loader.systemd-boot.enable = true;
boot.kernelModules = [ "virtio_pci" ];
users.users.root.initialPassword = "x";
security.sudo.wheelNeedsPassword = false;
`
	if err := ValidateConfiguration(template); err == nil || !strings.Contains(err.Error(), "provisioned user") {
		t.Fatalf("expected provisioned-user error, got %v", err)
	}
}
