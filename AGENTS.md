# Agent Instructions

Project conventions for AI coding agents working in this repo.

## Build & Sign

After every Go build, re-sign the binary with the virtualization entitlement:

```bash
go build -o cove ./cmd/cove
codesign -s - -f --entitlements cmd/cove/vz.entitlements ./cove
```

Without re-signing, the binary will fail with sandbox/entitlement errors at runtime.

## Style

Russ Cox style: small interfaces, stdlib-first, no panics in APIs, no
backwards-compat hacks, lowercase error messages wrapped with `fmt.Errorf`.
Default to no comments — code should be self-explanatory; comments only when
the *why* is non-obvious.

## Testing

- Table-driven tests with `[]struct{name, input, want}`.
- Example tests with `// Output:` comments for exported APIs.
- Script-based integration tests via `rsc.io/script` for CLI flows.

- In-process `rsc.io/script` adapters retain the test process working directory.
  Pass fixture paths with `State.Setenv` and expand `$VAR` directly. The script
  parser supports single quotes; shell-style double quotes become literal path
  characters. Keep test-created files under `t.TempDir`.

## Landing Work

1. Run quality gates: `go build ./...`, `go test ./...`.
2. Atomic commits — stage related changes together, never `git add -A`.
3. `git push` before handing off. Work is not complete until it lands on the remote.

## Commit helper

If `git-auto-commit-message` fails with a retired Claude model, set
`CGPT_MODEL=claude-haiku-4-5` for the helper. Verify HEAD changed: the helper can
return success even when model generation fails.
