# Agentkit Images

Agentkit images are local curated cove images for common agent sandboxes. v1
uses the existing local image store and does not publish a registry or signed
channel.

## Layout

Agentkit image refs use the `agentkit/<variant>:<tag>` namespace:

```text
~/.vz/images/
  agentkit/
    linux-base/
      latest/
        manifest.json
        linux-disk.img
        config.json
        vmlinuz
        initrd
    linux-claude-ready/
      latest/
        manifest.json
        linux-disk.img
        config.json
        vmlinuz
        initrd
```

`cove image list` renders these as:

```text
NAME                         TAG
agentkit/linux-base          latest
agentkit/linux-claude-ready  latest
```

## Variants

`linux-base` is the minimal Ubuntu agent base:

```text
recipes: agentkit-linux-base
source:  stopped Ubuntu VM with vz-agent installed and verified
ref:     agentkit/linux-base:latest
```

`linux-claude-ready` adds the Claude Code CLI on top of the base agent image:

```text
recipes: agentkit-linux-base, agentkit-linux-claude-ready
source:  stopped Ubuntu VM with vz-agent installed, verified, and Claude Code installed
ref:     agentkit/linux-claude-ready:latest
```

The recipe sequence is intentionally just vzscript names. Build or update the
source VM by booting a Linux VM, running the sequence with `cove vzscript run`,
shutting it down cleanly, then snapshotting it with `cove image build`.

## Build

Start from Linux VMs that already have the desired recipe sequence baked in.
For recipes that mutate the source, boot the VM headless, run the recipes, shut
it down, then snapshot:

```sh
cove -vm linux-base-source run -linux -headless
cove vzscript run -vm linux-base-source agentkit-linux-base
cove ctl -vm linux-base-source agent-shutdown force
cove image build -from linux-base-source -tag agentkit/linux-base:latest

cove clone linux-base-source linux-claude-ready-source --linked
cove -vm linux-claude-ready-source run -linux -headless
cove vzscript run -vm linux-claude-ready-source agentkit-linux-base agentkit-linux-claude-ready
cove ctl -vm linux-claude-ready-source agent-shutdown force
cove image build -from linux-claude-ready-source -tag agentkit/linux-claude-ready:latest
```

If an image already exists, remove it after confirming no live forks reference
it:

```sh
cove image inspect agentkit/linux-claude-ready:latest
cove image rm agentkit/linux-claude-ready:latest
```

## Verify

List the curated images:

```sh
cove image list
```

Boot a fresh child from the Claude-ready image and wait for the agent:

```sh
cove run -fork-from agentkit/linux-claude-ready:latest -ephemeral -headless
```

In another shell:

```sh
cove ctl agent-ping
cove shell "$(readlink ~/.vz/current | xargs basename)" -- uname -a
```

The gate for v1 is that `cove image list` shows both curated refs and the
Claude-ready image boots to a working vz-agent.
