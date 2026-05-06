cask "cove" do
  version "0.3.0"
  sha256 "REPLACE_WITH_SHA256_FROM_dist_build-v0.3.0.sh"

  url "https://github.com/tmc/cove/releases/download/v0.3.0/cove_0.3.0_darwin_arm64.tar.gz"
  name "cove"
  desc "macOS and Linux VM management using Apple's Virtualization framework"
  homepage "https://github.com/tmc/cove"

  binary "cove"
end
