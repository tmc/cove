# anthropic-bridge

Substrate-only Python bridge that drives a running cove macOS VM as a
Claude computer-use target via the Anthropic Messages API.

This is **not** the v0.4 SDK adapter (design 022 scope). It is a single
~200-LOC helper that demonstrates that cove's existing control-socket
primitives (`screenshot`, `mouse`, `text`, `key`) are sufficient to back
Claude computer-use without any cove changes.

## Requirements

- Python 3.11+
- `httpx` (preferred) or `requests`
- `ANTHROPIC_API_KEY` environment variable
- A cove VM that is already running with its control socket exposed

## Quickstart

```bash
cove -vm macos-eval run -gui &
python -m pip install httpx
ANTHROPIC_API_KEY=sk-... python3 adapters/anthropic-bridge/computer_use.py \
    --vm macos-eval --task "open Safari and report the page title"
```

## Limitations

- One-shot loop, not interactive. Stdin into the guest is not exposed by
  this helper.
- `cursor_position` returns `(0, 0)`. The control socket does not expose
  the live cursor today.
- Coordinate translation is the helper's responsibility. The model's
  declared `display_width_px`/`display_height_px` come from the most
  recent screenshot's reported size; if the VM is rescaled or the model
  reasons in a different coordinate space, the helper does not rescale
  click coordinates for you.
- Key-name to keycode mapping in `_execute_tool` is intentionally
  minimal. A production adapter needs a complete xdotool-style table.

See [docs/examples/anthropic-computer-use.md](../../docs/examples/anthropic-computer-use.md)
for the cookbook walkthrough.
