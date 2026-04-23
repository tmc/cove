# vzscript host-file copying — v2 design

**Status**: draft v2
**Author**: cove team
**Date**: 2026-04-20
**Target**: v0.3 (alongside `cove build` work)
**Supersedes**: `007-vzscript-host-files.md` (v1)
**Reviews folded in**: `007-vzscript-host-files-REVIEW.md`, `007-vzscript-host-files-NLM-REVIEW.md`

---

## 0. What changed since v1

v1 proposed a declarative `# host-cp:` directive parsed by the Go harness and
executed before the script body. Both reviews rejected it. The two killing
arguments:

- **User-creation race.** On a fresh `cove up`, the daemon agent comes online
  before the LaunchDaemon's `sysadminctl` has finished creating the admin user.
  A pre-script directive that expands `$USER` from `dscl` returns empty, and
  `chown $USER:staff` aborts the run. The script body's `guest-wait` is the
  only correct gate — directives running before it are racing the gate.
- **App-state lifecycle.** Real recipes need bytes to land *after* a tool's
  install step has materialized its state dir (1Password's `~/.config/op/`,
  iTerm2's sandbox container, Xcode's 35 GB tree). A pre-script slot can't
  express "after step N."

Plus: v1's heuristic classifier had high false-positive (`~/.bash_history`,
`*.pem` public keys, `~/.ssh/known_hosts`, `~/.vscode/theme_token_colors.json`)
*and* high false-negative (`~/.npmrc`, `~/.config/gh/hosts.yml`,
`~/.zshrc` containing `export OPENAI_API_KEY=sk-...`, `~/.cargo/credentials.toml`)
rates. The classifier was security theatre.

v2 keeps the goals and the security mechanics, but **drops the directive**
entirely and **upgrades the existing `host-cp` engine command** in
`vzscript.go`.

## 1. Problem (unchanged from v1)

Several real provisioning recipes need host-side files in the guest before the
recipe is "done":

- `claude-code` — `~/.claude/settings.json`, `~/.claude/CLAUDE.md`.
- `iterm2` — `~/Library/Preferences/com.googlecode.iterm2.plist`.
- `git` — `~/.gitconfig` (and sometimes ssh keys).
- `aws`, `gcloud`, `kubectl` — credential dirs.

Today this is a two-step manual dance: `cove ctl agent-cp` lands files as
`root:staff`, then `cove ctl --daemon agent-exec chown` fixes ownership. Drift
between recipes is the norm. We want a single recipe-side primitive that:

1. Names a host source path (with `~` and `$VAR` expansion).
2. Names a guest destination path.
3. Puts bytes there with the right ownership/mode in one atomic step.
4. Refuses to silently copy obvious credentials.
5. Works whether the recipe runs as `runs-on: daemon` or in user context.

## 2. Decision

**Kill the `# host-cp:` directive. Keep and upgrade the existing runtime
`host-cp` engine command** (registered in `vzscript.go` as `hostCpCmd`).

Reasoning:

- vzscripts are imperative `txtar` archives executed by `rsc.io/script`. File
  copies that depend on guest state (user existence, app first-launch dirs,
  conditional `[macos]` blocks) need to run in script order, not before it.
- The runtime command already exists, already streams via `agent-cp`, already
  composes with `?` (optional) prefixes and `[condition]` guards, and already
  participates in the script's exit-on-error semantics.
- All v1 security work (consent prompts, audit log, sensitivity gating, CLI
  `--allow-host-cp` / `--deny-host-cp`) attaches to the runtime command
  without losing anything.

The `# inject:` directive precedent is *different*: inject runs at disk-mount
time before the agent exists. Pre-script-but-post-boot has no such excuse.

## 3. Runtime command shape

```
host-cp [flags] <host-path> <guest-path>
```

Flags (all optional, named to avoid v1's positional-ambiguity bug):

| Flag | Meaning | Default |
|---|---|---|
| `-mode 0644` | File mode for destination | source's mode |
| `-owner user:group` | Owner of destination (and any dirs created) | resolved console user `:staff` |
| `-agent daemon\|user\|auto` | Which guest agent receives the bytes | `auto` (see §5) |
| `-optional` | Skip silently if host file is missing | error |
| `-on-deny skip\|fail` | Behavior when consent is denied | `fail` |
| `-force` | Overwrite if destination exists | overwrite (current behavior) |
| `-timeout 30m` | Per-copy timeout | `30m` |

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

# xcode (35 GB; conditional, skippable)
[long] ? host-cp -timeout 2h /Applications/Xcode.app /Applications/Xcode.app
```

`$HOME` / `$USER` are expanded **at command-execution time** by the engine, by
querying the daemon agent for the current console user. This sidesteps the v1
pre-script race: the script's preceding `guest-wait` gates user creation
before any `host-cp` runs.

Quoting follows the script engine's existing rules (shell-style with single or
double quotes). No new parser.

## 4. Sensitivity & consent

The classifier is gone. The replacement is two narrow checks plus an opt-in.

### 4.1 Source allowlist (well-known config paths)

A small built-in list of paths is treated as "ordinary user config, copy with
one-line audit, no prompt":

```
~/.gitconfig          ~/.gitignore_global        ~/.zshrc      ~/.bashrc
~/.bash_profile       ~/.profile                 ~/.tmux.conf  ~/.editorconfig
~/.claude/settings.json ~/.claude/CLAUDE.md
~/.ssh/known_hosts    ~/.ssh/config              ~/.ssh/*.pub
~/Library/Preferences/com.googlecode.iterm2.plist
```

Adding to this list is a code change, deliberately. The list is **paths**, not
patterns — `*.pem` is *not* on it (a public key on disk could be a private one
named badly).

### 4.2 Content sniff (token-shape detection)

For any host file under 256 KB and detected as text, scan for canonical token
shapes before sending bytes:

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

If a match is found, the file is treated as **secret** regardless of where it
lives (catches `~/.npmrc`, `~/.config/gh/hosts.yml`, `~/.zshrc` with
`export ANTHROPIC_API_KEY=...`).

Files larger than 256 KB or detected as binary skip the content sniff. They
are gated by classification (§4.3).

### 4.3 Three outcomes per host file

| Path on allowlist | Token shape detected | Outcome |
|---|---|---|
| yes | no | copy with one-line audit, no prompt |
| yes | yes | prompt: file appears to contain credentials |
| no | no | prompt: unrecognized path, allow? |
| no | yes | prompt with `secret` label; requires `--trust-secrets` to remember |

Prompt format (one prompt per `host-cp` invocation, not per file):

```
host-cp wants to send a host file into vm 'dflash':
  source: ~/.npmrc (1.2 KB, 0644, contains tokens matching: github_pat_, sk-)
  target: /Users/tmc/.npmrc (mode 0644, owner tmc:staff)
  via:    user agent (port 1025)

Allow? [y/N/always-this-recipe-on-this-vm]
```

### 4.4 CLI flags

- `--allow-host-cp` — accept all `host-cp` invocations without prompting.
  Required when stdin is not a tty unless explicit recipe consent is cached.
- `--deny-host-cp` — reject all. Recipe sees them as a copy failure and decides
  (`?`-prefixed `host-cp` calls become no-ops).
- `--trust-secrets` — allow caching of "always" answers for files where token
  shape was detected. Without this flag, secret-class files re-prompt every
  run.
- (default, tty present) — interactive, choices cached in
  `~/.cove/host-cp-allow.json`.

### 4.5 Recipe-side opt-in

A recipe that wants to copy any file flagged by the content sniff (i.e.
"secret" outcome) must declare:

```
# accepts-secrets: ~/.npmrc ~/.config/gh/hosts.yml
```

This is a path enumeration, **not** a boolean. A boolean (`# accepts-secrets:
true`) is the kind of flag authors copy-paste once and forget; an
enumeration makes the parser refuse a `host-cp` of a path not in the list,
and makes `grep accepts-secrets vzscripts/*.vzscript` a meaningful audit.

The list supports literal paths and globs (`~/.config/gh/*`). `*` does not
match `..` or recurse.

### 4.6 Consent cache key

`(recipe-content-sha256, host-path, vm-machine-id)` — all three components.

- **Recipe content hash, not name.** Per review 1's scenario: cloning a repo
  that contains a `claude-code.vzscript` with a different body should not
  inherit consent given to the previous one with the same name.
- **VM-scoped by default.** Consent for `dflash-autoresearch` does not carry
  to `prod-build`. The previous "friendlier" framing was wrong. The flag is
  `--share-consent-across-vms` (off by default), not the inverse.
- **Per host-path, not per recipe-run.** A recipe that copies four files
  prompts for each unrecognized one on first run, caches each independently.

Cache file: `~/.cove/host-cp-allow.json`. Schema: array of
`{recipe_sha, host_path, vm_machine_id, decision, granted_at}`. Hand-editable.

### 4.7 Audit log

Unchanged from v1. Every invocation, allowed or denied, appends one line to
`~/.cove/host-cp.log`:

```
2026-04-20T00:45:12Z dflash-autoresearch claude-code ~/.claude/settings.json -> /Users/tmc/.claude/settings.json (4825b mode=0644 owner=tmc:staff agent=user class=allowlist consent=cached-recipe)
```

For the user, not for security — useful for "what has this VM seen of my home dir."

## 5. Permissioning inside the guest

v1 mandated daemon-streams-then-chowns. Review 2 correctly pointed out this
fails for TCC-protected user dirs (`~/Library/Application Support/`) where
root without Full Disk Access is *less* privileged than the logged-in user.

v2: the engine **picks the agent based on the destination path prefix**.

```
/etc/      /Library/   /var/   /System/   /Applications/   /usr/      → daemon (port 1024)
/Users/<u>/                                                            → user   (port 1025)
/private/etc/   /private/var/                                          → daemon
~/  (engine-expanded relative to console user $HOME)                   → user
anything else                                                           → daemon
```

Override with `-agent daemon|user`. Default is `auto`.

Why this works:

- Daemon agent runs as root, fine for system paths. It does not need TCC for
  `/etc/` or `/Library/LaunchDaemons/`.
- User agent runs in the console user's Aqua session via LaunchAgent. It
  inherits the user's TCC and Full Disk Access grants, can write to
  sandbox-container dirs, and creates files with correct ownership in one shot
  (no chown).

Implementation note: the user agent currently exposes `UserExec` /
`UserExecStream` but **not** a native `WriteFile` / `CopyIn`. v0.3 has two
options:

- **Option A (ship-now):** implement user-bound copy as
  `UserExec(["sh", "-c", "cat > /tmp/host-cp-N && install -m MODE /tmp/host-cp-N DST"])`
  with the host streaming bytes in via stdin. Works today, no proto change.
- **Option B (clean):** add `WriteFile` / `CopyIn` RPCs to the `UserAgent`
  service in `proto/agent.proto`, mirroring `Agent`. Better long-term, but
  bumps the agent version and requires a `cove agent-upgrade` round-trip.

Default to Option A in v0.3, schedule Option B for v0.4 alongside the secrets
adapter work (which will need a clean user-agent file API anyway).

For destinations that need parent-dir creation, use `install -d -o $owner -g
$group -m 0700` (mirror the host's mode if 0700, else 0755). Do **not**
unconditionally pre-create with 0755 — `op`, `gpg`, and `gh` all expect 0700
on their config dirs and will silently misbehave or rewrite perms.

App-Sandbox container paths
(`~/Library/Containers/*/Data/Library/Preferences/`) are explicitly **not
supported** by `host-cp`. Recipe authors should `open -a App` once first to
let macOS create the container with proper ACLs, *then* `host-cp` into it via
the user agent. The engine emits a warning if a destination matches that
pattern and the parent doesn't already exist.

## 6. What this is **not**

- Not a replacement for `# inject:`. Inject runs at disk-mount time before
  first boot. `host-cp` runs against a live agent.
- Not for large files without warning. Files > 100 MB log a warning; recipes
  that legitimately need it (Xcode) should use `?` and pair with disk
  attachment for the next iteration.
- Not bidirectional. Host → guest only. Use `agent-cp` directly or a shared
  folder for the reverse.
- Not for binaries-as-installers. Mach-O detection emits a warning ("did you
  mean to use brew/installer?") but does not refuse — Xcode.app is a
  legitimate Mach-O tree.
- Not for network sources. `host-cp https://...` stays out. Use `curl` in the
  script body.

## 7. Open questions

### 7.1 `cove build` cache invalidation

A `host-cp` invocation in a recipe makes the layer's cache key depend on the
host file's content hash. Two options:

- **Include in digest** (correct, breaks caching) — every change to
  `~/.claude/settings.json` invalidates the layer and everything below it.
- **Strip from digest** (cacheable, lies) — two builds with different host
  files produce identical image digests; reproducibility goes out the window.

Proposal: include by default; offer `host-cp -no-cache-input` for the rare
recipe where the host file is intentionally non-deterministic (e.g., an SSH
key the recipe author *wants* to be machine-local). This shifts the call to
the recipe author with a clear opt-out. **Confirm in Council.**

### 7.2 Per-VM cache vs `cove build` reproducibility

If `--share-consent-across-vms` is off by default (§4.6), a `cove build`
running on one machine will re-prompt every time it builds against a fresh
ephemeral VM. Either:

- `cove build` always implies `--share-consent-across-vms` for its scratch VM.
- Or `cove build` requires explicit `--allow-host-cp` and refuses to prompt.

Lean toward the second: builds should be hermetic in their consent posture,
not lean on a TTY. **Confirm in Council.**

### 7.3 Daemon-vs-user negotiation when console user is missing

If `host-cp` targets `/Users/foo/...` but no GUI user is logged in (fresh boot,
auto-login disabled), the user agent isn't reachable. Options:

- Block with a clear error: "no console user; log in or pass `-agent daemon`."
- Fall back to daemon + chown (the v1 dance).

Lean toward block-with-error: silently switching agents hides the difference
between "wrote with user TCC" and "wrote with root, may break TCC-protected
dirs later." Recipes that legitimately want the daemon path should ask for it.

### 7.4 Templating in host paths

`host-cp ~/.config/$RECIPE/config.toml ...` — useful for
"copy this tool's config dir." Defer until a second concrete use case appears.
Not blocking v0.3.

### 7.5 Re-running `host-cp` against a long-lived VM

A recipe re-run six months later may see a *very* different host home dir.
The cache entry from six months ago is still valid by `(recipe-sha, path,
vm-id)`. Should consent expire? Proposal: TTL of 90 days on cached "always"
decisions, displayed at decision time ("remembered until 2026-07-19").

## 8. Phased delivery

**v0.3** (this proposal):

- Add flag-style argument parsing to `hostCpCmd` (named flags, no positional
  mode/owner ambiguity).
- Add `-agent auto` routing based on destination path prefix.
- Implement user-bound copy via `UserExec` + stdin streaming (Option A from
  §5).
- Add the allowlist + token-shape sniff classifier.
- Implement consent prompt, cache, and audit log.
- Wire `--allow-host-cp` / `--deny-host-cp` / `--trust-secrets` flags to
  `cove vzscript run` and `cove up`.
- Migrate `claude-code.vzscript` and `iterm2.vzscript` to use the new flags.
- Delete the v1 `# host-cp:` directive proposal entirely.

**v0.4** (with secrets architecture):

- Add native `WriteFile` / `CopyIn` RPCs to the user agent (Option B).
- Reuse the secrets adapter from doc 005 so
  `host-cp -secret 1password://item ~/.config/gh/hosts.yml` doesn't require
  the credential to land on host disk first.
- 90-day TTL on cached consent (§7.5).
- Per-recipe `# accepts-secrets:` enforcement at parse time.

**Later**:

- Mach-O-aware large-file warnings.
- App-Sandbox container detection improvements.

## 9. Reference: the manual one-off (kept from v1)

Until the upgraded `host-cp` lands, the manual recipe is:

```bash
# 1. Copy via daemon agent (writes as root:staff because daemon agent runs as root).
cove -vm $VM ctl agent-cp ~/.claude/settings.json /Users/$U/.claude/settings.json
cove -vm $VM ctl agent-cp ~/.claude/CLAUDE.md     /Users/$U/.claude/CLAUDE.md

# 2. Fix ownership (must use --daemon flag to chown root-owned files).
cove -vm $VM ctl agent-exec --daemon -- /usr/sbin/chown $U:staff \
  /Users/$U/.claude/settings.json /Users/$U/.claude/CLAUDE.md
```

That this two-step is the documented path is itself the evidence that the
upgrade is overdue.
