---
title: Documentation map
description: Every cove documentation page, grouped by what you are trying to do.
icon: list
---
# Summary

## Start here
* [What cove is](README.md)
* [Install cove](getting-started/install.md)
* [Quick start](getting-started/quickstart.md)
* [Tutorial: provision and snapshot your first VM](getting-started/first-vm.md)
* [Fix a VM that will not start](guides/troubleshooting.md)

## Understand how cove works
* [How cove works](architecture/overview.md)
* [How the purego bindings work](architecture/purego.md)
* [Frequently asked questions](getting-started/faq.md)

## Run and manage VMs
* [Suspend and resume a VM](features/suspend-resume.md)
* [Snapshot and roll back a VM](features/snapshots.md)
* [Run Linux VMs](features/linux.md)
* [Run Windows VMs](guides/windows.md)
* [Configure networking](features/networking.md)
* [Configure the GUI and displays](features/gui-display.md)
* [Share folders with the guest](features/shared-folders.md)
* [Export a host directory over 9p](features/9p.md)
* [Push and pull VM images](getting-started/push-pull.md)
* [Track image provenance and freshness](features/image-provenance.md)
* [Read run metrics](features/metrics.md)
* [Work with the runs UX](features/runs-ux.md)
* [Probe soft reset](features/softreset-probes.md)

## Provision and automate a guest
* [Provision a guest](guides/provisioning.md)
* [Write and run vzscripts](features/vzscript.md)
* [Talk to the guest agent](features/guest-agent.md)
* [Manage SIP](features/sip.md)
* [Recover a VM and manage SIP](guides/recovery-sip.md)
* [Fix auto-login that is not firing](recovery/auto-login-not-firing.md)
* [Recover from a partial Homebrew install](recovery/homebrew-partial-cleanup.md)
* [Boot a NixOS guest](quickstart/nixos.md)

## Connect agents, CI, and other tools
* [Start an agent sandbox](agent-sandbox/quickstart.md)
* [Use the agent sandbox cookbook](agent-sandbox/cookbook.md)
* [Choose an agent sandbox provider](agent-sandbox/provider-matrix.md)
* [Use the agent sandbox CLI](features/agent-sandbox-cli.md)
* [Serve cove over MCP](features/mcp.md)
* [Run a Node.js MCP client](examples/nodejs-mcp-client.md)
* [Run GitHub Actions jobs](features/gha-executor.md)
* [Run hosted runners](examples/hosted-runners.md)
* [Run a macOS CI runner](examples/ci-runner.md)
* [Build a reproducible dev environment](examples/dev-environment.md)
* [Build a security research sandbox](examples/security-sandbox.md)
* [Join a Tailscale mesh](examples/tailscale-mesh.md)
* [Run a Linux GUI desktop](examples/linux-gui-desktop.md)
* [Drive Anthropic computer use](examples/anthropic-computer-use.md)
* [Drive OpenAI Agents SDK](examples/openai-agents.md)
* [Drive Gemini computer use](examples/gemini-computer-use.md)
* [Drive Vertex AI computer use](examples/vertex-ai-computer-use.md)
* [Integrate the OpenAI Agents SDK](integrations/openai-agents.md)
* [Check the integration matrix](integrations/matrix.md)
* [Browse all examples](examples/README.md)
* [Run a fleet](quickstart/fleet.md)

## Operate and release cove
* [Run the release pipeline](release-pipeline.md)
* [Work through the release checklist](reference/release-checklist.md)
* [Smoke-test the runtime listener](runbooks/runtime-listener-smoke.md)
* [Build agentkit images](runbooks/agentkit-images.md)
* [Pass through a block device](runbooks/block-device-passthrough.md)
* [Audit the test HOME](reliability/test-home-audit.md)
* [Review concurrency findings](reliability/concurrency-2026-05.md)

## Reference
* [CLI reference](reference/cli.md)
* [Control socket API](reference/control-api.md)
* [HTTP API](reference/http-api.md)
* [Agent commands](reference/agent-commands.md)
* [vzscript commands](reference/vzscript-commands.md)
* [Shared folders](reference/shared-folders.md)
* [cove forward](reference/forward.md)
* [Fleet control plane](reference/fleet-control-plane.md)
* [Helper SIGKILL log](reference/sigkill-log.md)
* [Run metrics schema](observability/runs-schema.md)
* [Fleet image transfer](fleet/image-transfer.md)
* [coved observability](coved/observability.md)
* [Changelog](reference/changelog.md)
* [Release notes v0.3.1](RELEASE-NOTES-v0.3.1.md)

## Project records

Dated engineering records, kept for provenance. They describe the project at the
date each was written and are not maintained as current documentation. For how
cove behaves today, use the groups above.

Design docs, bug investigations, and research notes are kept on disk under
`docs/designs/` and are not tracked in git or published here.

### Release readiness records
* [Release post-tag checklist](release/post-tag-checklist.md)
* [Tag cut runbook](release/tag-cut-runbook.md)

### Benchmarks
* [cove shell roundtrip latency](benchmarks/cove-shell-latency.md)
* [Disk I/O Benchmark](benchmarks/disk-io.md)
* [R53 perf snapshot](benchmarks/r53-perf-snapshot.md)
