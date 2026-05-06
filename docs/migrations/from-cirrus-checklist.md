# Cirrus migration preflight checklist

Use this checklist before changing a `.cirrus.yml` task. The goal is to
preserve the important inputs while there is still time to run the old job and
the new cove job side by side.

1. **Inventory every Cirrus config.** Find `.cirrus.yml` and `.cirrus.yaml` in
   the repository and note each task name.
2. **Classify the task shape.** Mark each task as container, macOS task,
   persistent_worker, matrix, cron, or custom executor.
3. **Record image references.** Save every container image, Tart image, and
   `macos_instance.image` value. Decide which ones become cove parent images.
4. **Export secrets metadata.** List secret names, owners, and target jobs.
   Do not copy secret values into docs or run artifacts.
5. **Capture schedules and triggers.** Translate Cirrus cron and branch filters
   into GitHub Actions `schedule`, `push`, `pull_request`, or manual dispatch.
6. **Map artifacts.** List logs, test reports, result bundles, and release
   outputs. Decide which run artifacts must be uploaded from `~/.vz/runs/`.
7. **Map caches.** Separate safe dependency caches from credential-bearing
   state. Use cove whole-VM `cache-key` only for state that can remain on the
   trusted runner host.
8. **Prepare runner images.** Build one cove image per job class, then gate it
   with `cove image verify --strict --newer-than 168h <ref>` and
   `cove action prepare-image <ref> --ttl 24h`.
9. **Run an A/B cutover.** Run the old Cirrus task and the cove workflow on the
   same commit. Compare exit code, test summary, artifacts, and
   `metrics.jsonl`.
10. **Write the rollback note.** Keep the old `.cirrus.yml`, the cove workflow,
    image refs, and operator commands in the migration ticket until the new
    workflow has passed the agreed soak period.

## Host commands

Run these from the repository root on the trusted cove host:

```bash
find . \( -name .cirrus.yml -o -name .cirrus.yaml \) -print
cove action doctor
cove image list
```

For each selected image:

```bash
cove image verify --strict --newer-than 168h <image-ref>
cove action prepare-image <image-ref> --ttl 24h
```

The companion VZScript can inspect a repository mounted into a guest and print a
small migration plan:

```bash
CIRRUS_MIGRATE_ROOT="/Volumes/My Shared Files/<repo-dir>" \
  cove vzscript run cirrus-migrate-doctor
```

Because the script runs inside the guest, it only sees host paths that cove has
mounted into the VM. For a precise host-side registry audit, keep using
`cove image list`, `cove image verify`, and the registry commands from the host.

## Known gaps to plan around

- The private GitHub Action is not a public Marketplace action.
- The Slice 1 `secrets` input is reserved and fails when non-empty.
- Guest artifact copy-out is not yet a first-class action input.
- cove runs on Apple Silicon hosts; it is not a cross-platform hosted CI
  service.
- Soft reset probes exist for research, but CI jobs should use disposable image
  forks.
