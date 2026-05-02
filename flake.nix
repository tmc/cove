{
  description = "macOS and Linux VM management using Apple's Virtualization framework";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      perSystem = flake-utils.lib.eachSystem systems (system:
        let
          pkgs = import nixpkgs { inherit system; };
          lib = pkgs.lib;
          version = "0.1.3";

          meta = {
            description = "macOS and Linux VM management using Apple's Virtualization framework";
            license = lib.licenses.mit;
            platforms = [
              "aarch64-darwin"
              "aarch64-linux"
              "x86_64-darwin"
              "x86_64-linux"
            ];
          };

          cove = pkgs.buildGoModule {
            pname = "cove";
            inherit version;
            # src = ./.; private-repo workaround. Swap to fetchFromGitHub when cove flips public.
            src = ./.;
            # vendorHash will be computed on first nix build; replace lib.fakeHash with the suggested hash.
            vendorHash = lib.fakeHash;
            subPackages = [ "." ];
            inherit meta;
          };

          vz-agent = pkgs.buildGoModule {
            pname = "vz-agent";
            inherit version;
            # src = ./.; private-repo workaround. Swap to fetchFromGitHub when cove flips public.
            src = ./.;
            # vendorHash will be computed on first nix build; replace lib.fakeHash with the suggested hash.
            vendorHash = lib.fakeHash;
            subPackages = [ "cmd/vz-agent" ];
            inherit meta;
          };
        in
        {
          packages = {
            inherit cove vz-agent;
            default = cove;
          };

          apps.default = {
            type = "app";
            program = "${cove}/bin/cove";
          };

          devShells.default = pkgs.mkShell {
            buildInputs = with pkgs; [ go gh goreleaser jq ];
          };
        });
    in
    perSystem // {
      darwinModules.default = import ./nix/darwin-module.nix;
      darwinModules.cove = import ./nix/darwin-module.nix;
    };
}
