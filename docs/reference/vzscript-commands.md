---
title: VZScript Commands
description: Complete reference for all commands and conditions available in vzscript recipes.
icon: book
---
# VZScript Commands

Complete reference for all commands and conditions available in vzscript recipes.

Run templated scripts with `vzscript run -template` and repeated
`-var name=value` flags. Templates are rendered with Go `text/template` before
the script metadata and command body are parsed.

## Guest Commands

Commands that interact with the guest VM via the agent over vsock.

### guest-wait

Wait for VM boot and guest agent connectivity.

```text
guest-wait [timeout]
```

Default timeout: 10 minutes. Polls every 5 seconds.

```text
guest-wait 3m
```

### guest-ping

Check guest agent connectivity. Fails if agent is unreachable.

```text
guest-ping
```

### guest-exec

Run a command in the guest VM.

```text
guest-exec <command> [args...]
```

```text
guest-exec ls /tmp
guest-exec brew install golang
```

### guest-shell

Copy a local script to the guest and run it with bash. The script file is embedded in the txtar archive.

```text
guest-shell <script-file>
```

```text
guest-shell install.sh

-- install.sh --
#!/bin/bash
curl -fsSL https://example.com/setup.sh | bash
```

### guest-terminal

Requests a visible terminal window in the guest (macOS Terminal.app, or Linux
GNOME Terminal / GNOME Console / Konsole / xterm via the active graphical
session). On macOS, cove falls back to host-streamed output when Terminal
automation is not already allowed by TCC.

```text
guest-terminal <script-file>
```

### guest-write

Copy a small local file to the guest.

```text
guest-write <guest-path> <local-path>
```

```text
guest-write /tmp/config.yaml config.yaml

-- config.yaml --
key: value
```

### guest-read

Read a file from the guest to stdout.

```text
guest-read <guest-path>
```

```text
guest-read /etc/hosts
stdout 'localhost'
```

### guest-cp

Copy a file or directory from host to guest using streaming (for large files).

```text
guest-cp <host-path> <guest-path>
guest-cp -from-guest <guest-path> <host-path>
```

### host-cp

Copy a host file or directory to guest with a long timeout (30 minutes).

```text
host-cp <host-path> <guest-path>
```

### append-path

Add a directory to the system PATH via `/etc/paths.d/`.

```text
append-path /opt/homebrew/bin
```

## UI Automation Commands

Commands that drive the VM display via the control socket using screenshots and OCR.

### ocr-click

Find text on screen via OCR and click its center.

```text
ocr-click <text> [timeout] [region]
```

```text
ocr-click Continue
ocr-click "Agree" 30s
ocr-click "Install" 10s menu      # search only menu bar region
```

### ocr-wait

Wait until text appears on screen.

```text
ocr-wait <text> [timeout] [region]
```

```text
ocr-wait "Welcome" 120s
ocr-wait Desktop 60s
```

### ocr-gone

Wait until text disappears from screen.

```text
ocr-gone <text> [timeout] [region]
```

```text
ocr-gone "Installing" 300s
```

### ocr

Run OCR on the current screen. Stdout receives all recognized text.

```text
ocr
```

```text
ocr
stdout 'Continue'
```

### screenshot

Capture VM screen to a JPEG file.

```text
screenshot [file]
```

```text
screenshot /tmp/screen.jpg
```

### type

Type text into the VM character by character.

```go
type <text>
```

```go
type "hello world"
type <password>
```

### type-keycodes

Type text using per-key keycode events (for fields that don't accept character input).

```text
type-keycodes <text>
```

### key

Send a key event. Supports named keys and modifiers.

```text
key <spec>
```

Named keys: `return`, `tab`, `escape`, `space`, `delete`, `up`, `down`, `left`, `right`, `f1`-`f12`.

Modifiers: `cmd+`, `shift+`, `alt+`, `ctrl+`.

```text
key return
key tab
key cmd+v
key shift+tab
key cmd+shift+3
```

### click

Click at normalized coordinates (0-1 range).

```text
click <x> <y>
```

```text
click 0.5 0.5          # center of screen
click 0.1 0.95         # bottom-left area
```

### wait

Sleep for a duration.

```text
wait <duration>
```

```text
wait 2s
wait 500ms
wait 1m
```

### wait-prompt-clear

Wait until a prompt text clears or progresses.

```text
wait-prompt-clear <text> [timeout]
```

```text
wait-prompt-clear "Password" 30s
```

### label-push

Push a label onto the script label stack. The current stack is logged and, for
headed VMs, appended to the VM window title.

```text
label-push <text>
```

```text
label-push "SIP disable"
label-push "Recovery Terminal"
```

### label-pop

Pop the current script label and update the log and window title.

```text
label-pop
```

### label-clear

Clear all script labels and remove the script suffix from the window title.

```text
label-clear
```

### answer-visible

Wait for the first visible prompt from a set of alternatives, type its answer
with keycode events, press Return, and wait for the prompt to clear or progress.

```text
answer-visible [-optional] [-skip-empty] [-timeout duration] [-delay duration] [-progress text] <prompt> <answer>...
```

```text
answer-visible -timeout 30s -progress "Password" "[y/n]" y "Are you sure" y
answer-visible -timeout 30s -delay 500ms "[y/n]" y
answer-visible -optional -timeout 5s "Authorized user" admin "user name" admin
answer-visible -optional -skip-empty "Password" $SIP_PASSWORD
```

### detect-page

Detect the current Setup Assistant page via OCR. Returns the page name.

```text
detect-page
```

### detect-screen

Detect the screen state. Returns one of: `black`, `apple-logo`, `setup-assistant`, `login`, `desktop`, `unknown`.

```text
detect-screen
```

### wait-menu-text

Wait for text to appear in the menu bar.

```text
wait-menu-text <text>
```

### click-menu-item

Click a menu bar title, then click a menu item.

```text
click-menu-item <menu> <item>
```

```text
click-menu-item Utilities Terminal
```

### recovery-options

Select Options in the Recovery startup picker. Use this after starting the VM
with `cove run -recovery`; the start option boots to the picker, and this
command advances into Recovery.

```text
recovery-options
```

`startup-options` is kept as an alias for older scripts.

### reboot-to-recovery

Stop the running VM and start macOS Recovery using Virtualization's Recovery
start option. After this command, use `recovery-options` to advance from the
startup picker into Recovery.

```text
reboot-to-recovery
recovery-options
```

### recovery-continue

Continue from Recovery setup screens, such as the language or continue prompt.

```text
recovery-continue
```

## Conditions

Conditions control whether a command line executes. Prefix with `[condition]`.

### screen

True if the current screen state matches.

```text
[screen:desktop] guest-exec open /Applications/Safari.app
[screen:login] type <password>
[screen:setup-assistant] ocr-click Continue
```

States: `black`, `apple-logo`, `setup-assistant`, `login`, `desktop`, `unknown`.

### page

True if the current Setup Assistant page matches.

```text
[page:language] ocr-click English
[page:country] ocr-click "United States"
```

### text-visible

True if text is currently visible on screen.

```text
[text-visible:Continue] ocr-click Continue
[text-visible:Not+Now] ocr-click "Not Now"
```

Space and punctuation use URL encoding: `+` for space, `%5B` for `[`, `%5D` for `]`.

```text
[text-visible:%5By%2Fn%5D] type y
```

## Standard Commands

These are inherited from rsc.io/script:

| Command | Description |
|---------|-------------|
| `cat` | Print file contents |
| `cp` | Copy files |
| `echo` | Print text |
| `env` | Print or set environment |
| `exists` | Check file existence |
| `help` | Print help |
| `mkdir` | Create directories |
| `sleep` | Sleep |
| `stderr` | Match stderr |
| `stdout` | Match stdout |
| `stop` | Stop script |

## Prefixes

| Prefix | Meaning |
|--------|---------|
| `!` | Expect command to fail |
| `?` | Don't care if command fails |

## Script Header Directives

| Directive | Description |
|-----------|-------------|
| `# requires: recipe1, recipe2` | Declare dependencies |
| `# runs-on: daemon` | Run via root daemon agent |
| `# mount: ~/path [ro\|rw]` | Mount host directory via VirtioFS |
