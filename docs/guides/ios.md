# iOS research bundles

Cove recognizes iOS bundles through the `ios` object in `config.json`.
The experimental native runner opens already-prepared bundles for headless
research runs. Native start, guest boot, and DFU discovery are not verified.
Cove does not provide an iOS firmware importer, patcher, or restore pipeline.
Do not use the macOS IPSW installer to provision an iOS guest.

The runner requires an existing set of `hw.model`, `machine.id`, `aux.img`,
`sep.img`, and `disk.img`, plus a configured boot ROM and optional SEP ROM.
It checks the saved hardware model against the research profile and opens
existing identity and storage files. It does not create an identity or prepare
NVRAM. Preserve these files together when preparing a bundle. GUI, recovery,
save/resume, cloning, provisioning, and guest automation are not supported by
this runner. Audio, touch input, accelerators, and battery devices are not
implemented. Idle and maximum-age policies are rejected; the run-budget policy
still applies.

Validate an idle prepared bundle before attempting a run:

```sh
cove ios validate -vm-dir /path/to/prepared.covevm
```

This command loads `config.json`, checks the saved iOS configuration and runtime
restrictions, and reports file errors together. It does not create a bundle,
write state, acquire a VM lock, or start a native VM. A successful check prints:

```text
Prepared iOS files validated; native start, DFU discovery and guest boot are unverified.
```

Prepare the bundle externally with a matching set of identity, NVRAM, SEP state,
disk, and firmware artifacts. Set `ios.rom` to the relative boot-ROM path in
`config.json`; set `ios.sepROM` only when supplying a separate SEP ROM. Do not
substitute empty files or a macOS hardware model for missing artifacts. Cove has
no command that generates a bootable iOS consistency set.

| Diagnostic | Required correction |
| --- | --- |
| `ios rom: configure a prepared boot rom` | Set `ios.rom` to an existing bundle-relative firmware file. |
| `resolve existing file inside bundle` | Restore the named artifact and check its path and symlink target. |
| `expected a nonempty regular file` | Replace the named empty file, directory, or special file with the prepared artifact. |
| `read/write access required` | Give the running user access to mutable state: `aux.img`, `sep.img`, and `disk.img`. |
| `read access required` | Give the running user read access to the named identity or firmware file. |
| `aliases` | Supply separate artifacts; a hard link, symlink, or repeated path cannot reuse another required file. |
| `runtime nvram updates are unsupported` | Prepare boot arguments in NVRAM externally, then clear `ios.bootArgs`. |

Required files must be nonempty regular files. Relative symlinks are accepted
only when they resolve inside the bundle; absolute symlinks are rejected.
Access checks open existing files without creating, truncating, or writing them.
They do not test whether the bundle directory permits later lock or status-file
creation. Keep the bundle idle during validation and startup: preflight does not
reserve the files or prevent concurrent replacement.

Static success does not decode the saved hardware model or identity, establish
that firmware and state belong together, verify firmware provenance, or prove
native API availability. Firmware provenance and matching state remain
unverified. Native startup separately loads the saved identity and hardware
model and checks host APIs; native guest boot remains unverified.

Run an already-prepared bundle with:

```sh
cove -headless -vm-dir /path/to/prepared.covevm run
cove -headless -vm-dir /path/to/prepared.covevm -force-dfu run
```

Rosetta and clipboard defaults are disabled for iOS bundles. Explicit requests
to enable them are rejected. Stop the foreground process with SIGINT or SIGTERM;
Cove confirms the stopped VM state before reporting shutdown. The runner does
not expose the generic control socket or guest-agent services.

The following illustrative configuration records the default research profile;
it is not a bootable bundle:

```json
{
  "ios": {
    "schemaVersion": 1,
    "profile": "vresearch101",
    "variant": "regular",
    "display": { "width": 1290, "height": 2796, "ppi": 460, "scale": 3 },
    "network": "nat"
  }
}
```

The accepted variants are `less`, `regular`, `dev`, `jb`, and `exp`.
`noBinpack` requires `less`; `noVphoned` is independent of the variant.
These fields record intent and do not run a provisioning pipeline.
Display dimensions and PPI must be positive; scale must also be finite.

Networking accepts `nat`, `none`, or `bridged:<interface>`. Bridge names
cannot contain whitespace, control characters, slashes, or colons. An optional
`mac` must be a six-byte unicast address.

Optional `rom` and `sepROM` references use clean, forward-slash paths relative
to the bundle, such as `firmware/boot.bin`. Absolute paths, parent traversal,
backslashes, colons, and NUL bytes are rejected. `Config.Validate` checks
configuration syntax only; the `ios validate` command also checks files and
symlink containment as described above.
The optional `firmwareDigest` must contain a SHA-256 hexadecimal string; it is
stored metadata, not a check of firmware bytes. `bootArgs` records boot arguments,
but the native runner currently rejects a nonempty value because runtime NVRAM updates are not implemented.

Unknown schemas and invalid iOS configuration fail loading instead of falling
back to macOS, even when `hw.model` is present. Hardware edits preserve the iOS
object. Failed configuration updates and recipe edits leave the saved file
unchanged.

The iOS run-plan validator rejects generic installation media, Linux boot
options, Rosetta, Virtio clipboard, and shared folders. Recovery and DFU are
mutually exclusive. Passing validation does not establish host support or a
successful guest boot.
