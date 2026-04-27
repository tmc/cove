# cove soft-reset isolation matrix

This is the B6 roadmap harness from `docs/designs/014-roadmap-update-post-v0.1.md`. It exists to decide whether per-eval user-account reset is a real isolation primitive or only a best-effort throughput optimization.

Do not run these probes against a VM you care about. Use a forked disposable VM and expect the run to create users, change preferences, touch TCC, mutate Keychain state, and install a test LaunchDaemon.

## Render a Blank Matrix

The default `soft-reset` profile is the six-concern matrix from
`docs/designs/014-roadmap-update-post-v0.1.md` and the planner Gap 1 artifact.

```bash
go run ./cmd/soft-reset-matrix \
  -vm soft-reset-eval-001 \
  -host "$(sw_vers -productVersion) $(sysctl -n machdep.cpu.brand_string)" \
  -out bench/soft-reset/matrix-$(date +%Y%m%d).md
```

The roadmap handoff also calls out a host-boundary checklist. Render it with
`-profile boundary`:

```bash
go run ./cmd/soft-reset-matrix \
  -profile boundary \
  -vm boundary-eval-001 \
  -host "$(sw_vers -productVersion) $(sysctl -n machdep.cpu.brand_string)" \
  -out bench/soft-reset/boundary-matrix-$(date +%Y%m%d).md
```

## Record Results

Each concern is recorded as `pass`, `fail`, `limit`, or `pending`:

```bash
go run ./cmd/soft-reset-matrix \
  -vm soft-reset-eval-001 \
  -result tcc=fail:"Screen Recording grant visible to replacement user" \
  -result keychain=limit:"System trust store is global by design" \
  -out bench/soft-reset/matrix-$(date +%Y%m%d).md
```

Concern IDs:

`soft-reset` profile:

- `tcc`
- `keychain`
- `appleid`
- `globalprefs`
- `securetoken`
- `daemon`

`boundary` profile:

- `tcc`
- `network`
- `disk`
- `kext`
- `machineid`
- `time`

## Soft-Reset Probe Protocol

### TCC Residue

1. Create User A.
2. Grant a visible protected permission to User A.
3. Delete User A.
4. Create User B.
5. Attempt the same access before any User B grant.
6. `pass` if User B is prompted or denied; `fail` if User B inherits access.

### System Keychain Residue

1. Create User A.
2. Install a clearly-named test root certificate or credential.
3. Delete User A.
4. Create User B.
5. Check whether the trust or credential remains effective.
6. `pass` if it is gone; `limit` if the global System Keychain keeps it by design.

### Apple Account Throttling

1. Use a dedicated VM identity and test Apple Accounts.
2. Run bounded login/logout or create/delete cycles.
3. Record the first throttle, lockout, or policy prompt.
4. `limit` if Apple policy blocks the cycle at a stable threshold.

### GlobalPreferences Leakage

1. Create User A.
2. Change a measurable global preference.
3. Delete User A.
4. Create User B.
5. Read the preference before User B changes anything.
6. `pass` if User B sees the baseline; `fail` if User B inherits User A's setting.

### FileVault SecureToken Cycle

1. Use a FileVault-enabled disposable VM.
2. Create and delete users in a bounded loop.
3. Record the first `sysadminctl` SecureToken propagation failure.
4. `pass` if the configured loop completes; `limit` at the first stable failure count.

### Orphaned LaunchDaemon Residue

1. Create User A.
2. Install a clearly-named inert test LaunchDaemon.
3. Delete User A.
4. Create User B.
5. Check whether the plist or launched process remains.
6. `pass` if both are gone; `fail` if either survives.

If three or more concerns are `fail` or hard `limit`, revise the eval-runner soft-reset positioning before publishing throughput claims.

## Boundary Probe Protocol

The boundary profile is a classification pass for claims about the VM/host
boundary. It should not be used as a substitute for the soft-reset matrix.

### TCC Isolation

1. Attempt protected operations from inside the guest.
2. Confirm the host neither grants nor inherits the permission outside cove's explicit control paths.
3. `pass` if the permission remains scoped to guest identity; `fail` if host TCC state changes implicitly.

### Network Isolation

1. Record default guest egress and host reachability.
2. Probe whether the guest can reach host-local services.
3. `pass` if the reachable surface is documented and bounded; `limit` if cove needs explicit controls before using the claim in eval positioning.

### Disk-Write Boundary

1. Write sentinel files inside the guest.
2. Inspect the VM bundle and configured shared paths.
3. `pass` if no host path outside configured VM storage or shares changes.

### Kernel-Extension Visibility

1. Compare guest-visible extension and driver state with host state.
2. Record whether host-only kernel state is exposed to the guest.
3. `pass` if visibility is confined to the guest environment; `limit` if the observed surface needs product documentation.

### Machine-Identity Collision

1. Record machine identifiers in a parent and multiple forks.
2. Check whether identifiers collide in ways that affect external account policy or eval isolation.
3. `pass` if identifiers are unique or documented as intentionally cloned; `limit` at the first policy or isolation conflict.

### Time-Source Determinism

1. Measure wall-clock and monotonic time against the host.
2. Suspend, resume, and fork the guest.
3. `pass` if drift and jumps are bounded enough for the target evals; `limit` if cove must document nondeterminism.
