---
title: VZScript Engine
---
# VZScript Engine

Declarative recipes for guest VM configuration. Built on [rsc.io/script](https://pkg.go.dev/rsc.io/script) with guest-agent and OCR commands.

## Usage

```bash
cove vzscript list                        # list built-in recipes
cove vzscript list -os linux              # list recipes for Linux guests
cove vzscript show homebrew               # print recipe contents
cove vzscript run homebrew                # run a recipe
cove vzscript run homebrew golang         # run multiple (deps resolved)
cove vzscript run ./custom.vzscript       # run a custom script file
```

## Run Options

```bash
cove vzscript run -v homebrew             # verbose output
cove vzscript run -timeout 30m golang     # custom timeout
cove vzscript run -terminal homebrew      # open a visible guest terminal window
cove vzscript run -auto-approve golang    # auto-click Allow/OK dialogs via OCR
cove vzscript run -template -var Mode=disable ./sip.vzscript.tmpl
```

`-terminal` opens a visible terminal window in the guest (macOS Terminal.app,
or Linux GNOME Terminal / GNOME Console / Konsole / xterm via the active
graphical session).

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

## Guest OS

Recipes declare the guest OS they support:

```
# guest-os: darwin
```

Valid values are `darwin`, `linux`, and `both`. Recipes without the directive
default to `darwin` for compatibility with older macOS-oriented recipes.

`cove vzscript list -os darwin` and `cove vzscript list -os linux` filter the
built-in recipe list. When `-vm` is set and `-os` is omitted, `vzscript list`
filters to that VM's configured guest OS. `vzscript run` refuses before running
commands when a recipe does not match the target VM's OS.

## Host Mounts

Scripts can declare host directories to mount via VirtioFS:

```
# mount: ~/projects rw
# mount: /data ro
```

Paths support `~/` expansion. Mounts are registered as shared folders and apply
when the VM boots with the corresponding VirtioFS device. Live hot-add is not
currently supported.

## Run Mode

Scripts that need root (e.g., installing packages, writing to `/etc`) should declare:

```
# runs-on: daemon
```

This routes commands through the root daemon agent (port 1024) instead of the user agent.

## Templates

`vzscript run -template` renders recipes as Go `text/template` files before
metadata parsing and execution. Pass values with repeated `-var name=value`
flags. The renderer provides `quote`, `queryescape`, and `env` functions.

```
label-push {{quote (printf "SIP %s" .Mode)}}
type-keycodes {{quote .Command}}
[text-visible:{{queryescape .SuccessText}}] screenshot
```

```
cove vzscript run -template \
  -var Mode=disable \
  -var Command="csrutil disable" \
  -var SuccessText="System Integrity Protection is off." \
  ./sip.vzscript.tmpl
```

## Built-in Recipes

Recipes are embedded in the cove binary. Use `cove vzscript list` to see all available recipes.

Common recipes: `homebrew`, `golang`, `developer-tools`, `claude-code`, `openclaw`, `rosetta`, `ssh-server`, `workstation`, `github-runner`.

## Full Command Reference

See [VZScript Commands](../reference/vzscript-commands.md) for the complete list of guest, UI automation, and standard commands.
