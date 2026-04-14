---
title: VZScript Commands
---
# VZScript Commands

Complete reference for all commands and conditions available in vzscript recipes.

## Guest Commands

Commands that interact with the guest VM via the agent over vsock.

### guest-wait

Wait for VM boot and guest agent connectivity.

```
guest-wait [timeout]
```

Default timeout: 10 minutes. Polls every 5 seconds.

```
guest-wait 3m
```

### guest-ping

Check guest agent connectivity. Fails if agent is unreachable.

```
guest-ping
```

### guest-exec

Run a command in the guest VM.

```
guest-exec <command> [args...]
```

```
guest-exec ls /tmp
guest-exec brew install golang
```

### guest-shell

Copy a local script to the guest and run it with bash. The script file is embedded in the txtar archive.

```
guest-shell <script-file>
```

```
guest-shell install.sh

-- install.sh --
#!/bin/bash
curl -fsSL https://example.com/setup.sh | bash
```

### guest-terminal

Run a script in Terminal.app inside the guest (visible to the user). Same as `guest-shell` but opens Terminal.app.

```
guest-terminal <script-file>
```

### guest-write

Copy a small local file to the guest.

```
guest-write <guest-path> <local-path>
```

```
guest-write /tmp/config.yaml config.yaml

-- config.yaml --
key: value
```

### guest-read

Read a file from the guest to stdout.

```
guest-read <guest-path>
```

```
guest-read /etc/hosts
stdout 'localhost'
```

### guest-cp

Copy a file or directory from host to guest using streaming (for large files).

```
guest-cp <host-path> <guest-path>
guest-cp -from-guest <guest-path> <host-path>
```

### host-cp

Copy a host file or directory to guest with a long timeout (30 minutes).

```
host-cp <host-path> <guest-path>
```

### append-path

Add a directory to the system PATH via `/etc/paths.d/`.

```
append-path /opt/homebrew/bin
```

## UI Automation Commands

Commands that drive the VM display via the control socket using screenshots and OCR.

### ocr-click

Find text on screen via OCR and click its center.

```
ocr-click <text> [timeout] [region]
```

```
ocr-click Continue
ocr-click "Agree" 30s
ocr-click "Install" 10s menu      # search only menu bar region
```

### ocr-wait

Wait until text appears on screen.

```
ocr-wait <text> [timeout] [region]
```

```
ocr-wait "Welcome" 120s
ocr-wait Desktop 60s
```

### ocr-gone

Wait until text disappears from screen.

```
ocr-gone <text> [timeout] [region]
```

```
ocr-gone "Installing" 300s
```

### ocr

Run OCR on the current screen. Stdout receives all recognized text.

```
ocr
```

```
ocr
stdout 'Continue'
```

### screenshot

Capture VM screen to a JPEG file.

```
screenshot [file]
```

```
screenshot /tmp/screen.jpg
```

### type

Type text into the VM character by character.

```
type <text>
```

```
type "hello world"
type mypassword
```

### type-keycodes

Type text using per-key keycode events (for fields that don't accept character input).

```
type-keycodes <text>
```

### key

Send a key event. Supports named keys and modifiers.

```
key <spec>
```

Named keys: `return`, `tab`, `escape`, `space`, `delete`, `up`, `down`, `left`, `right`, `f1`-`f12`.

Modifiers: `cmd+`, `shift+`, `alt+`, `ctrl+`.

```
key return
key tab
key cmd+v
key shift+tab
key cmd+shift+3
```

### click

Click at normalized coordinates (0-1 range).

```
click <x> <y>
```

```
click 0.5 0.5          # center of screen
click 0.1 0.95         # bottom-left area
```

### wait

Sleep for a duration.

```
wait <duration>
```

```
wait 2s
wait 500ms
wait 1m
```

### wait-prompt-clear

Wait until a prompt text clears or progresses.

```
wait-prompt-clear <text> [timeout]
```

```
wait-prompt-clear "Password" 30s
```

### detect-page

Detect the current Setup Assistant page via OCR. Returns the page name.

```
detect-page
```

### detect-screen

Detect the screen state. Returns one of: `black`, `apple-logo`, `setup-assistant`, `login`, `desktop`, `unknown`.

```
detect-screen
```

### wait-menu-text

Wait for text to appear in the menu bar.

```
wait-menu-text <text>
```

### click-menu-item

Click a menu bar title, then click a menu item.

```
click-menu-item <menu> <item>
```

```
click-menu-item Utilities Terminal
```

### startup-options

Enter startup options (for recovery boot).

```
startup-options
```

### recovery-continue

Continue from recovery selection screen.

```
recovery-continue
```

## Conditions

Conditions control whether a command line executes. Prefix with `[condition]`.

### screen

True if the current screen state matches.

```
[screen:desktop] guest-exec open /Applications/Safari.app
[screen:login] type mypassword
[screen:setup-assistant] ocr-click Continue
```

States: `black`, `apple-logo`, `setup-assistant`, `login`, `desktop`, `unknown`.

### page

True if the current Setup Assistant page matches.

```
[page:language] ocr-click English
[page:country] ocr-click "United States"
```

### text-visible

True if text is currently visible on screen.

```
[text-visible:Continue] ocr-click Continue
[text-visible:Not+Now] ocr-click "Not Now"
```

Space and punctuation use URL encoding: `+` for space, `%5B` for `[`, `%5D` for `]`.

```
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
