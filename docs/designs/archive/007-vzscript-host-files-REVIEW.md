# Review: 007-vzscript-host-files.md

**Reviewer**: independent (cold-read)
**Date**: 2026-04-20
**Reviewing**: draft v1

## Top-line verdict

**Send back for rewrite.** Not because the directive is wrong — the directive is roughly right and overdue — but because the security story (the longest section in the doc) leans on heuristics that do not actually deliver the property the doc claims, and the lifecycle model has an unforced error that will bite the first non-trivial recipe to use it.

The cleanup work (one canonical name for "copy a host file", killing the manual two-step) is unambiguously good. Ship that part. The classifier and consent-cache design needs another pass.

---

## Concerns, ranked by severity

### 1. The "definitely-secret" classifier is bypassable in one extra `cp`, and the false-positive rate will train users to mash `always-recipe`

Severity: **high**. The classifier as described keys on:

- pathname patterns (`*.pem`, `*credential*`, dirs under `~/.aws/`)
- mode `0600` + ownership = current user

Both are trivially defeated:

- `cp ~/.aws/credentials /tmp/aws.json && chmod 0644 /tmp/aws.json` — now public-config class.
- A `~/.zshrc` that contains `export ANTHROPIC_API_KEY=sk-...` — one of the most common secret-leak patterns in 2026 — sails through as public-config because it is `.zshrc` with mode `0644`.
- `~/.netrc` is on the list, but the canonical place teams keep API tokens today is `~/.config/$TOOL/config.{json,yaml}` and the heuristic doesn't catch any of those (`~/.config/gh/hosts.yml`, `~/.config/op/config`, `~/.config/fly/config.yml`, `~/.config/litellm/config.yaml`, `~/.config/openai/auth.json`...).

Meanwhile, ordinary harmless dotfiles will trip the heuristic: `~/.gitconfig` with mode `0600` (a common defensive default for users who have learned to be careful) becomes definitely-secret and prompts every run forever — because per §3.2 the definitely-secret class doesn't even allow `always-recipe` without `--trust-secrets`. By month two of v0.3 use, every user runs `cove vzscript` with `--trust-secrets` aliased into their shell, and the classifier has been neutralized.

**Suggested change.** Stop trying to classify by content/path heuristics. Instead:

1. Drop the three-tier model. Use a single tier: "outside the recipe's declared `# host-cp:` allowlist of source paths, refuse." The recipe has already enumerated which host paths it touches; the parser knows the full set at parse time. The user is consenting to *that list*, not to a runtime classification.
2. The prompt at consent time shows the *exact list of host paths* the recipe will read (with sizes and modes), not a per-file walk. One decision per recipe-run, not N. This eliminates alert fatigue without weakening the model.
3. If you still want a "warn loudly" tier, base it on `xattr com.apple.metadata:kMDItemWhereFroms` (was this downloaded?) and on actual file content sniffing for high-entropy strings matching `(sk|ghp|xoxb)-...`, not on filename. The Vision framework already lets us peek at content; for textual configs under, say, 256 KB we can do a regex pass for canonical token shapes (Anthropic, OpenAI, GitHub PAT, Slack, AWS access key) and surface "this file appears to contain credentials" as a separate signal from path-based heuristics.

### 2. `# accepts-secrets: true` is exactly the kind of opt-in that authors copy-paste

Severity: **high**. The doc already half-acknowledges this when it says "makes intent explicit on the producer side too". In practice: the first recipe author who hits the parser error pastes `# accepts-secrets: true` to make it go away, never thinks about it again, and now every recipe in the wild has it. This is the same dynamic that made `--insecure` a permanent fixture in `curl` invocations.

If the goal is *producer-side intent*, the right tool is not a boolean — it's machine-checkable. Suggestions:

- Make the producer declaration **enumerate the paths**: `# host-cp-allows: ~/.ssh/* ~/.aws/credentials`. The parser then refuses any `# host-cp:` whose host path doesn't match that allowlist. Now you can grep recipes for "what does this thing read" without trusting an unstructured boolean.
- Or: drop the producer opt-in entirely. The runtime consent prompt (concern 1) is what protects the *consumer*; a producer-side flag adds nothing the parse-time path enumeration doesn't already give you.

The current `# accepts-secrets: true` design pretends to be a guardrail and isn't one. Pick a real guardrail or remove the line.

### 3. Per-recipe consent caching is wrong; it should be per-(recipe, host-path-set, vm-identity)

Severity: **high**. The doc picks per-recipe with this rationale: "one cache entry covers all VMs that run the recipe". That is exactly the failure mode worth worrying about.

Concrete scenario: I `cove vzscript run claude-code` against my personal dev VM, click `always-recipe` for `~/.claude/settings.json`. Six months later I clone someone else's repo that includes a `claude-code.vzscript` (same recipe name, different content) and run `cove up -vzscripts claude-code` against a VM I'm building for a customer demo that I'll later ship. My consent silently applies. The recipe contents have changed; my mental model of what `claude-code` does has not been updated; the cache hit is invisible.

The cache key needs at minimum:

- recipe **content hash** (not name). This catches "same name, different content".
- the *set of host paths* the recipe declares. This catches "recipe author added a new host-cp directive in the next version" — which should re-prompt.
- VM identity (machine-id or VM directory path). The doc treats VM-scoping as an opt-in convenience flag. It should be the default. The flag should be `--share-consent-across-vms`, not `--scope-consent-to-vm`. Per-VM by default makes consent *narrower* than the user expects, which is the safe direction for a security-relevant cache.

The "friendlier" argument in the doc is the wrong frame. Re-prompting on a new VM costs the user one keystroke. Silently leaking creds to a hostile VM costs the user a credential rotation.

### 4. Lifecycle: directives run "before the script body" — but the host-cp targets often don't exist until the script runs

Severity: **medium-high**. The doc says directives run before the script body and parents are `mkdir -p`'d. That works for `~/.claude/` (the install script creates it; we can also create it). It fails the moment a recipe wants to drop a config file into a tool's own state dir that *the tool itself* creates with specific perms / SecureToken-aware ownership.

Examples this breaks today:

- `~/Library/Containers/com.googlecode.iterm2/Data/Library/Preferences/com.googlecode.iterm2.plist` — the App Sandbox container is created the first time iTerm2 launches under that user; pre-creating the dir as we propose can break Apple's sandbox container creation (it expects specific ACL bits we won't set with `install -d`).
- `~/Library/Application Support/Code/User/settings.json` — VS Code creates `Code/User/` on first launch and pre-creating it with `0755 user:staff` is fine, but Code rewrites `settings.json` on first launch and may clobber what we wrote.
- `~/.config/op/` for the 1Password CLI — created by `op signin` and the dir has expected mode `0700`. We'd create it `0755` and `op` would either ignore it or fix it, depending on version.

The directive-first model assumes the destination is just a passive filesystem location. It isn't. It's a piece of state another program will treat as an installation marker.

**Suggested change.** Two things:

1. Add `# host-cp-after: <step-name>` (or just allow `host-cp` as a runtime command, see concern 5) for the not-uncommon case where the bytes need to land *after* a specific install step. Don't pretend everything fits the "before the body" model.
2. For the "dot-dir doesn't exist yet" case, document that `install -d` will be issued with mode `0700` if the host equivalent dir is `0700`, mirroring the host's permissions. This at least matches what `op` and similar tools expect.
3. Explicitly call out the App Sandbox container case as **not supported** by host-cp. Recipe authors should use `defaults import` or invoke the app once first.

### 5. Two mental models for "copy a file" is worse than one, and the doc punts on which one wins

Severity: **medium**. The doc identifies the duplication (§6.5) and proposes deprecating the runtime command in v0.3, removing in v0.4. Good. But "directive parsed by harness" vs "engine command" is a real semantic split that won't disappear:

- Directive: declarative, runs before script, no shell expansion of script vars, no conditional logic, can't depend on script outputs.
- Runtime command: imperative, runs in script order, can be inside a `[condition]` block, can depend on prior step outputs.

There are real recipes that need the latter (concern 4 examples). If you delete the runtime `host-cp`, recipe authors will reinvent it as `guest-shell` blocks that call `agent-cp` via `cove ctl`, which is exactly the duct-tape the directive was supposed to replace.

**Suggested change.** Keep both, but unify the *implementation* and *naming*:

- The directive is `# host-cp:` (declarative; harness-parsed).
- The runtime command is `host-cp` (same name, no `#`). It accepts the same argument grammar as the directive.
- Both go through a single `internal/hostcp` package. Both share the consent cache. Both write the same audit log lines.
- The directive is sugar for "run this `host-cp` invocation as the first script step". Document it that way. One mental model, two surface forms.

This also makes the deprecation question moot: you don't deprecate the runtime command, you align it with the directive.

### 6. Argument syntax breaks on paths with spaces and the "[mode] [owner]" trailing slots are positionally ambiguous

Severity: **medium**. `# host-cp: <host-path> <guest-path> [mode] [owner]` is whitespace-split. macOS users have `~/Library/Application Support/`, `~/Library/Mobile Documents/com~apple~CloudDocs/`, and any number of paths with spaces. The doc doesn't say how to quote.

Also: `0644` and `tmc:staff` are both positional and both optional. If a user writes `# host-cp: ~/.foo $HOME/.foo tmc:staff`, is `tmc:staff` the mode or the owner? The current spec would interpret it as the mode, fail the parse, and the error message will be unhelpful.

**Suggested change.** Either:

- Adopt shell-style quoting (a small parser, but well-understood). `# host-cp: "~/Library/App Support/foo" "$HOME/Library/App Support/foo" mode=0644 owner=tmc:staff`
- Or go full key=value: `# host-cp: src="..." dst="..." mode=0644 owner=tmc:staff`. Ugly but unambiguous and self-documenting. Given that this is a security-relevant directive, unambiguous wins.
- Reject unquoted paths containing whitespace at parse time with a specific error.

The current spec, if shipped as-is, will produce silent-misparse bugs in the wild within a week.

### 7. `# host-cp?:` for "optional" is the wrong default direction and the migration story is broken

Severity: **medium-low**. The doc makes "missing host file is an error" the default and `# host-cp?:` opt-in for optional. Direction is right. But:

- The `?` suffix on the directive name is visually noisy and easy to miss in code review. `# host-cp:` and `# host-cp?:` differ by one character.
- The migration story — author writes `host-cp?:`, later wants it required — requires editing the directive itself. Compare with: `# host-cp: src dst optional=true`. Toggling the flag is local.
- "Missing on host" and "user denied consent" are different failure modes. The current design conflates them: `# host-cp?:` means "tolerate either". Should be separable.

**Suggested change.** Use an explicit flag on the directive: `# host-cp: src dst optional=true`. And distinguish:

- `optional=true` — tolerate "host file does not exist".
- `on-deny=skip` (vs default `on-deny=fail`) — tolerate "user denied consent at the prompt".

Recipes that legitimately want "copy if you have it, no big deal" (e.g., `~/.ssh/id_ed25519` for a VM that *might* be used for git push but works fine without it) want `optional=true on-deny=skip`. Recipes that want "you must have the file but if you don't trust me with it, that's fine, run me without it" want `optional=false on-deny=skip`.

---

## What the doc gets right and should not be rewritten

- **The directive should exist.** The two-step manual recipe in §8 is genuine evidence of overdue cleanup work. Don't lose this momentum to bikeshedding.
- **`install -d` for parent dirs as the target owner, not root.** This is exactly the bug the existing flow has. Keep this regardless of how the rest of the design changes.
- **Daemon-streams-then-chowns.** Right call. Avoids the TCC-on-user-agent-can't-write-to-Library trap. Don't expose a "use user agent" knob.
- **Audit log to `~/.cove/host-cp.log`.** Cheap, useful, and the framing ("for the user, not for security") is honest.
- **Phasing.** v0.3 ship, v0.4 align with secrets architecture (so `# host-cp: 1password://...` reuses the same adapter registry from doc 005). This is the right sequencing — don't try to land both in v0.3.
- **Punting on symlinks, network paths, large files, binaries.** All of these are correct punts for v0.3. Especially network paths — keeping the directive filesystem-only is the sharp boundary the doc claims it is.
- **Not a replacement for `# inject:`.** Right. They're different lifecycle slots. Keep the distinction explicit in the docs.

---

## Open questions for Council

1. **Consent UX in non-interactive but non-CI contexts.** The doc handles `CI=1` and `--allow-host-cp` but not the in-between case: `cove up` invoked from a Makefile target on a developer's laptop. No CI env var; stdin is a pipe; user expected the build to "just work". Default-deny will surprise them. Default-allow is dangerous. Proposal: emit a clear advisory to stderr ("this script needs host-cp consent; either run with -t or pass --allow-host-cp") and exit non-zero. Council should ratify this as the third bucket.

2. **Interaction with `cove build` cache invalidation.** The doc's §6.6 hand-waves that "cache hits on a recipe with host-cp directives are rare". For a docker-build-shaped feature, a recipe that *never* cache-hits is a misfeature — every layer below it loses cacheability. Council should decide: do `# host-cp:` directives participate in the layer hash (correct, but defeats caching) or do they get stripped from the digest input (cacheable, but means two builds with different `~/.claude/settings.json` produce the same image digest — bad for provenance). Neither answer is obviously right; pick deliberately.

3. **Should `cove vzscript run` against a *running* VM (not during install) be allowed to use `# host-cp:` at all?** The threat model in the doc assumes provisioning; the directive will work at any time. A recipe re-run six months later might see a *very* different host filesystem than at first run. Worth a paragraph in the spec.

4. **Relation to `# secret-from:` from doc 005.** If `# host-cp: 1password://item path/in/guest` becomes a thing in v0.4, do we want a single directive (`# host-cp:` with URI sources) or two (`# host-cp:` for filesystem, `# secret-from:` for secret stores)? The doc says reuse the adapter registry, which I read as "two directives, shared backend". Confirm that's the intent, because the alternative ("one directive, scheme-driven") is also defensible and the choice locks in the surface area.

5. **Should denied consent block the script, or just block the directive?** Doc implies block-the-directive (recipe sees the file as missing and decides). For non-optional `# host-cp:`, this means denial fails the script. For `# host-cp?:`, denial is silent. Confirm this matches Council's threat model — a user who clicks "no" might expect the *whole run* to abort, not just one file to silently fall through.

---

## Bottom line

The "remove the two-step manual dance" goal is legitimate and the directive is roughly the right shape. The security story needs to be rebuilt around path enumeration + content-based detection rather than filename heuristics + producer booleans. The lifecycle is too rigid for the App-Sandbox / first-launch cases that motivate half the use cases. Fix the classifier, fix the consent cache scoping, fix the parse grammar, then ship.
