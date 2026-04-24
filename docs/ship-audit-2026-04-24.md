# cove 0.1 Ship-Gate Audit — 2026-04-24

Auditor: Claude (Opus 4.7, 1M-ctx). Source of truth:
`docs/designs/011-beat-lume-roadmap.md` §114–124.

Environment: VM `hermes-mlx-go-60g-v10` running at
`~/.vz/hermes-mlx-go-60g-v10/control.sock`; `./cove` binary built at
commit `0e2952c` (dump-docs reports version `4599ce29`).

Full `go test ./...` run: all 13 packages pass, 0 failures (log:
`/private/tmp/.../tasks/b2xt9ak49.output`).

## Findings

| Item | Status | Evidence | Severity |
|---|---|---|---|
| 1. `cove up -user <name>` reaches desktop without SA handholding | PASS (code-inspection only) | `up.go:217` forces `provisionStrategy = "disk"`; `up.go:275-276` hardcodes `SkipSetupAssistant:true, AutoLogin:true`; pipeline install→provision→run is linear with no TODO/FIXME across `up.go`, `provision_automation.go`, `setup_assistant.go` (2 kLOC combined). Disk-inject path writes `.AppleSetupDone` + LaunchDaemon + kcpassword + loginwindow.plist. `dump-docs` confirms `-user`, `-password`, `-vzscripts`, `-ipsw`, `-headless`, `-force` flags exist. Not exercised end-to-end (no fresh install ran). | major if broken on fresh install; minor otherwise |
| 2. `cove run -headless` does not create a GUI window | PASS | Headless path calls `newHeadlessGUIController` (`gui_control.go:114`) which invokes `initDetachedView` (line 166): constructs a `VZVirtualMachineView` but does NOT call `NewWindowWithContentRectStyleMaskBackingDefer`. `initWindow` (line 180) is only reached from the `gui-open` control-socket handler (`gui_control.go:333,430`). No NSWindow is created at boot in headless mode. | — |
| 3. `cove serve --mcp` operates a pre-existing VM end-to-end | PASS | `dump-docs -type mcp` lists 19 MCP tools: `vm_list`, `vm_status`, `vm_pause`, `vm_resume`, `vm_stop`, `vm_request_stop`, `vm_screenshot`, `vm_type`, `vm_key`, `vm_mouse`, `vm_agent_exec`, `vm_agent_read`, `vm_agent_write`, `vm_snapshot_save`, `vm_snapshot_list`, `vm_snapshot_restore`, `vm_snapshot_delete`, `vm_disk_snapshot_list`, `vm_pit_snapshot_list`. Bindings in `control_mcp.go:464-772` proxy to real `ControlRequest` protos, not stubs. Create-VM is a CLI-only path for 0.1 (resolved by product decision 2026-04-24). | — |
| 4. `cove serve -http` operates a pre-existing VM end-to-end | PASS (parity with MCP) | `dump-docs -type api` lists 26 REST endpoints under `/v1/vms/{name}/*`: status, pause, resume, stop, request-stop, screenshot, type, key, mouse, agent/exec, agent/read, agent/write, agent/cp, snapshot, snapshots (list/restore/delete), disk-snapshots, pit-snapshots, events (SSE), plus `/v1/operations/*` (list+SSE). Route handlers in `serve_gateway.go:596-628`. `POST /v1/vms` exists but returns `not_implemented` — by design for 0.1 (CLI-only create; resolved by product decision 2026-04-24). | — |
| 5. Agents can invoke snapshots and pause/resume over HTTP/MCP | PASS | Pause + resume + snapshot save/list/restore/delete all wired on both surfaces. Live-probed on hermes VM: `ctl pause` → status `canResume:true, canStop:true`; `ctl resume` → back to running. Roadmap "suspend/resume" clarified 2026-04-24 to mean pause/resume (resolved by product decision). | — |
| 6. `cove pull` of a lume-produced image boots in cove | PASS (code path exists; not booted E2E) | `cove pull` subcommand dispatched at `main.go:463`; `pull.go` is 471 lines with real OCI manifest fetch, LZ4 chunk streaming, atomic disk rename. `pull.go:323` calls `ociimage.NormalizeLayerAnnotations` which `internal/ociimage/annotations.go:22-49` defines a bidirectional `coveToLume`/`lumeToCove` map covering all legacy `org.trycua.lume.*` annotation keys. Pull accepts lume-produced manifests. Live boot of a lume image was not exercised. | major if lume schema diverges from the mapped keys; minor otherwise |
| 7. `cove dump-docs` emits structured CLI/API/MCP data | PASS | `./cove dump-docs` emits a single JSON document with top-level keys `version`, `cli`, `api`, `mcp`. CLI section has 26 commands with name/summary/usage/flags/examples. API section has 26 endpoints with method/path/description/auth. MCP section has 19 tools with name/description/JSONSchema input_schema. `--help` shows `-type cli|api|mcp` and `-pretty` flags. Output is valid JSON parseable without help-text scraping. | — |

### Incidental findings

- **`-verbose` ObjC main-thread crash:** not reproduced in this audit; previous
  report attributed it to `setMainMenu:` firing off-main. `setMainMenu:`
  is called at `mainmenu.go:91` and `mainmenu.go:152`. Main-thread
  discipline is enforced via `runOnUIThreadSync` / `drainUIThreadTasks`
  (`ui_thread.go`), so any call path that reaches `setMainMenu` from a
  goroutine would trigger the same "API misuse" assertion. Most likely a
  verbose-gated log path that builds menu items off-thread — needs
  repro + stack, not in scope here.
- **Import cycles / dead code:** `go test ./...` passed cleanly; no build
  errors. No cycles flagged.
- **Test suite:** 13 packages, all green; runtime ~8s.

## Blockers to 0.1 ship

None at **blocker** severity. Both prior **major** flags resolved by product
decision on 2026-04-24:

1. `cove serve` — create-VM is CLI-only for 0.1; "operate a pre-existing VM"
   is the 0.1 contract. Roadmap bullet reworded accordingly. (#3, #4)
2. "suspend/resume" in the ship gate means pause/resume. Roadmap bullet
   reworded to drop "suspend." (#5)

The PASS items that weren't exercised end-to-end (#1, #6) remain candidates
for smoke passes before tagging:

- Fresh `cove up -user smoketest` to a booted macOS desktop.
- `cove pull` against a known lume-produced image.
