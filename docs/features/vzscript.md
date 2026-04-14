---
title: VZScript Engine
---
# VZScript Engine

Declarative recipes for guest VM configuration. Built on [rsc.io/script](https://pkg.go.dev/rsc.io/script) with guest-agent and OCR commands.

## Usage

```bash
cove vzscript list                        # list built-in recipes
cove vzscript show homebrew               # print recipe contents
cove vzscript run homebrew                # run a recipe
cove vzscript run homebrew golang         # run multiple (deps resolved)
cove vzscript run ./custom.vzscript       # run a custom script file
```

## Run Options

```bash
cove vzscript run -v homebrew             # verbose output
cove vzscript run -timeout 30m golang     # custom timeout
cove vzscript run -terminal homebrew      # run in Terminal.app (visible in VM)
cove vzscript run -auto-approve golang    # auto-click Allow/OK dialogs via OCR
```

## Script Format

Scripts are [txtar](https://pkg.go.dev/golang.org/x/tools/txtar) archives. The comment section contains commands; embedded files are extracted to a working directory.

```
# Wait for the guest agent
guest-wait 3m

# Install Homebrew
guest-shell install.sh

-- install.sh --
#!/bin/bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

## Dependencies

Scripts declare dependencies with a header directive:

```
# requires: homebrew
```

Dependencies are resolved automatically. Each recipe runs at most once, even when specified by multiple dependents.

## Host Mounts

Scripts can declare host directories to mount via VirtioFS:

```
# mount: ~/projects rw
# mount: /data ro
```

Paths support `~/` expansion. Mounts are registered as shared folders, hot-plugged into the running VM, and mounted in the guest automatically.

## Run Mode

Scripts that need root (e.g., installing packages, writing to `/etc`) should declare:

```
# runs-on: daemon
```

This routes commands through the root daemon agent (port 1024) instead of the user agent.

## Built-in Recipes

Recipes are embedded in the cove binary. Use `cove vzscript list` to see all available recipes.

Common recipes: `homebrew`, `golang`, `developer-tools`, `claude-code`, `openclaw`, `rosetta`, `ssh-server`, `workstation`.

## Full Command Reference

See [VZScript Commands](../reference/vzscript-commands.md) for the complete list of guest, UI automation, and standard commands.
