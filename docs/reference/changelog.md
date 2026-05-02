---
title: Changelog
---
# Changelog

All notable changes to cove are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/).

## Unreleased

### Added
- `cove run -linux -shell`: attach the host terminal to an interactive guest shell after boot. Allocates a PTY in the guest via the agent, forwards host SIGWINCH to `ResizeExecTTY` and host SIGINT to `SignalExec(SIGINT)` (with the main cove shutdown handler temporarily detached so the first Ctrl-C reaches the guest only). v0.2 stdin is read-only -- bidirectional stdin and a standalone `cove shell <vm>` subcommand are deferred to design 023.
- Multi-page documentation site with mdBook
- Custom CSS for branded docs
- Integration test infrastructure for headed mode and shared folders
- Optional pprof server for runtime profiling (`-pprof` flag)
- Linux guest support for Ubuntu, Debian, Fedora, and Alpine, including Rosetta defaults and nested virtualization where supported
- Local content-addressed store under `~/.vz/store/` with build-cache reachability for GC
- `cove build --dry-run` cache-key planning, local cache-hit reporting, block-delta primitives, and persistent build-cache metadata
- `cove build` non-dry-run execution for local VM-directory bases: cache hits validate metadata and skip guest execution, misses fork a scratch VM and run vzscript steps through the agent, layer manifests are verified before apply, and the final VM directory can be handed to `cove push`
- `# secret:` directive: host environment variables mounted through a guest tmpfs (Linux) or RAM disk (macOS) with guest swap disabled and unmounted before compaction and the layer diff
- Build pipeline compaction: `fast`, `targeted` (default), and `thorough` modes run between guest execution and the layer diff, with the compact mode included in the cache key. `thorough` on macOS guests uses a `dd /dev/zero` + virtio-blk TRIM recipe (APFS rejects `diskutil secureErase freespace`), and is gated by a host-side capacity pre-flight that bails before inflating the sparse image past the host volume's free space. Host-side punch-hole is async and may take a few seconds to settle after the recipe returns.
- Cache TTL handling (`# cache-ttl:`) and full cache entry / layer manifest digest validation before apply
- Internal `cove build` executor scratch lifecycle and cache-hit materialization scaffolding
- Published fork-only and boot-to-agent fork benchmarks on named M4 hardware under `bench/fork-time/`
- OpenAI Agents SDK adapter v1 plus live-smoke and package-check documentation under `adapters/openai-agents-python/`
- VM fork/restore tooling and fork-time benchmark harness
- Agent-aware `cove compact` for zeroing guest free space before OCI pushes
- Recovery automation commands and text normalization in VZScript
- Tag sanitization and read-only VirtioFS mount support for shared folders
- Keyboard fallbacks and multilingual detection for Setup Assistant
- Absolute mouse click support in control socket
- Free space check before large disk writes
- Help hints for unknown CLI commands
- Destructive delete confirmation prompts

### Deferred
The following are explicitly deferred for this RC. Public docs keep this list
visible and consistent across CLI reference, roadmap, and release checklist:
- Registry-base `cove build` execution. Non-dry-run requires a local VM directory base; registry refs stay planning-only and fail with `cove build: non-dry-run requires local VM base directory`.
- Registry cache import/export (`--cache-from`, `--cache-to`). The flags are reserved and fail before planning if used.
- Public curated `cove` image registry and signed agentkit image channels until trademark counsel clears the name or a rename lands.
- External secret stores (1Password, Vault, SOPS, age). v0.3 secrets are host environment variables mounted through tmpfs only.
- BuildKit-style parallel step execution. v0.3 build execution is sequential.
- Packer plugin shim.
- Product-name resolution before any public registry or signed channel ships.

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
- Build cache hits now require complete step metadata, verify it before applying a layer, and honor `# cache-ttl:` expiry
- `cove build <name> --base ... --script ... --dry-run` now accepts the documented command order
- `cove build --dry-run` can use `--store-dir` to inspect cache hits in a specific content store
- `cove build` registry-base non-dry-run use remains gated until base materialization lands; `--push` requires at least one `--tag`
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
