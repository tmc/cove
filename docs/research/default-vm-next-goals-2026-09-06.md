# Default VM — next goals (prioritized)

Follow-up to the boot resolution
([default-vm-boot-resolution-2026-09-06.md](default-vm-boot-resolution-2026-09-06.md))
and setup validation ([default-vm-setup-validation-2026-09-06.md](default-vm-setup-validation-2026-09-06.md)).

The default VM now cold-boots to Setup Assistant (root cause was ASIF disk
corruption — a zeroed directory table pointer — repaired on a clone; suspected
trigger was the installer's `CacheEphemeral` sync mode, since changed to
`CacheDurable`). Work through the goals below **in order**. Do P0 fully before P1.
Commit at the end of each priority so progress is durable.

## P0 — Lock in the win (do this first)

1. **Fix the test-compile break.** `go test ./...` fails to compile at
   `cmd/cove/private_api_display_test.go:458`: undefined
   `privvz.NewMacGraphicsDisplayWithConfigurationError`. This is a dropped/renamed
   binding from the regenerated `private/virtualization`. Reroute it the way the
   other removed wrappers were handled (see
   `cmd/cove/private_api_removed_wrappers_test.go`: guard with
   `objc.RespondsToSelector`, call via `objc.SendIfResponds`, return
   `&objc.UnrecognizedSelectorError{Selector: sel}` when absent). Do NOT weaken the
   test — preserve what it asserts.
2. **Get `go test ./...` green** (or document any remaining failure as pre-existing
   and unrelated, with evidence).
3. **Verify the default still cold-boots clean** with a plain `cove -vm default run`
   (no recovery/resume/boot-command/boot-args overrides) — reaches the hello screen,
   renders correctly. Capture one screenshot as proof.
4. **Land the work as atomic commits.** Group logically (boot/disk repair tooling;
   installer `CacheDurable` fix; OCR/provisioning; test-compile fix). Stage each
   group and use `~/bin/git-auto-commit-message --auto`. Do NOT stage binaries. Do
   NOT hand-write go.sum. Keep the local `github.com/tmc/apple` replacement's
   `x/vzkit/ocr/ocr.go` change in its own checkout/commit.

## P1 — Finish the default end-to-end

Drive the **real** `default` VM (not disposable clones) through Setup Assistant to a
usable desktop: an admin account created, logged in to Finder, and the cove
`vz-agent` installed and reconnecting on boot — matching the known-good `mlx-lm`
reference (agent reconnects in ~16s). Confirm with `cove -vm default ctl status` /
an agent round-trip, not just a screenshot. Preserve the existing clean install;
back up before destructive steps.

## P2 — Harden ASIF durability

1. **Reproduce the corruption** to prove (or disprove) that `CacheEphemeral`
   (sync=None) was the cause: install a throwaway VM the old way, confirm the zeroed
   directory table pointer recurs, then confirm `CacheDurable` avoids it. If it does
   not reproduce, say so — do not treat the installer change as a proven fix.
2. **Add a post-install disk-readability preflight to cove install** so a corrupt
   ASIF never silently ships again: after writing the guest disk, verify it is
   actually readable (valid GPT / partitions / non-zero sector 0, e.g. via
   `diskutil image info` or an equivalent in-process check) and fail the install
   loudly if not. Add a test.

## P3 — Fix the cove IPSW downloader stall

cove's HTTP IPSW downloader has no stall detection or retry — it stalled at
~1 MB/s / 0.5% for hours this session while `curl` pulled the same CDN URL at
10-14 MB/s (HTTP 206 resume worked). Add a stall timeout + resumable retry loop
(Range requests) to the downloader so installs don't hang. Add a test around the
stall/resume logic. Reusable cached IPSW: `~/.vz/cache/RestoreImage.ipsw`
(18 GB, 26.6.2/25G83).

## Constraints (CLAUDE.md)

- Work in the main checkout. Do NOT create commits directly — stage atomic changes
  and use `~/bin/git-auto-commit-message --auto`. Do not annotate commits with
  "Claude Code". Golang-project commit style (≤50-char summary, blank line, body).
- Do not stage binary files. Never hand-write go.sum.
- Run `sudo` commands in an iTerm2 split.
- Write code like Russ Cox would (minimal, clear, stdlib-first).
- Be economical — this was handed off because Claude credits are low; don't spin on
  long boot waits.
