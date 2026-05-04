# parity-smoke

`parity_smoke.py` runs the same browser screenshot task through each
computer-use adapter:

- `openai`: `adapters/openai-agents-python` (`cove_sandbox`)
- `anthropic`: `adapters/anthropic-sandbox-python` (`cove_claude_sandbox`)
- `gemini`: `adapters/google-bridge`
- `vertex`: `adapters/google-bridge/vertex-ai`

The harness forks a stopped parent VM, boots the fork with GUI enabled, waits
for the guest agent, opens Safari to the target URL, takes a screenshot through
the selected adapter, and writes:

```
adapters/parity-smoke/results/<adapter>-<date>.json
```

The JSON records the parent, fork name, URL, wall-clock seconds, frame count,
screenshot dimensions, and screenshot SHA-256. It does not store screenshot
pixels.

## Live Acceptance

The initial live acceptance run was deferred on 2026-05-04 because the host's
macOS VM slots were occupied by the long-running `gha-runner-mlx-go-v1` and
`gha-runner-mlx-go-libs-2` workers. Stopping those workers is out of scope for
this harness. Run the smoke when a VM slot is free and commit the generated
`adapters/parity-smoke/results/<adapter>-<date>.json` if a recorded acceptance
artifact is needed.

## Run

Use a stopped macOS parent VM with the cove guest agent installed:

```sh
python3 adapters/parity-smoke/parity_smoke.py \
  --adapter=openai \
  --parent=cove-test
```

The default `--cove` is the repo-local `./cove` binary when present. Override it
with `--cove` or `COVE_BIN`.

Useful options:

```sh
python3 adapters/parity-smoke/parity_smoke.py \
  --adapter=anthropic \
  --parent=cove-test \
  --url=https://example.com \
  --name=parity-anthropic-manual \
  --keep-vm
```

For Vertex AI, the screenshot smoke does not call Vertex APIs, but the bridge
constructor still requires a project and region:

```sh
python3 adapters/parity-smoke/parity_smoke.py \
  --adapter=vertex \
  --parent=cove-test \
  --vertex-project=my-project \
  --vertex-region=us-central1
```

## Add An Adapter

Add one screenshot function and register it in `adapters()`:

```python
def new_adapter_screenshot(ctx: SmokeContext) -> bytes:
    client = NewAdapter(vm=ctx.vm, token=ctx.token, socket_path=ctx.socket_path)
    return client.screenshot()

def adapters(...) -> dict[str, Adapter]:
    return {
        ...
        "new-adapter": Adapter("new-adapter", new_adapter_screenshot),
    }
```

The function must return raw PNG bytes. The harness owns the fork, GUI boot,
browser open, timing, result JSON, and cleanup so every adapter runs the same
task.
