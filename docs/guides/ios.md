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
this runner.

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
backslashes, colons, and NUL bytes are rejected. Configuration validation does
not open these files, resolve symbolic links, or establish firmware provenance.
The optional `firmwareDigest` must contain a SHA-256 hexadecimal string; it is
stored metadata, not a check of firmware bytes. `bootArgs` records boot arguments, but the native runner currently rejects a
nonempty value because runtime NVRAM updates are not implemented.

Unknown schemas and invalid iOS configuration fail loading instead of falling
back to macOS, even when `hw.model` is present. Hardware edits preserve the iOS
object. Failed configuration updates and recipe edits leave the saved file
unchanged.

The iOS run-plan validator rejects generic installation media, Linux boot
options, Rosetta, Virtio clipboard, and shared folders. Recovery and DFU are
mutually exclusive. Passing validation does not establish host support or a
successful guest boot.
