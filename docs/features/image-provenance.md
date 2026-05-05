# Image provenance and freshness

`cove image build` records provenance in `manifest.json` for each local image:

- `cove_commit`
- `agent_commit`
- `agent_features`
- `build_recipe`
- `source_image`
- `built_at`
- `default_network`
- `default_sandbox`

`cove image verify <ref>` checks that:

- the manifest parses
- required image files are present
- the disk size matches the manifest
- the image advertises `execattach.v3`
- the image was built by the current `cove` binary or a compatible version

Output is `PASS`, `WARN`, or `FAIL`. Use `--strict` to turn a missing
`execattach.v3` feature into `FAIL`.

`cove run -fork-from <ref>` calls `image verify` before forking. It
refuses a `FAIL` result unless `COVE_ALLOW_STALE_IMAGE=1` is set.
`WARN` results print a warning and continue.

## Comparing images

Use `cove image inspect --diff <ref-a> <ref-b>` to compare two local image
manifests and image layers:

```sh
cove image inspect --diff agentkit/linux-base:old agentkit/linux-base:new
```

The diff reads manifest fields at runtime, so older manifests and newer
provenance fields can be compared without a schema update. Fields and layers
are marked `[CHANGED]`, `[UNCHANGED]`, `[ADDED]`, or `[REMOVED]`.

Use `-json` with `--diff` for structured output containing `added`,
`removed`, `changed`, and `unchanged` objects with old and new values.
