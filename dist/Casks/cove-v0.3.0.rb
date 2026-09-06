cask "cove" do
  version "0.3.0"
  sha256 "REPLACE_WITH_SHA256_FROM_dist_build-v0.3.0.sh"

  url "https://github.com/tmc/cove/releases/download/v0.3.0/cove_0.3.0_darwin_arm64.tar.gz"
  name "cove"
  desc "macOS and Linux VM management using Apple's Virtualization framework"
  homepage "https://github.com/tmc/cove"

  # Windows guests run on the direct QEMU/HVF backend, which shells out to
  # qemu-system-aarch64 and qemu-img and reads the EDK2 AArch64 pflash images
  # shipped by the same formula. macOS and Linux guests do not need it.
  depends_on formula: "qemu"

  binary "cove"

  caveats <<~EOS
    Windows guests use the QEMU/HVF backend. Check its prerequisites with:

      cove doctor qemu
  EOS
end
