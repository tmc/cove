# Image provenance and freshness

`cove image build` records provenance in `manifest.json` for each local image:

- `cove_commit`
- `agent_commit`
- `agent_features`
- `build_recipe`
- `source_image`
- `source_manifest_digest`
- `built_at`
- `default_network`
- `default_sandbox`

When the source VM came from `cove pull`, Tart pull, or Lume pull,
`disk.provenance` is copied into `source_manifest_digest`. Image-backed forks
write that digest back to the child VM's `disk.provenance`, so a later
`cove image build` keeps the registry manifest chain. `cove store gc` also
treats local image manifests with `source_manifest_digest` as roots for the
local OCI content store.

`cove image verify <ref>` checks that:

- the manifest parses
- required image files are present
- the disk size matches the manifest
- the image advertises `execattach.v3`
- the image was built by the current `cove` binary or a compatible version
- the optional source registry manifest digest is well-formed when present

Output is `PASS`, `WARN`, or `FAIL`. Use `--strict` to turn a missing
`execattach.v3` feature into `FAIL`.

`cove run -fork-from <ref>` calls `image verify` before forking. It
refuses a `FAIL` result unless `COVE_ALLOW_STALE_IMAGE=1` is set.
`WARN` results print a warning and continue.

`cove action prepare-image <ref>` also runs `cove image verify --strict --json`
before it trusts the freshness shortcut. A recently built image that lacks
`execattach.v3`, has a corrupt layout, or fails a strict provenance check is not
reported as prepared.

## Comparing images

Use `cove diff <ref-a> <ref-b> [-json]` to compare two local image disk layer
metadata sets:

```sh
cove diff agentkit/linux-base:old agentkit/linux-base:new
cove diff agentkit/linux-base:old agentkit/linux-base:new -json
```

The diff reads each image manifest at runtime to choose the expected disk
layer name for the image OS. Disk layers are marked `[CHANGED]`,
`[UNCHANGED]`, `[ADDED]`, or `[REMOVED]`.

Use trailing `-json` for structured output containing the compared refs,
per-file status, old and new values, and whether any layer changed.
