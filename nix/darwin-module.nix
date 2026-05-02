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
    environment.systemPackages = [ cfg.package ];

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
