---
title: VZScript Engine
description: Declarative recipes for guest VM configuration.
icon: puzzle-piece
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
cove vzscript run -terminal homebrew      # stream guest-shell output here
cove vzscript run -terminal-gui homebrew  # explicit guest terminal window
cove vzscript run -auto-approve golang    # auto-click Allow/OK dialogs via OCR
cove vzscript run -template -var Mode=disable ./sip.vzscript.tmpl
```

`-terminal` streams guest-shell output to the host terminal. This avoids macOS
Apple Events prompts from Terminal.app on fresh guests. Use `-terminal-gui`
only when you explicitly need a visible terminal window in the guest.

## Script Format

Scripts are [txtar](https://pkg.go.dev/golang.org/x/tools/txtar) archives. The comment section contains commands; embedded files are extracted to a working directory.

```sh
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

```sh
# requires: homebrew
```

Dependencies are resolved automatically. Each recipe runs at most once, even when specified by multiple dependents.

## Guest OS

Recipes declare the guest OS they support:

```sh
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

```sh
# mount: ~/projects rw
# mount: /data ro
```

Paths support `~/` expansion. Mounts are registered as shared folders and apply
when the VM boots with the corresponding VirtioFS device. During `cove vzscript
run`, cove also attempts best-effort hot-plug and guest mounting for an already
running VM. If that live step fails, the run prints a warning and the saved
mount still applies on a future cold boot.

## Run Mode

Scripts that need root (e.g., installing packages, writing to `/etc`) should declare:

```sh
# runs-on: daemon
```

This routes commands through the root daemon agent (port 1024) instead of the user agent.

Recipes can also request terminal output:

```sh
# runs-on: terminal
```

This streams output to the host terminal. `# runs-on: terminal-gui` explicitly
requests a guest Terminal window and falls back to host-streamed output if
Terminal automation is not already allowed by macOS TCC.

## Templates

`vzscript run -template` renders recipes as Go `text/template` files before
metadata parsing and execution. Pass values with repeated `-var name=value`
flags. The renderer provides `quote`, `queryescape`, and `env` functions.

```text
label-push {{quote (printf "SIP %s" .Mode)}}
type-keycodes {{quote .Command}}
[text-visible:{{queryescape .SuccessText}}] screenshot
```

```sh
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

## Static planning

Inspect recipes before choosing or starting a VM:

```sh
cove vzscript plan -os darwin golang
cove vzscript validate ./custom.vzscript
cove vzscript explain -json workstation
```

These commands read recipe sources and dependencies without invoking host or
guest commands, evaluating live conditions, extracting archive files, changing
shares, or looking up a running VM. `plan` reports dependency order, inclusion
reasons, declared guest OS and execution route, mounts, injections, and unresolved
checks. `explain` also reports registered commands and UI requirements. `validate`
returns an error for static diagnostics; JSON diagnostics include the source,
line, class, and suggested next action.

Syntax inspection uses the same script engine and command registration as
execution, with inert adapters. It checks quoted arguments, conditions, expected
failure prefixes, command names, and archive filenames. Command argument
semantics, condition outcomes, guest readiness, tools, and filesystem contents
remain runtime checks. A guarded command is checked even when its condition
would be false at runtime. No command arguments or environment values appear in
the plan.

Templates remain unresolved and produce a diagnostic. Render them with explicit
inputs before validating the rendered recipe. Static planning does not read the
host environment or infer missing template parameters. Existing execution
metadata rules are preserved: mount and injection headers use unquoted paths;
spaces in these paths require changing the recipe rather than guessing a new
metadata grammar. Relative mount paths are shown as declared and remain
unresolved until an execution workspace is chosen.
