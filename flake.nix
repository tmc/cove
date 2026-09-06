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
            # The Windows backend runs qemu-system-aarch64 and qemu-img, which
            # it resolves through PATH, so suffix qemu onto the wrapped
            # command's PATH; suffixed so a qemu the user installed themselves
            # still wins. It also needs the EDK2 AArch64 pflash images, and
            # that lookup does not consult PATH: it reads COVE_QEMU_EFI_CODE
            # and COVE_QEMU_EFI_VARS_TEMPLATE and otherwise searches a fixed
            # list of UTM and Homebrew directories, none of which exist under
            # Nix. Point both variables at this qemu's firmware, as defaults so
            # an explicit setting still wins.
            nativeBuildInputs = [ pkgs.makeWrapper ];
            postInstall = ''
              efiCode=${pkgs.qemu}/share/qemu/edk2-aarch64-code.fd
              efiVars=${pkgs.qemu}/share/qemu/edk2-arm-vars.fd
              for f in "$efiCode" "$efiVars"; do
                test -f "$f" || { echo "cove: $f missing; update the firmware paths for this qemu" >&2; exit 1; }
              done
              wrapProgram $out/bin/cove \
                --suffix PATH : ${lib.makeBinPath [ pkgs.qemu ]} \
                --set-default COVE_QEMU_EFI_CODE "$efiCode" \
                --set-default COVE_QEMU_EFI_VARS_TEMPLATE "$efiVars"
            '';
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
