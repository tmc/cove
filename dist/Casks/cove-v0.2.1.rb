cask "cove" do
  version "0.2.1"
  sha256 "REPLACE_WITH_SHA256_FROM_dist_build-v0.2.1.sh"

  url "https://github.com/tmc/cove/releases/download/v0.2.1/cove_0.2.1_darwin_arm64.tar.gz"
  name "cove"
  desc "macOS and Linux VM management using Apple's Virtualization framework"
  homepage "https://github.com/tmc/cove"

  binary "cove"
end
