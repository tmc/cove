# vzscript host-file directives — copying host files into the guest at provision time

**Status**: draft v1
**Author**: cove team
**Date**: 2026-04-20
**Target**: v0.3 (alongside `cove build` work)

---

## 1. Problem

Several real provisioning recipes need host-side files to land in the guest before the recipe is "done":

- `claude-code` — needs `~/.claude/settings.json` and `~/.claude/CLAUDE.md` so the freshly installed `claude` CLI is configured for the user (not a fresh first-run setup).
- `iterm2` — needs `~/Library/Preferences/com.googlecode.iterm2.plist` so iTerm2 boots with the user's keybindings/profiles.
- `git` — needs `~/.gitconfig` and (sometimes) `~/.ssh/id_ed25519` to push.
- `aws`, `gcloud`, `kubectl` — need credential dirs.

Today this is ad-hoc: users run `cove ctl agent-cp` after a vzscript completes, then `cove ctl -use-daemon agent-exec chown` because the daemon-side `agent-cp` lands files as `root:staff`. The user then realizes the chown step needs `--daemon` because the user agent can't chown its own files (TCC). This is fragile and the steps drift between recipes.

We want a single declarative directive in the vzscript that:

1. Names a host source path (with `~` and `$VAR` expansion).
2. Names a guest destination path (with `$USER`, `$HOME` expansion against the guest admin user).
3. Sets the right ownership/mode without a separate chown dance.
4. Refuses to silently copy sensitive files without consent — but doesn't block CI.
5. Works whether the recipe runs as `runs-on: daemon` or in user context.

## 2. Proposed directive

```
# host-cp: <host-path> <guest-path> [mode] [owner]
```

Examples:

```vzscript
# claude-code
# runs-on: daemon
# host-cp: ~/.claude/settings.json $HOME/.claude/settings.json 0644 $USER:staff
# host-cp: ~/.claude/CLAUDE.md     $HOME/.claude/CLAUDE.md     0644 $USER:staff

guest-ping
guest-shell install-claude-code.sh
```

Behavior:

- Parsed by the **Go harness**, not the vzscript engine — same model as `# inject:` from the existing provisioning architecture decision.
- Files are streamed to the guest via the existing `agent-cp` plumbing.
- Defaults: mode `0644`, owner `$USER:staff` (where `$USER` = the guest admin user resolved via the same `dscl` lookup the recipes already do).
- Parent directories are `mkdir -p`'d as the target owner (not as root) so they don't end up `root:staff`.
- Order: directives run **before** the script body, in declaration order. They are part of the recipe's contract, not a side effect of the script.
- A missing host file is an **error** by default. `# host-cp?:` (with `?`) marks it optional, parallel to the `?` prefix on commands.
- Symlinks on the host: follow by default; `# host-cp-link:` for "preserve as symlink" (uncommon, deferred).

## 3. Sensitivity & consent

Host-file copying is the highest-blast-radius thing a vzscript can do — it's the difference between "this recipe sets up Claude Code" and "this recipe also exfiltrates `~/.aws/credentials` to a guest VM that may later be exported". The default has to be **safe enough that a Council reviewer is comfortable with `cove vzscript run <untrusted-recipe>`**.

### 3.1 Sensitivity classification

Source paths are classified at parse time:

| Class | Examples | Default behavior |
|---|---|---|
| **Public-config** | `~/.claude/settings.json`, `~/.gitconfig`, `~/.zshrc` | Copy with one-line summary. No prompt. |
| **Likely-secret** | Anything under `~/.ssh/`, `~/.aws/`, `~/.config/gcloud/`, `~/.docker/config.json`, `~/.netrc`, files with `*key*`, `*token*`, `*credential*` in basename, anything matching `*.pem`/`*.p12`/`*.kdbx` | Prompt: `host-cp would copy ~/.ssh/id_ed25519 (sensitive). Allow? [y/N/always-this-recipe]` |
| **Definitely-secret** | Files mode-bits `0600` and owned by current user, in directories the user has not whitelisted | Prompt with stronger warning, never `always-this-recipe`-able without explicit `--trust-secrets` flag |

The classifier is conservative — false positives (prompting on a non-secret) are fine; false negatives (silently copying a secret) are not.

### 3.2 Consent flags (CLI side)

`cove vzscript run` and `cove up` accept:

- `--allow-host-cp` — accept all `# host-cp:` directives without prompting (CI default if `CI=1` or non-tty + this flag).
- `--deny-host-cp` — reject all `# host-cp:` directives. The recipe sees them as missing files and decides whether to fail.
- (default) — interactive prompt per file, with `[y/N/always-recipe/always-host-path]` choices remembered in `~/.cove/host-cp-allow.json` keyed by `(recipe-name-or-sha256, host-path)`.
- `--trust-secrets` — allow "always" answers for definitely-secret class (otherwise prompts every run for that class).

### 3.3 Recipe-side opt-in

A recipe declaring `# host-cp:` of a likely-secret path **must** also declare `# accepts-secrets: true` at the top, or the parser refuses to load the recipe. This makes intent explicit on the **producer** side too — you can't accidentally write a recipe that copies `~/.ssh/` because the parser will tell you to acknowledge.

### 3.4 Audit trail

Every host-cp invocation appends to `~/.cove/host-cp.log`:

```
2026-04-20T00:45:12Z dflash-autoresearch claude-code ~/.claude/settings.json -> /Users/tmc/.claude/settings.json (4825b mode=0644 owner=tmc:staff classification=public-config consent=auto)
```

The log is for the user, not for security — they can grep it to figure out what their VMs have seen of their home dir.

## 4. Permissioning inside the guest

The host file lands in the guest with the declared owner/mode regardless of which agent receives the bytes. Implementation:

1. Stream the bytes to a `/tmp/cove-host-cp-<rand>` file via the **daemon agent** (root). This avoids TCC issues where the user agent might be denied write access to dot-dirs the user "hasn't visited" yet.
2. Create the destination parent directory(ies) as the target owner (`install -d -o $owner -g $group -m 0755`). This is the most common cause of permission bugs today (chowning `~/.claude/settings.json` but not `~/.claude/`).
3. `install -o $owner -g $group -m $mode <tmp> <dst>` — atomic move + chown + chmod in one call.
4. Remove the tmp file.

The "default to daemon-side delivery, then chown to user" pattern is why we don't expose a "use user agent" option in the directive. Users shouldn't have to reason about which agent handled the bytes.

## 5. What this is **not**

- **Not a replacement for `# inject:`.** Inject runs at disk-mount time before first boot. Host-cp runs after the agent is up. They're different lifecycle slots.
- **Not for large files.** Anything > 100 MB should use VirtioFS/host-cp tarball/`cove disk attach`. We can add a soft warning for files > 10 MB.
- **Not a bidirectional sync.** One-shot, host → guest only. If you want sync, use a shared folder.
- **Not for binaries.** `host-cp` is for config/state. Binaries should come from a recipe step (`brew install`, native installer, etc.). We can add a rejection on Mach-O magic bytes if needed.

## 6. Open questions

1. **Per-VM consent vs per-recipe consent.** Right now the proposed cache key is `(recipe, host-path)`. Should it also be VM-scoped (`(recipe, host-path, vm-name)`) so consent for VM `dflash-autoresearch` doesn't carry over to `prod-build`? Per-VM is safer; per-recipe is friendlier. Default proposal: per-recipe (one cache entry covers all VMs that run the recipe), opt into per-VM with `--scope-consent-to-vm`.

2. **Templating in the host path.** Should `# host-cp: ~/.config/$RECIPE/config.toml` resolve `$RECIPE` to the recipe name? Useful for "copy the config dir for this tool". Probably yes, but defer until we have a second use case.

3. **Diff before copy.** If the destination already exists with different content, default behavior: **overwrite silently**. The directive is declarative — recipe says "this file should be there", not "create this file if missing". If the user wants the safer behavior, they can use `# host-cp-if-missing:`. Worth it?

4. **Network paths.** Should `# host-cp: https://...` work? No — that's `curl` in the recipe body. Keeping this directive **filesystem-only** is a sharp boundary.

5. **`host-cp` vs `# host-cp:`.** The vzscript engine already has a `host-cp` runtime command (line 694 of vzscript.go) that copies host files into the guest at script-execution time. The proposed `# host-cp:` directive is parsed by the Go harness and runs *before* the script. We should rename one of them. Proposal: keep the directive name `# host-cp:` (declarative, top-of-file), and rename the runtime command to `host-stream` or just remove it (everything it does, the directive does better). Migration: deprecate the runtime command in v0.3, remove in v0.4.

6. **Interaction with `cove build`** (OCI layer caching). If a layer's cache key includes a `# host-cp:` source's content hash, then changing your `~/.claude/settings.json` invalidates the cache. That's correct behavior, but it means cache hits on a recipe with host-cp directives are rare in practice. Probably fine — these recipes aren't meant to be re-cached across machines anyway.

## 7. Phased delivery

**v0.3**:
- Parse the directive.
- Implement the four sensitivity classes + interactive prompt.
- Write the audit log.
- Default deny on non-tty without `--allow-host-cp`.
- Migrate `claude-code.vzscript` and `iterm2.vzscript` to use it.

**v0.4** (with secrets architecture):
- Reuse the secrets adapter framework for `# host-cp: 1password://item-name path/in/guest` so credentials don't have to land on disk before being copied.
- VM-scoped consent option.

**Later**:
- `# host-cp-if-missing:` if there's demand.
- Remove the runtime `host-cp` engine command.

## 8. Non-design: the manual one-off

Until this lands, the manual recipe is:

```bash
# 1. Copy via daemon agent (writes as root:staff because daemon agent runs as root).
cove -vm $VM ctl agent-cp ~/.claude/settings.json /Users/$U/.claude/settings.json
cove -vm $VM ctl agent-cp ~/.claude/CLAUDE.md     /Users/$U/.claude/CLAUDE.md

# 2. Fix ownership (must use --daemon flag to chown root-owned files).
cove -vm $VM ctl agent-exec --daemon -- /usr/sbin/chown $U:staff \
  /Users/$U/.claude/settings.json /Users/$U/.claude/CLAUDE.md
```

That this two-step is the recommended path is itself a sign the directive is overdue.
