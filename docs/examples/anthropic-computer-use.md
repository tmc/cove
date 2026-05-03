---
title: Anthropic Computer Use
---
# Anthropic Computer Use

Drive a local cove macOS VM as a Claude computer-use target. The bridge
is a single Python file that translates Anthropic Messages API
`tool_use` blocks into cove control-socket commands. There is no SDK
dependency and no abstraction layer -- this is the substrate the v0.4
adapter (design 022) will sit on top of.

## Prerequisites

- cove built and signed locally:
  ```bash
  go build -o cove .
  codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
  ```
- A long-lived macOS VM with the control socket exposed and the guest
  agent up.
- `ANTHROPIC_API_KEY` exported in the calling shell.
- `httpx` installed (`python -m pip install httpx`).

## Run

In one terminal, start a forked VM for the agent run:

```bash
cove -vm claude-eval run -gui
cove ctl -vm claude-eval agent-ping -wait 120s
```

In another terminal, hand the VM to the bridge:

```bash
python3 adapters/anthropic-bridge/computer_use.py \
    --vm claude-eval \
    --task "open Safari, navigate to apple.com, and tell me the headline"
```

The script runs at most five `messages.create` round-trips. Each
iteration sends the tool list and conversation, executes any returned
`tool_use` blocks against the VM's control socket, and feeds the
results back. The loop stops on `end_turn` or when iteration cap is
hit.

## How it works

1. The helper opens `~/.vz/vms/<vm>/control.sock` and reads
   `control.token` from the same directory.
2. On each iteration it POSTs to `https://api.anthropic.com/v1/messages`
   with the `anthropic-beta: computer-use-2025-11-24` header and a
   `computer_20250124` tool entry sized to the VM's last reported
   framebuffer.
3. For every `tool_use` block in the response, it dispatches the
   appropriate control-socket command -- `screenshot` returns a base64
   PNG that becomes a `tool_result` image block; `left_click`,
   `right_click`, `type`, and `key` translate directly to `mouse`,
   `text`, and `key` JSON requests on the socket.
4. The reply, plus all tool results, becomes the next `user` turn.

## Limits

- Not the v0.4 SDK adapter. There is no `AnthropicSandbox`,
  `SandboxRunConfig` backend, or session lifecycle. Use it to evaluate
  whether the substrate fits, then graduate to design 022 when that
  ships.
- One-shot only. The loop is bounded by `--max-iters` and exits on
  `end_turn`. There is no resumable session.
- Coordinate translation is naive: the model is told the screen size
  reported by the most recent screenshot, and clicks are sent in those
  same pixels. If your VM display is rescaled or the model reasons in a
  different coordinate space, you must rescale before calling `click`.
- `cursor_position` is unsupported (cove's control socket does not
  expose the live cursor) and the helper returns `(0, 0)`.

## See also

- [OpenAI Agents SDK](openai-agents.md) -- sibling adapter for OpenAI's
  Agents SDK `ComputerTool` and `SandboxRunConfig`.
- [Tailscale Mesh VM](tailscale-mesh.md) -- if you want to drive the
  same VM from a different machine over a tailnet, install Tailscale at
  first boot and point the bridge at the remote socket via SSH
  forwarding.
