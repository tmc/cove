Verdict: The design is fundamentally broken because its pre-script execution model attempts to write files before guest provisioning is actually complete, and the root-chown model introduces TCC bypass failures that writing via the user-session agent would natively avoid.

### 1. The User Creation Race Condition (S1 - Missed by 007-REVIEW)
**Severity: S1 (Blocker)**
**Citation:** `vzscript.go` ~lines 289-290, `up.go` ~line 280.
**Concern:** The doc states directives run "before the script body" and expand `$USER` using `dscl`. On a fresh `cove up`, the VM boots and the daemon agent becomes reachable *before* the LaunchDaemon's `sysadminctl` finishes creating the user account. `vzscript.go` explicitly documents this: `"On a fresh boot the agent comes up well before the LaunchDaemon's sysadminctl finishes"`. If `# host-cp:` runs before the script body (and thus before the `guest-wait` command that gates on `/var/db/.vz-provisioned`), the user literally does not exist yet. `$USER` expansion will fail, and `chown $USER:staff` will abort the build. 007-REVIEW missed this fatal race condition entirely.
**Concrete change:** Abandon the pre-script declarative lifecycle. File copying must happen sequentially inside the script body after `guest-wait` guarantees the environment is provisioned. 

### 2. Three `vzscripts/` Where Pre-Script Lifecycle Breaks
**Severity: S1 (Blocker)**
**Citation:** `vzscripts/` directory.
**Concern:** Moving file copies to a declarative pre-script directive actively breaks existing recipes that rely on procedural guest state:
1. **`workstation.vzscript` (line 389):** 
   `guest-wait 10m`
   `guest-ping`
   If you declare `# host-cp:` in this file, it executes *before* `guest-wait 10m`. It will attempt to write to `/Users/...` before the disk is ready or the user is provisioned, crashing the recipe immediately.
2. **`xcode.vzscript` (line 396):**
   `? host-cp /Applications/Xcode.app /Applications/Xcode.app`
   This transfers 35GB of data. By keeping it as a runtime command, the script can fail fast on prior steps or conditionally skip it. Hoisting this to a declarative `# host-cp:` blocks the entire pipeline parsing phase on a 35GB network transfer before `guest-ping` has even confirmed the VM is viable.
3. **`claude-code.vzscript` (line 342):**
   `BREW_USER=$(dscl . -read /Groups/admin GroupMembership | ...)`
   `su -l "$BREW_USER" -c 'curl -fsSL https://claude.ai/install.sh | bash'`
   This script dynamically resolves the admin user at runtime to handle execution. A pre-script directive cannot dynamically resolve variables derived from guest shell execution, breaking any attempt to copy `~/.claude/settings.json` into the correct `$BREW_USER` directory.

### 3. Daemon-streams-then-chowns vs. User-Agent
**Severity: S1 (Blocker)**
**Citation:** `agent_control.go` ~lines 40, `agent_client.go` ~lines 36, `agent_inject.go` ~line 81.
**Concern:** The design doc mandates using the daemon agent (root) to write files, then `chown`ing them, to "avoid TCC issues." This is wrong on modern macOS. Root does *not* magically bypass TCC for protected user directories (e.g., `~/Library/Application Support`) unless the `vz-agent` LaunchDaemon has been explicitly granted Full Disk Access (FDA) in System Settings — which it hasn't, because `cove` injects it silently. 
Conversely, `agent_control.go` line 40 explicitly maps port 1025 to the "User agent: user session, TCC/FDA grants." This `LaunchAgent` runs in the `Aqua` session type. Writing directly via `UserAgentClient` (port 1025) natively executes in the user's GUI context, inheriting their SecureToken state for FileVault, natively registering with APFS, and correctly triggering Aqua TCC prompts if needed. 
**Concrete change:** Drop the daemon `install -d` + `chown` dance. Route all user-directed `host-cp` traffic exclusively through port 1025 (`UserAgentClient.CopyIn` / `WriteFile`). 

### 4. Classifier Stress-Test (False Positives & Negatives)
**Severity: S2**
**Concern:** The section 3.1 heuristic classifier fails catastrophically against real-world file shapes. 
**Five False Positives (Harmless files flagged as secrets):**
1. `~/.bash_history`: Mode 0600 + owned by user -> *Definitely-secret* (Triggers hard blockers).
2. `~/.lesshst`: Mode 0600 + owned by user -> *Definitely-secret*.
3. `~/.ssh/known_hosts`: Sits in `~/.ssh/` -> *Likely-secret* (It's strictly public server keys).
4. `public_key.pem`: Matches `*.pem` -> *Likely-secret* (Explicitly public crypto material).
5. `~/.vscode/theme_token_colors.json`: Matches `*token*` -> *Likely-secret* (Just UI config).
**Five False Negatives (Critical secrets allowed silently as public-config):**
1. `~/.npmrc`: Typically 0644, contains raw npm auth tokens -> *Public-config*.
2. `~/.config/gh/hosts.yml`: Typically 0644, contains GitHub OAuth tokens -> *Public-config*.
3. `~/.pypirc`: Typically 0644, contains PyPI API passwords -> *Public-config*.
4. `~/.zshrc`: Mode 0644, frequently contains `export OPENAI_API_KEY=sk-...` -> *Public-config*.
5. `~/.cargo/credentials.toml`: Mode 0644, crates.io tokens -> *Public-config*.
**Concrete change:** Delete Section 3.1 entirely. Adopt 007-REVIEW's recommendation of explicit path enumeration and content-sniffing (regexing for `sk-...`, `xoxb-...`) rather than naive filename and `0600` heuristic matching.

### 5. Keep or Kill the Runtime `host-cp` Engine Command
**Severity: S2**
**Citation:** `vzscript.go` ~line 299.
**Decision:** **KILL the `# host-cp:` directive. KEEP the `host-cp` runtime engine command.**
**Defense:** Vzscripts are imperative `txtar` archives executed sequentially. Declarative file copies that bypass script ordering fundamentally clash with macOS provisioning realities (waiting for `sysadminctl` to finish, waiting for `iTerm2.app` to initialize its sandbox containers, evaluating `[conditions]`). The engine command `host-cpCmd` already exists in `vzscript.go`. Upgrading this command to support the new consent-caching and TCC-aware user-agent routing gives you 100% of the security benefits of the design doc with zero of the lifecycle and race-condition regressions. 

### Things the Design Got Right
* Acknowledging that the `cove ctl agent-cp` + `cove ctl --daemon agent-exec chown` two-step is hostile UX and needs abstraction.
* Pointing the audit log directly to `~/.cove/host-cp.log` for user transparency rather than hiding it in system logs.
* Keeping the command purely filesystem-focused and punting on network URIs (leave that to `curl` or `005-v04-secrets-architecture.md`).

### Open Questions Worth Raising in Council
* If we keep `host-cp` as a runtime command, how does `cove build` handle OCI layer caching? Does a runtime `host-cp` invalidate the layer hash dynamically, or do we require authors to declare `# host-cp-allows: <path>` in the header purely for cache invalidation inputs?
* How should we handle `host-cp` targeting `/etc/` or `/Library/` which genuinely *do* require the daemon agent (root), versus `~/Library/` which requires the user agent? Does the engine command auto-negotiate the agent based on the target path prefix?

***

**Verdict:** The design is not ready to implement. The declarative pre-script execution model is fatally flawed due to user-creation races, and the macOS permission model is backward. Scrap the `# host-cp:` directive entirely, rebuild the security and consent mechanics around the existing `host-cp` runtime command, and route user-bound file writes through the port 1025 `UserAgentClient` to natively solve TCC/FDA constraints.
