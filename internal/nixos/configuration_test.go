package nixos

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRenderConfiguration(t *testing.T) {
	got, err := RenderConfiguration(Config{
		Hostname:  "test-nixos",
		Username:  "alice",
		Password:  "secret",
		SSHPubKey: "ssh-ed25519 AAAA test",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`networking.hostName = "test-nixos";`,
		`users.users.alice = {`,
		`initialPassword = "secret";`,
		`services.openssh.enable = true;`,
		`vmw_vsock_virtio_transport`,
		`ssh-ed25519 AAAA test`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("configuration missing %q\n%s", want, got)
		}
	}
	if err := ValidateConfiguration(got); err != nil {
		t.Fatalf("ValidateConfiguration: %v", err)
	}
}

func TestValidateConfigurationRejectsMissingFields(t *testing.T) {
	if err := ValidateConfiguration(`{ users.users.alice = {}; }`); err == nil {
		t.Fatal("ValidateConfiguration accepted incomplete config")
	}
}

func TestRenderInstallScript(t *testing.T) {
	got := RenderInstallScript("services.openssh.enable = true;")
	for _, want := range []string{
		"nixos-install --root /mnt --no-root-passwd",
		"configuration.nix <<'EOF'",
		"services.openssh.enable = true;",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("install script missing %q\n%s", want, got)
		}
	}
}

func TestConfigurationNixSyntaxWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("nixos-rebuild"); err != nil {
		t.Skip("nixos-rebuild not installed")
	}
	got, err := RenderConfiguration(Config{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := dir + "/configuration.nix"
	if err := os.WriteFile(path, []byte(got), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("nixos-rebuild", "dry-build", "-I", "nixos-config="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nixos-rebuild dry-build: %v\n%s", err, out)
	}
}
