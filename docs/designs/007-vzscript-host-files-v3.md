# vzscript host-file copying — v3 design

**Status**: shipped — `host-cp` runtime command (`vzscript.go:157`, `:839`) backed by `CopyIn` + `WriteFile` RPCs (`proto/agent.proto:28-34`).
**Author**: cove team
**Date**: 2026-04-20
**Target**: v0.3 (alongside `cove build` work)
**Supersedes**: `007-vzscript-host-files.md` (v1), `007-vzscript-host-files-v2.md` (v2)
**Reviews folded in**: `007-vzscript-host-files-REVIEW.md`, `007-vzscript-host-files-NLM-REVIEW.md`, plus a third NotebookLM pass on v2

---

## 0. What changed since v2

v2 already killed the v1 `# host-cp:` directive in favor of upgrading the
runtime engine command. The third review pass on v2 surfaced two findings
that move v2 from "ship it" to "ship-blocker." Both are addressed here.

1. **Option A is impossible.** v2 §5 proposed shipping the user-bound copy
   path via `UserExec(["sh","-c","cat > /tmp/host-cp-N && install ..."])`
   with the host streaming bytes in via stdin. The current
   `UserAgentClient.UserExecStream` returns a `connect.ServerStreamForClient`
   — a server→client stream. There is no `Send()` method, and
   `pb.ExecRequest` has no stdin field. **You cannot stream bytes into the
   guest with this RPC.** v0.3 must add native `WriteFile` / `CopyIn` RPCs
   to the user agent before host-cp can route to it. (See §5.)

2. **Symlink evasion of the path allowlist.** v2 §4.1 allowlists
   `~/.gitconfig` (and similar). A malicious recipe can walk the user
   through `ln -s ~/.ssh/id_rsa ~/.gitconfig`, then `host-cp ~/.gitconfig`
   silently exfiltrates the SSH key — basename matches the allowlist, no
   prompt fires. Defense: `lstat` + `filepath.EvalSymlinks` the host source
   *before* allowlist matching, reject if the resolved target lands in a
   sensitive directory. (See §4.1.)

Plus four design decisions previously left as open Council questions are now
resolved (see §7); the remaining genuinely open questions are narrower.

## 1. Problem (unchanged from v1/v2)

Provisioning recipes need host files in the guest before they're "done":

- `claude-code` — `~/.claude/settings.json`, `~/.claude/CLAUDE.md`.
- `iterm2` — `~/Library/Preferences/com.googlecode.iterm2.plist`.
- `git` — `~/.gitconfig` (and sometimes ssh keys).
- `aws`, `gcloud`, `kubectl` — credential dirs.

Today: `cove ctl agent-cp` lands files as `root:staff`, then `cove ctl
--daemon agent-exec chown` fixes ownership. Recipe-side drift is the norm.
We want a single recipe-side primitive that:

1. Names a host source path (with `~` and `$VAR` expansion).
2. Names a guest destination path.
3. Puts bytes there with the right ownership/mode in one atomic step.
4. Refuses to silently copy obvious credentials.
5. Works whether the recipe runs as `runs-on: daemon` or in user context.

## 2. Decision (unchanged from v2)

**Kill the `# host-cp:` directive. Keep and upgrade the existing runtime
`host-cp` engine command** (registered in `vzscript.go` as `hostCpCmd`).

Reasoning unchanged: vzscripts are imperative; pre-script slots can't gate
on user creation, app-state materialization, or `[condition]` guards.

## 3. Runtime command shape

```
host-cp [flags] <host-path> <guest-path>
```

Flags (named, not positional, to dodge v1's `[mode] [owner]` ambiguity):

| Flag | Meaning | Default |
|---|---|---|
| `-mode 0644` | File mode for destination | source's mode |
| `-owner user:group` | Owner of dst (and any dirs created) | resolved console user `:staff` |
| `-agent daemon\|user\|auto` | Which guest agent receives the bytes | `auto` (see §5) |
| `-optional` | Skip silently if host file is missing | error |
| `-on-deny skip\|fail` | Behavior when consent denied | `fail` |
| `-force` | Overwrite if dst exists | overwrite (current behavior) |
| `-timeout 30m` | Per-copy timeout | `30m` |
| `-allow-binary` | Allow Mach-O / unknown-magic files (see §4.4) | reject |
| `-no-cache-input` | Strip from `cove build` layer digest (§7.1) | include |
| `-recursive` | Allow directory bundles via tar stream (§3.1) | files only |

Examples:

```vzscript
# claude-code (post-install configuration)
guest-ping
guest-shell install-claude-code.sh
host-cp -mode 0644 ~/.claude/settings.json '$HOME/.claude/settings.json'
host-cp -mode 0644 ~/.claude/CLAUDE.md     '$HOME/.claude/CLAUDE.md'

# iterm2 (after first launch creates the sandbox container)
guest-shell install-iterm2.sh
host-cp -agent user ~/Library/Preferences/com.googlecode.iterm2.plist \
                   '$HOME/Library/Preferences/com.googlecode.iterm2.plist'

# xcode bundle (35 GB; conditional, optional)
[long] ? host-cp -recursive -timeout 2h -no-cache-input \
                 /Applications/Xcode.app /Applications/Xcode.app
```

`$HOME` / `$USER` expand at command-execution time by querying the daemon
agent for the current console user, so the script's preceding `guest-wait`
gates user creation before any `host-cp` runs (no race).

### 3.1 Directory bundles

`-recursive` enables directory copies. Implementation:

1. **Same-volume APFS fast path.** If the host source and the VM's disk
   image both live on the same APFS container, use `clonefile(2)` for an
   instant copy-on-write into a staging area on the disk, then `mv` into
   place inside the guest. Zero-copy for the Xcode-class case.
2. **Slow path (cross-volume or non-APFS).** Stream a tar archive over the
   user/daemon agent's `CopyIn` RPC; untar on the guest side. No
   intermediate disk write on the host.

Mach-O detection (§4.4) runs on every regular file inside the bundle in
recursive mode; bundles containing executables require `-allow-binary`.

## 4. Sensitivity & consent

The classifier is gone. Replaced by symlink-resolved allowlist checks and
content-shape sniffing.

### 4.1 Source allowlist (well-known config paths)

A small built-in list:

```
~/.gitconfig          ~/.gitignore_global        ~/.zshrc      ~/.bashrc
~/.bash_profile       ~/.profile                 ~/.tmux.conf  ~/.editorconfig
~/.claude/settings.json ~/.claude/CLAUDE.md
~/.ssh/known_hosts    ~/.ssh/config              ~/.ssh/*.pub
~/Library/Preferences/com.googlecode.iterm2.plist
```

Adding to this list is a code change, deliberately. Paths, not patterns —
`*.pem` is *not* on it (could be a private key named badly).

**Symlink resolution (S1 fix from v2 review).** Before checking allowlist
membership the engine MUST:

1. `lstat` the host source. If a symlink, `filepath.EvalSymlinks` to the
   real target.
2. Compare the **resolved** absolute path against the allowlist. If the
   resolved path lands in a known-sensitive directory (`~/.ssh/`, `~/.aws/`,
   `~/.config/gcloud/`, `~/.docker/config.json`, `~/.netrc`,
   `~/.config/op/`, `~/.gnupg/`), reject and emit:

   ```
   host-cp: ~/.gitconfig is a symlink to ~/.ssh/id_rsa
            (matches sensitive directory ~/.ssh/) — refusing.
            If this is intentional, reference the real path directly.
   ```

3. If the resolved path is not on the allowlist, the unresolved-path
   allowlist match is **discarded** and §4.3 proceeds with the resolved
   path. The user sees the real path in any prompt, never the symlink.

This costs one stat per copy; negligible.

### 4.2 Content sniff (token-shape detection)

For any host file under 256 KB and detected as text, scan for canonical
token shapes:

```
sk-[A-Za-z0-9]{20,}        # OpenAI / Anthropic
xox[baprs]-[0-9A-Za-z-]+   # Slack
ghp_[A-Za-z0-9]{36}        # GitHub PAT
gho_[A-Za-z0-9]{36}        # GitHub OAuth
github_pat_[A-Za-z0-9_]{82}
AKIA[0-9A-Z]{16}           # AWS access key id
ASIA[0-9A-Z]{16}           # AWS session token
glpat-[A-Za-z0-9_-]{20}    # GitLab PAT
```

Files larger than 256 KB or detected as binary skip the content sniff. They
are gated by classification (§4.3) and the binary check (§4.4).

> **Open question (see §7.6):** the 256 KB cap is a trivial bypass — pad
> credentials with junk past the threshold. Mitigation in §7.6.

### 4.3 Three outcomes per host file

| Path on allowlist (resolved) | Token shape detected | Outcome |
|---|---|---|
| yes | no | copy with one-line audit, no prompt |
| yes | yes | prompt: file appears to contain credentials |
| no | no | prompt: unrecognized path, allow? |
| no | yes | prompt with `secret` label; requires `--trust-secrets` to remember |

Prompt format (one prompt per `host-cp` invocation):

```
host-cp wants to send a host file into vm 'dflash':
  source: ~/.npmrc (1.2 KB, 0644, contains tokens matching: github_pat_, sk-)
  target: /Users/tmc/.npmrc (mode 0644, owner tmc:staff)
  via:    user agent (port 1025)

Allow? [y/N/always-this-recipe-on-this-vm]
```

### 4.4 Binary handling

Mach-O magic byte detection (`feedface`, `feedfacf`, `cefaedfe`, `cffaedfe`,
`cafebabe` for fat) runs on every host source. If a binary is detected:

- Without `-allow-binary`: **reject.** Emit "did you mean to use brew /
  the native installer? `host-cp` is for config and state, not binaries."
- With `-allow-binary`: copy is permitted. The engine **strips
  `com.apple.quarantine` xattr** from the destination after `install` so
  Gatekeeper won't kill the binary on first execution. Logged in the audit
  trail.

This matches v1's "not for binaries" stance but provides an explicit escape
hatch with the right xattr handling for the rare legitimate case (e.g.,
copying `/Applications/Xcode.app` with `-recursive -allow-binary`).

### 4.5 CLI flags

- `--allow-host-cp` — accept all `host-cp` invocations without prompting.
  **Required** (not just defaulted) when stdin is not a tty. Required by
  `cove build` (see §7.2).
- `--deny-host-cp` — reject all. Recipes see them as a copy failure;
  `?`-prefixed `host-cp` calls become no-ops.
- `--trust-secrets` — allow caching of "always" answers for files where
  token shape was detected. Without this, secret-class files re-prompt.
- (default, tty present) — interactive, choices cached in
  `~/.cove/host-cp-allow.json`.

### 4.6 Recipe-side opt-in

A recipe that wants to copy any file flagged by the content sniff (i.e.
"secret" outcome) must declare:

```
# accepts-secrets: ~/.npmrc ~/.config/gh/hosts.yml
```

Path enumeration, **not** a boolean. Glob-supporting (`~/.config/gh/*`).
`*` does not match `..` or recurse. The parser refuses a `host-cp` of a
secret-classified path not in the list. `grep accepts-secrets
vzscripts/*.vzscript` is a meaningful audit query.

### 4.7 Consent cache

Key: `(recipe-content-sha256, host-path, vm-machine-id)`. **All three
required.**

- Recipe content hash, not name — cloning a repo with a same-named recipe
  doesn't inherit consent.
- VM-scoped by default. Flag is `--share-consent-across-vms` (off).
- Per host-path, not per recipe-run.

Cache file: `~/.cove/host-cp-allow.json`. Schema:
`{recipe_sha, host_path, vm_machine_id, decision, granted_at, expires_at}`.
Hand-editable.

**Two invalidation triggers, both active:**

1. **Recipe SHA change.** If the recipe body changes, the SHA changes, and
   the cached entry stops matching. New consent required.
2. **90-day TTL.** Even if the recipe is identical, an "always" decision
   expires 90 days after `granted_at`. Protects against host-side drift —
   the user may have placed a token into a previously-benign path.

Both triggers fire independently. The prompt shows the expiry: "remembered
until 2026-07-19."

### 4.8 Audit log

Unchanged from v1/v2. Every invocation appends one line to
`~/.cove/host-cp.log`:

```
2026-04-20T00:45:12Z dflash claude-code ~/.claude/settings.json -> /Users/tmc/.claude/settings.json (4825b mode=0644 owner=tmc:staff agent=user class=allowlist consent=cached-recipe symlink=no)
```

For the user, not for security.

## 5. Permissioning inside the guest

The engine picks the agent based on destination path prefix:

```
/etc/   /Library/   /var/   /System/   /Applications/   /usr/   → daemon (port 1024)
/private/etc/   /private/var/                                    → daemon
/Users/<u>/                                                      → user   (port 1025)
~/  (engine-expanded relative to console user $HOME)             → user
anything else                                                    → daemon
```

Override with `-agent daemon|user`. Default is `auto`.

Why this works: the daemon agent (root) is fine for system paths but lacks
TCC/FDA grants for protected user dirs. The user agent runs in the console
user's Aqua session, inherits TCC/FDA, and writes with correct ownership in
one shot. Hard error (no silent daemon fallback) if the user agent isn't
reachable when one is required (§7.3 resolved).

### 5.1 User-agent file API (v0.3 ships, **not deferred**)

v2 proposed shipping via `UserExec` + stdin streaming and deferring native
RPCs to v0.4. **That path is impossible** — the existing `UserExecStream`
RPC is server→client only (`agent_client.go:29-39`); there is no `Send()`
on the stream and no `stdin` field on `pb.ExecRequest`.

v0.3 therefore **must** ship native RPCs. Add to
`proto/agent.proto`'s `UserAgent` service:

```proto
// CopyIn streams a file or tar archive from host to guest.
// Mode/owner are applied atomically via tmpfile + install(1).
// For directories, set is_tar=true and stream a POSIX ustar archive.
rpc CopyIn(stream CopyInChunk) returns (CopyInResult);

message CopyInChunk {
  // First chunk has header populated; subsequent chunks have only data.
  CopyInHeader header = 1;
  bytes data = 2;
}
message CopyInHeader {
  string dst_path  = 1;  // absolute guest path
  uint32 mode      = 2;  // unix mode bits
  string owner     = 3;  // "user:group", empty = console user
  bool   is_tar    = 4;  // data stream is a tar archive
  bool   strip_quarantine = 5;  // for -allow-binary
  uint64 total_size = 6; // host's known total (for progress)
}
message CopyInResult {
  uint64 bytes_written = 1;
  string sha256        = 2;
}
```

The daemon `Agent` service gets the same RPC. For the bundle case (§3.1),
the host streams a tar archive; the guest agent untars to a tmpdir and
moves into place atomically.

Atomicity: the agent writes to a same-directory tmpfile with `O_CREAT |
O_EXCL`, calls `fchmod` and `fchown` on the fd, then `renameat`. Truncated
streams (host crashed mid-copy) leave the tmpfile to be cleaned up by a
GC sweep — never half-written destinations.

This is the **single hard agent-protocol bump in v0.3.** It costs a
`cove agent-upgrade` round-trip on existing VMs the first time `host-cp`
runs against them; the upgrade flow already exists.

### 5.2 Parent directories

When parent directories don't exist, the engine creates them with
`install -d -o $owner -g $group -m M`, where `M` is `0700` if the host
source was `0700`-class (anything in `~/.ssh`, `~/.aws`, `~/.config/op`,
`~/.gnupg`) and `0755` otherwise. `op`, `gpg`, `gh`, and `aws` all
silently misbehave if their config dirs end up `0755`.

App-Sandbox container paths
(`~/Library/Containers/*/Data/Library/Preferences/`) are explicitly **not
supported.** Recipe authors `open -a App` once first to let macOS create
the container with proper ACLs, *then* `host-cp` into it. The engine
emits a warning if a destination matches that pattern and the parent
doesn't already exist.

## 6. What this is **not**

- Not a replacement for `# inject:`. Inject runs at disk-mount time before
  first boot. `host-cp` runs against a live agent.
- Not for files > 100 MB without warning. Recipes that legitimately need it
  (Xcode) must use `-recursive` and accept the layer-cache invalidation.
- Not bidirectional. Host → guest only.
- Not for binaries by default. `-allow-binary` opt-in (§4.4).
- Not for network sources. `host-cp https://...` stays out — use `curl`.
- Not a way to bypass App-Sandbox container creation.

## 7. Decisions previously open in v2

These were Council questions in v2; v3 picks one and explains.

### 7.1 `cove build` cache invalidation — **include in digest** (default)

Including the host file's content hash in the OCI layer digest is the
correct default. Stripping it lies about reproducibility — two builds
with different host files would produce identical image digests. The
`-no-cache-input` flag (§3) is the recipe-author's escape hatch for
intentionally machine-local inputs (e.g., a local SSH key for a build VM).

### 7.2 `cove build` consent — **require `--allow-host-cp`** (no prompt)

`cove build` runs in CI / scripted contexts. Falling back to a TTY prompt
or to ambient `--share-consent-across-vms` makes builds non-hermetic and
hangs CI. v0.3: `cove build` refuses to start if any recipe contains a
`host-cp` invocation and `--allow-host-cp` is not set. Clear error,
deterministic failure.

### 7.3 No console user, `/Users/...` target — **hard error** (no fallback)

If `host-cp` targets `/Users/foo/...` (or `~/`) but the user agent isn't
reachable (no GUI user logged in), block with:

```
host-cp: target /Users/tmc/.claude/settings.json requires the user agent,
         but no console user is logged in. Either log in (cove ctl
         user-login), wait for auto-login (-no-resume cold boot), or pass
         -agent daemon to write as root (TCC may break dependent apps).
```

Silent fallback to daemon+chown was the v1 pattern that produced
"works on my machine" failures when TCC-protected dirs (`~/Library/Mail`,
`~/Library/Application Support`) ended up with synthetic ACLs.

### 7.4 User-agent native RPCs — **ship in v0.3** (Option A is impossible)

See §5.1. Not a Council decision; a fact.

### 7.5 Cached "always" consent — **TTL + recipe-SHA**, both active

Both invalidation triggers run independently. SHA-change covers
recipe-side tampering; 90-day TTL covers host-side drift (a token now
sitting in a previously benign path). Either trigger re-prompts.

## 8. Genuinely open questions (v3)

Narrower than v2's set. These need Council attention.

### 8.1 Token-shape sniff size cap

Currently 256 KB. Trivial bypass: a malicious recipe instructs the user to
append junk to a credential file until it crosses the threshold; sniff
skips, and the file passes the no-token-detected branch. Options:

- **Stream-and-scan, no cap.** Run the regex over the entire stream as
  bytes go to the guest. Cost: linear in file size, but you're already
  streaming so it's incremental. *Probably the right answer.*
- **Cap at 256 KB, but content-class becomes "unknown" (not "no tokens
  found") above the cap.** Files > 256 KB at unrecognized paths still
  prompt, just without the "found token X" hint.
- **Cap at 256 KB, accept the bypass for v0.3, fix in v0.4.**

Lean toward stream-and-scan with a hard 100 MB upper bound (above which
the file is too big to be a credential anyway).

### 8.2 Templating in host paths

`host-cp ~/.config/$RECIPE/config.toml ...` — useful for "copy this tool's
config dir, parameterized." Defer until a second concrete use case.

### 8.3 Native MCP tooling for the consent prompt

`cove ctl host-cp-prompts` could surface pending consent decisions over
MCP for an LLM-driven approval flow (relevant for autoresearch-class
agentic VMs). Out of scope for v0.3, listed so we don't paint into a
corner.

### 8.4 Quarantine xattr stripping policy

§4.4 strips `com.apple.quarantine` after install on `-allow-binary` copies.
Question: does this also need to apply to executables *inside* a
`-recursive` bundle copy (e.g., `Xcode.app/Contents/MacOS/Xcode`)? Lean
yes — Gatekeeper otherwise kills the binary even though we copied it
intentionally. Confirm in Council before shipping.

## 9. Phased delivery

**v0.3** (this proposal):

- Add named-flag argument parsing to `hostCpCmd` in `vzscript.go`.
- Add `-agent auto` routing based on destination path prefix.
- **Add `CopyIn` RPCs to `proto/agent.proto`'s `UserAgent` and `Agent`
  services.** Implement in `cmd/vz-agent`. Update `agent_client.go`. Bump
  agent version; rely on the existing `cove agent-upgrade` flow.
- Add the symlink-resolving allowlist + token-shape sniff classifier.
- Add Mach-O magic-byte rejection with `-allow-binary` opt-in.
- Add `-recursive` directory copy: tar stream by default, APFS clonefile
  fast path when same-volume.
- Implement consent prompt, cache (with TTL + SHA invalidation), and audit
  log.
- Wire `--allow-host-cp` / `--deny-host-cp` / `--trust-secrets` to
  `cove vzscript run` and `cove up`.
- Make `cove build` require `--allow-host-cp` upfront (no prompt).
- Migrate `claude-code.vzscript` and `iterm2.vzscript` to the new flags.

**v0.4** (with secrets architecture):

- Reuse the secrets adapter from doc 005 so
  `host-cp -secret 1password://item ~/.config/gh/hosts.yml` doesn't require
  the credential to land on host disk first.
- Stream-and-scan content sniff (§8.1).
- Per-recipe `# accepts-secrets:` enforcement at parse time.
- MCP exposure of pending consent prompts (§8.3) if useful.

**Later**:

- Templating in host paths (§8.2).
- `host-cp -if-missing` if a use case appears.

## 10. Reference: the manual one-off (kept from v1)

Until the upgraded `host-cp` lands:

```bash
# 1. Copy via daemon agent (writes as root:staff because daemon agent runs as root).
cove -vm $VM ctl agent-cp ~/.claude/settings.json /Users/$U/.claude/settings.json
cove -vm $VM ctl agent-cp ~/.claude/CLAUDE.md     /Users/$U/.claude/CLAUDE.md

# 2. Fix ownership (must use --daemon flag to chown root-owned files).
cove -vm $VM ctl agent-exec --daemon -- /usr/sbin/chown $U:staff \
  /Users/$U/.claude/settings.json /Users/$U/.claude/CLAUDE.md
```

That this two-step is the documented path is the evidence that the upgrade
is overdue.

---

## Appendix A — Diff vs v2

| Area | v2 | v3 |
|---|---|---|
| User-agent file delivery | `UserExec` + stdin streaming (Option A); native RPCs in v0.4 | **Native `CopyIn` RPCs in v0.3** (Option A is impossible) |
| Symlink handling | Punted | **`lstat` + `EvalSymlinks` before allowlist check** |
| Directory bundles | "Not for large files" | **`-recursive` + APFS clonefile fast path** |
| Mach-O binaries | "Warning, but not refused" | **Reject by default, `-allow-binary` opt-in, strip quarantine xattr** |
| `cove build` cache | Open question | **Resolved: include in digest by default** |
| `cove build` consent | Open question | **Resolved: require `--allow-host-cp`, no prompt** |
| No console user | Open question | **Resolved: hard error, no fallback** |
| Cache invalidation | TTL OR SHA | **TTL AND SHA (both active)** |
| Token-sniff cap bypass | Not discussed | **Open question §8.1** |
| Quarantine inside bundles | Not discussed | **Open question §8.4** |

## Appendix B — Council asks

1. Confirm token-shape sniff strategy (§8.1) — stream-and-scan vs cap.
2. Confirm quarantine xattr stripping policy for `-recursive` bundles
   (§8.4).
3. Approve the agent-protocol bump (`CopyIn` RPC on `Agent` and
   `UserAgent`) for v0.3 — this is the largest agent-side change since
   the original LaunchAgent split.

## Verified 2026-05-10

- `host-cp <host> <guest>` script command registered at
  `vzscript.go:157`, implemented in `hostCpCmd` (`vzscript.go:839`).
  v3 §0 finding 1 (Option A "stream stdin via UserExecStream") was
  resolved by the native RPCs below.
- Native RPCs landed in `proto/agent.proto`: `CopyIn(stream CopyInChunk)`
  (line 29) and `WriteFile(WriteFileRequest)` (line 34) with their
  message types at lines 181-222.
- Help text in `vzscript_apply.go:72` and `vzscript.go:17` documents
  the 30-minute long-copy timeout per v3 §3.
- Path-allowlist + symlink-evasion defense (v3 §0 finding 2) is NOT
  visibly enforced in `vzscript.go`'s `hostCpCmd`: no `EvalSymlinks`
  or basename-allowlist guard. Callers currently pass arbitrary host
  paths; the v3 hardening is a follow-up.
