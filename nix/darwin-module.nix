{ config, lib, pkgs, ... }:
let cfg = config.services.cove; in
{
  options.services.cove = {
    enable = lib.mkEnableOption "cove macOS VM management";

    package = lib.mkOption {
      type = lib.types.package;
      description = "cove package to use.";
      # No default — requires user to pass `services.cove.package = inputs.cove.packages.${pkgs.system}.cove;`
    };

    qemu = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Install qemu system-wide and point cove at its EDK2 AArch64 pflash
        images. Windows guests run on the direct QEMU/HVF backend, which runs
        qemu-system-aarch64 and qemu-img off PATH and loads the firmware from
        COVE_QEMU_EFI_CODE and COVE_QEMU_EFI_VARS_TEMPLATE; cove's built-in
        firmware search only covers UTM and Homebrew directories, so the
        variables are exported here. Set to false if only macOS and Linux
        guests are used.
      '';
    };

    users = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "User accounts that can run cove without sudo.";
    };

    defaultMemoryGiB = lib.mkOption {
      type = lib.types.ints.positive;
      default = 8;
      description = "Default memory (GiB) for new VMs.";
    };

    defaultCpuCount = lib.mkOption {
      type = lib.types.ints.positive;
      default = 4;
      description = "Default CPU count for new VMs.";
    };
  };

  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ cfg.package ] ++ lib.optional cfg.qemu pkgs.qemu;

    # cove finds qemu-system-aarch64 and qemu-img on PATH, but its firmware
    # search never consults PATH, so name the pflash images explicitly.
    environment.variables = lib.mkIf cfg.qemu {
      COVE_QEMU_EFI_CODE = "${pkgs.qemu}/share/qemu/edk2-aarch64-code.fd";
      COVE_QEMU_EFI_VARS_TEMPLATE = "${pkgs.qemu}/share/qemu/edk2-arm-vars.fd";
    };

    launchd.daemons.cove-helper = {
      serviceConfig = {
        Label = "com.tmc.cove.helper";
        ProgramArguments = [ "${cfg.package}/bin/cove" "helper" ];
        RunAtLoad = true;
        # Per project memory (project_cove_helper_crashloop):
        # KeepAlive=true with HOME unset caused crash-loop in v0.1.x.
        # nix-darwin module ships KeepAlive=false to avoid that regression.
        KeepAlive = false;
        ThrottleInterval = 30;
        StandardOutPath = "/var/log/cove-helper.out.log";
        StandardErrorPath = "/var/log/cove-helper.err.log";
        EnvironmentVariables = {
          # HOME must be set for the helper daemon; root has no $HOME by default
          # under launchd, which caused the v0.1.x crash-loop.
          HOME = "/var/root";
        };
      };
    };
  };
}
