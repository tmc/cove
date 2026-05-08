---
title: cove integration matrix
status: Draft
date: 2026-05-05
---

# cove integration matrix

This page lists the operator-facing integrations that ship with cove v0.4 and
the main file or command surface for each one.

| Integration | Status | Design | Provider | Surface | Entry point |
|---|---|---|---|---|---|
| Anthropic computer-use | shipped v0.4 | 022 v2 | Go | `cove agent-sandbox run --provider anthropic` | [`docs/features/agent-sandbox-cli.md`](../features/agent-sandbox-cli.md), [`docs/examples/anthropic-computer-use.md`](../examples/anthropic-computer-use.md), [`adapters/anthropic-bridge/computer_use.py`](../../adapters/anthropic-bridge/computer_use.py) |
| OpenAI Agents SDK | shipped v0.4 | 035 | Python | `cove-sandbox` + `SandboxRunConfig` | [`docs/integrations/openai-agents.md`](openai-agents.md), [`docs/examples/openai-agents.md`](../examples/openai-agents.md), [`adapters/openai-agents-python/README.md`](../../adapters/openai-agents-python/README.md) |
| Gemini computer-use | shipped v0.3 | T-4 | Python | `cove agent-sandbox run --provider gemini` | [`docs/examples/gemini-computer-use.md`](../examples/gemini-computer-use.md), [`adapters/google-bridge/computer_use.py`](../../adapters/google-bridge/computer_use.py) |
| Vertex AI computer-use | shipped v0.3 | V0.2.2-#3 | Python | `cove agent-sandbox run --provider vertex` | [`docs/examples/vertex-ai-computer-use.md`](../examples/vertex-ai-computer-use.md), [`adapters/google-bridge/vertex-ai/computer_use.py`](../../adapters/google-bridge/vertex-ai/computer_use.py) |
| GitHub Actions | shipped v0.4 | 021 / 030 | Go | `.github/actions/cove-action` + `cmd/cove-action` | [`docs/features/gha-executor.md`](../features/gha-executor.md), [`.github/actions/cove-action/action.yml`](../../.github/actions/cove-action/action.yml), [`cmd/cove-action/main.go`](../../cmd/cove-action/main.go) |
| GitLab CI | partial | 021 | Shell / YAML | `vzscripts/gitlab-runner.vzscript` | [`docs/designs/archive/021-v04-ci-executors-tracks.md`](../designs/archive/021-v04-ci-executors-tracks.md), [`vzscripts/gitlab-runner.vzscript`](../../vzscripts/gitlab-runner.vzscript) |
| Tailscale mesh | shipped v0.3 | example | Shell / vzscript | `cove vzscript run tailscale` | [`docs/examples/tailscale-mesh.md`](../examples/tailscale-mesh.md), [`vzscripts/tailscale.vzscript`](../../vzscripts/tailscale.vzscript) |
| Cirrus migration | shipped v0.4 | docs | CI migration | `docs/landing/cove-vs-cirrus.md` | [`docs/landing/cove-vs-cirrus.md`](../landing/cove-vs-cirrus.md), [`docs/migrations/from-cirrus.md`](../migrations/from-cirrus.md), [`docs/migrations/from-cirrus-checklist.md`](../migrations/from-cirrus-checklist.md) |
| cove fleet | shipped v0.4 Slice 1 | 034 | Go | `cove fleet add/ls/rm` and `--fleet=<name>` | [`docs/quickstart/fleet.md`](../quickstart/fleet.md), [`docs/designs/034-fleet-slice-1.md`](../designs/034-fleet-slice-1.md) |
| NixOS guest | shipped v0.4 | 036 | Linux install | `cove install -nixos` | [`docs/quickstart/nixos.md`](../quickstart/nixos.md), [`vzscripts/nixos-base.vzscript`](../../vzscripts/nixos-base.vzscript) |
| Linux Desktop autoprovisioning | documented v0.4 | 037 | Linux Desktop install | `cove up -linux -desktop -user ... -password ...` | [`docs/designs/037-linux-autoprov.md`](../designs/037-linux-autoprov.md), [`docs/benchmarks/disk-io.md`](../benchmarks/disk-io.md) |

Notes:

- The matrix is intentionally about operator-facing integration surfaces, not
  every internal package in the tree.
- `partial` means the shape is documented and partially implemented, but not a
  finished public surface.
- `documented` means the contract is present in docs and related provisioning
  code, but first-boot behavior is still under active validation.
