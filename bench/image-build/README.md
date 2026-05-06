# image-build benchmark

Measures `cove build` cache behavior:

- cache miss from a clean build key;
- cache hit for the same vzscript;
- partial hit when one layer changes.

The result summary must report the cove commit, script path, cache key, and
per-step wall time. If no disposable base VM is supplied, the script records
`not measured` rather than mutating an arbitrary VM.
