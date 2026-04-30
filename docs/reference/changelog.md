---
title: Changelog
---
# Changelog

All notable changes to cove are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/).

## Unreleased

### Added
- Multi-page documentation site with mdBook
- Custom CSS for branded docs
- Integration test infrastructure for headed mode and shared folders
- Optional pprof server for runtime profiling (`-pprof` flag)
- Linux guest support for Ubuntu, Debian, Fedora, and Alpine, including Rosetta defaults and nested virtualization where supported
- Local content-addressed store under `~/.vz/store/` with build-cache reachability for GC
- `cove build --dry-run` cache-key planning, local cache-hit reporting, block-delta primitives, and persistent build-cache metadata
- VM fork/restore tooling and fork-time benchmark harness
- Agent-aware `cove compact` for zeroing guest free space before OCI pushes
- Recovery automation commands and text normalization in VZScript
- Tag sanitization and read-only VirtioFS mount support for shared folders
- Keyboard fallbacks and multilingual detection for Setup Assistant
- Absolute mouse click support in control socket
- Free space check before large disk writes
- Help hints for unknown CLI commands
- Destructive delete confirmation prompts

### Changed
- Renamed project from vz-macos to cove
- Rewrote README for cove launch
- Expanded suspend config fingerprint to track all device types
- Refactored app event loop to use NextEventMatchingMask/SendEvent pattern
- Replaced sudo/osascript privilege escalation with native Security.framework APIs
- Refactored keyboard input and control socket commands
- Migrated boot command DSL to VZScript format
- SIP automation now generates VZScript instead of boot commands
- VM config codec uses format envelope with multi-format encoding
- Linux installer uses staged boot artifacts
- Improved VM close and stop logic ordering
- Writes VM config atomically
- Bounds launch resource sizes
- Caps and times out socket connections
- Bounds socket request lines
- Restricts iTerm2 WebSocket origins to loopback
- Binds cloud-init HTTP to vmnet host IP
- Shell-escapes password reset commands
- Streams guest-exec output to prevent hangs
- Skips install and provisioning when VM already exists (`up` command)
- Applies `runs-on` directive for VZScript recipes in `up` command

### Fixed
- Malformed build/store manifest digests now return validation errors instead of silent success
- Build-cache entries and layer manifests now reject malformed digests before saving or reporting cache hits
- `cove build` non-dry-run use is gated as dry-run-only until the v0.3 VM execution path lands
- Removed title-bar cropping from screenshots; tracks capture bounds instead
- Corrupt suspend state is now dropped before resume attempt
- Aborted curl downloads for IPSW are now killed
- Agent relays stop on context cancellation
- Script render errors are now returned from provisioning
- Separate mutex from exported network stats struct
- Cleaned up HID test formatting
- Corrected inaccurate SIGTRAP claims in authorization code
- Added recovery guidance for proxy errors
- Adapted toolbar image bindings

### Removed
- Local macgo replace directive in go.mod (reverted)
