# vz-macos Implementation Notes

## Overview

This example demonstrates macOS VM installation and running using purego-based bindings for Apple's Virtualization framework. The implementation mirrors [Code-Hex/vz](https://github.com/Code-Hex/vz) patterns where possible.

## Key Technical Learnings

### 1. CFRunLoop Modes

**Critical**: `kCFRunLoopCommonModes` vs `kCFRunLoopDefaultMode`

- `kCFRunLoopDefaultMode` - Use for `CFRunLoopRunInMode()` calls
- `kCFRunLoopCommonModes` - Only valid for adding sources/observers, NOT for running the loop

```go
// CORRECT
CFRunLoopRunInMode(kCFRunLoopDefaultMode, seconds, returnAfterSourceHandled)

// WRONG - causes "invalid mode 'kCFRunLoopCommonModes'" error
CFRunLoopRunInMode(kCFRunLoopCommonModes, seconds, returnAfterSourceHandled)
```

### 2. Restore Image Loading

Use `loadFileURL:completionHandler:` not `loadFromURL:completionHandler:`:

```go
// Code-Hex/vz pattern
objc.Send[objc.ID](
    objc.ID(objc.GetClass("VZMacOSRestoreImage")),
    objc.Sel("loadFileURL:completionHandler:"),
    fileURL.ID,
    handler,
)
```

### 3. VM Creation with Dispatch Queue

The Virtualization framework requires a serial dispatch queue for VM operations:

```go
queue := dispatch.QueueCreate("com.appledocs.vz.vm")
vm := objc.Send[vz.VZVirtualMachine](
    instance.ID,
    objc.Sel("initWithConfiguration:queue:"),
    config, queue.Handle(),
)
```

### 4. Installer Creation

Must be created on the VM's dispatch queue:

```go
vm.queue.Sync(func() {
    instance := vz.GetVZMacOSInstallerClass().Alloc()
    installer = objc.Send[vz.VZMacOSInstaller](instance.ID,
        objc.Sel("initWithVirtualMachine:restoreImageURL:"),
        vm.ID, fileURL.ID)
})
```

### 5. State Monitoring

Code-Hex/vz uses KVO (Key-Value Observing) via CGO with `ObservableVZVirtualMachine`. For purego, we poll:

```go
func (vm *virtualMachine) monitorState() {
    ticker := time.NewTicker(100 * time.Millisecond)
    for range ticker.C {
        newState := vz.VZVirtualMachineState(vm.vm.State())
        if newState != oldState {
            // Handle state change
        }
    }
}
```

### 6. Object Retention

Virtualization framework objects must be retained to prevent premature deallocation:

```go
// Retain objects passed to framework
objc.Send[objc.ID](config.ID, objc.Sel("retain"))
objc.Send[objc.ID](vm.ID, objc.Sel("retain"))
objc.Send[objc.ID](installer.ID, objc.Sel("retain"))

// DO NOT autorelease framework objects
```

### 7. Auxiliary Storage Creation

Must be created WITH hardware model for installation, and CRITICAL: pass `.ID` fields explicitly:

```go
// CORRECT - extract .ID explicitly
auxStorage := objc.Send[vz.VZMacAuxiliaryStorage](instance.ID,
    objc.Sel("initCreatingStorageAtURL:hardwareModel:options:error:"),
    url.ID, hwModel.ID, vz.VZMacAuxiliaryStorageInitializationOptionAllowOverwrite, unsafe.Pointer(&errPtr))

// WRONG - passing Go struct types directly may not work
auxStorage := objc.Send[vz.VZMacAuxiliaryStorage](instance.ID,
    objc.Sel("initCreatingStorageAtURL:hardwareModel:options:error:"),
    url, hwModel, ...)  // url and hwModel are structs, not objc.ID
```

### 8. Audio Device Configuration (CRITICAL FOR INSTALLATION)

**CRITICAL**: The DFU error 3004/4014 during installation is caused by missing audio stream configuration.
Without proper host audio streams, the MobileDevice XPC service fails during restoration.

Code-Hex/vz configures audio with host input/output streams:

```go
// Create audio device with host streams - REQUIRED for macOS installation!
audioConfig := vz.NewVZVirtioSoundDeviceConfiguration()

// Input stream with host source (microphone)
inputStream := vz.NewVZVirtioSoundDeviceInputStreamConfiguration()
hostInputSource := vz.NewVZHostAudioInputStreamSource()
inputStream.SetSource(hostInputSource)

// Output stream with host sink (speaker)
outputStream := vz.NewVZVirtioSoundDeviceOutputStreamConfiguration()
hostOutputSink := vz.NewVZHostAudioOutputStreamSink()
outputStream.SetSink(hostOutputSink)

// Set streams on the audio config
audioConfig.SetStreams([]vz.VZVirtioSoundDeviceStreamConfiguration{
    inputStream.VZVirtioSoundDeviceStreamConfiguration,
    outputStream.VZVirtioSoundDeviceStreamConfiguration,
})
```

Without this configuration, installation fails with MobileRestore errors:
- Code 3004: "Failed to copy auth install options in DFU mode"
- Code 4014: "Unexpected device state 'DFU' expected 'RestoreOS'"

## Installation Status

**WORKING** as of 2025-12-03: Pure purego macOS installation completes successfully!

Key fixes required:
1. Audio device streams (input/output with host source/sink)
2. Explicit `.ID` passing in `objc.Send` calls
3. Proper entitlements signing

Typical installation time: ~5 minutes for macOS restore image.

## Known Issues

### Main Queue Dispatch from Background Threads

**Problem**: Dispatching to the main queue (`dispatch_get_main_queue()`) from a goroutine or socket handler thread does NOT work reliably.

**Why**: The GCD main queue requires `NSApplication.run()` or `dispatch_main()` to drain it. `CFRunLoopRunInMode()` only pumps CFRunLoop sources, NOT the GCD main queue.

```go
// THIS WILL NOT WORK from a goroutine:
DispatchAsync(GetMainDispatchQueue(), func() {
    // Never executes unless NSApplication.run() is actively running
})

// THIS WILL DEADLOCK from a goroutine:
DispatchSync(GetMainDispatchQueue(), func() {
    // Waits forever for main queue that isn't draining
})
```

**Solutions**:
1. **Use custom dispatch queues** - They work correctly from any thread:
   ```go
   vmQueue := dispatch.QueueCreate("com.app.vm")
   DispatchAsyncQueue(vmQueue, func() { /* works! */ })
   ```

2. **Use thread-safe APIs** - Many CoreGraphics APIs are thread-safe:
   ```go
   // CGWindowListCreateImage is thread-safe - no main queue needed
   cgImage := coregraphics.CGWindowListCreateImage(bounds, options, windowID, imageOptions)
   ```

3. **Use CGEvent instead of NSEvent** - For input events:
   ```go
   // CGEventPost is thread-safe
   event := coregraphics.CGEventCreateKeyboardEvent(nil, keycode, keydown)
   coregraphics.CGEventPost(kCGHIDEventTap, event)
   ```

**Verified by**: `go test -run TestMainQueue` in dispatch_test.go

### DFU State Error (Code 4014 / 3004)

```
Domain: com.apple.MobileDevice.MobileRestore
Code: 4014 or 3004
Description: Unexpected device state 'DFU' expected 'RestoreOS'
         or: Failed to copy auth install options in DFU mode
```

**Primary Cause**: Missing audio device stream configuration (see section 8 above).

**Secondary Cause**: MobileDevice XPC services in corrupted state from previous failed installation attempts.

**Solutions**:
1. Ensure audio streams are configured with host source/sink (CRITICAL)
2. Clean VM directory before installation: `rm -rf ./vm`
3. If persists, reboot host machine to reset XPC services

### Sandbox Preferences Error

```
accessing these preferences requires user-preference-read or file-read-data sandbox access
```

**Solution**: Sign binary with virtualization entitlement:
```bash
codesign -s - -f --entitlements entitlements.plist ./vz-macos
```

### 9. Control Socket and Screenshots

The control socket (`~/.vz/vms/<name>/control.sock`) provides JSON-based VM control from any process.

**Thread-safe screenshot capture** uses `CGWindowListCreateImage`:

```go
// CGWindowListCreateImage is thread-safe (no main queue dispatch needed)
bounds := corefoundation.CGRect{} // CGRectNull = capture full window
cgImage := coregraphics.CGWindowListCreateImage(
    bounds,
    coregraphics.CGWindowListOption(8),  // kCGWindowListOptionIncludingWindow
    coregraphics.CGWindowID(windowNum),
    coregraphics.CGWindowImageOption(0), // kCGWindowImageDefault
)
```

**Pixel format conversion** - CGImage uses BGRA, Go images use RGBA:

```go
for y := 0; y < height; y++ {
    for x := 0; x < width; x++ {
        // BGRA to RGBA
        b := srcData[pixelStart]
        g := srcData[pixelStart+1]
        r := srcData[pixelStart+2]
        a := srcData[pixelStart+3]
        rgba.SetRGBA(x, y, color.RGBA{r, g, b, a})
    }
}
```

**VM operations** dispatch to the VM queue for thread safety:

```go
done := make(chan struct{})
DispatchAsyncQueue(s.vmQueue, func() {
    defer close(done)
    state = vz.VZVirtualMachineState(s.vm.State())
    canPause = s.vm.CanPause()
})
<-done
```

## File Structure

```
vz-macos/
├── main.go              # CLI entry point, VM running
├── installer.go         # macOS installation (vz-style)
├── blocks.go            # Objective-C block helpers, run loop
├── objc_helpers.go      # Low-level objc helpers
├── control_socket.go    # Unix socket server for VM control
├── control_client.go    # Programmatic control client for Go code
├── screenshots.go       # Screenshot capture using CGWindowListCreateImage
├── screen_detection.go  # Detect UI state (Setup Assistant, Login, Desktop)
├── setup_assistant.go   # Automate Setup Assistant via keyboard
├── provision.go         # Disk injection & LaunchDaemon provisioning
├── ipsw.go              # IPSW download utilities
├── linux.go             # Linux VM support (run)
├── linux_installer.go   # Linux installation with cloud-init
├── dispatch_test.go     # Diagnostic tests for dispatch/block behavior
└── entitlements.plist
```

## Linux VM Support

### Quick Start

```bash
# Install Ubuntu Server 24.04 ARM64 (auto-downloads ISO)
./vz-macos install -linux

# Install with custom user credentials
./vz-macos install -linux -provision-user myuser -provision-password secret123

# Run the installed Linux VM
./vz-macos run -linux

# Run with GUI display
./vz-macos run -linux -gui
```

### Architecture

Linux VMs use:
- **VZGenericPlatformConfiguration** - Generic platform for non-macOS guests
- **VZEFIBootLoader** - EFI boot with NVRAM variable store
- **VZVirtioGraphicsDeviceConfiguration** - Virtio GPU for display
- **Cloud-init NoCloud** - Automated installation via user-data/meta-data ISO

### Installation Flow

1. **ISO Download**: Auto-downloads Ubuntu Server 24.04 ARM64 if not provided
2. **Cloud-Init ISO**: Creates `cidata.iso` with autoinstall configuration
3. **VM Boot**: Boots from Ubuntu ISO with cloud-init datasource attached
4. **Autoinstall**: Ubuntu reads `user-data` and installs unattended
5. **Reboot**: After installation, VM reboots and boots from disk

### Cloud-Init Configuration

The installer creates a NoCloud datasource with:

```yaml
#cloud-config
autoinstall:
  version: 1
  identity:
    hostname: ubuntu-vm
    username: <provision-user>
    password: <sha512-hashed-password>
  ssh:
    install-server: true
    allow-pw: true
  storage:
    layout:
      name: lvm
  late-commands:
    - curtin in-target -- systemctl enable ssh
```

### Boot Modes

| Mode | Flag | Description |
|------|------|-------------|
| EFI Boot | (default) | Uses VZEFIBootLoader, required for ISO installation |
| Direct Boot | `-kernel <path>` | Uses VZLinuxBootLoader with kernel + initrd |

### Direct Kernel Boot

For direct kernel boot (without EFI):

```bash
./vz-macos run -linux \
  -kernel /path/to/vmlinuz \
  -initrd /path/to/initrd.img \
  -cmdline "console=tty0 console=hvc0 root=/dev/vda"
```

### Serial Console

Linux serial output goes to stdout by default:

```bash
# Disable serial console
./vz-macos run -linux -serial none

# Write to file
./vz-macos run -linux -serial /tmp/linux-serial.log
```

### Known Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| Slow boot | EFI firmware initialization | Normal, wait ~30 seconds |
| No network | DHCP timeout | Check NAT networking settings |
| Black screen in GUI | Virtio GPU driver not loaded | Wait for kernel to load, or use serial console |

## Command Reference

```bash
# Install macOS (uses vz-style installer by default)
./vz-macos -install -ipsw ~/.cache/vz/restore.ipsw

# Use old installer flow
./vz-macos -install -ipsw ~/.cache/vz/restore.ipsw -vz-style=false

# Run macOS VM
./vz-macos -run

# Run with GUI
./vz-macos -run -gui

# Debug output
VZ_DEBUG_INSTALL=1 ./vz-macos -install -ipsw ~/.cache/vz/restore.ipsw -v
```

### Auto-Provisioning

There are two methods for automatic user provisioning:

#### Method 1: Inject Command (Recommended)

The `inject` command writes a self-contained LaunchDaemon directly into the VM disk image. This runs on first boot without requiring VirtioFS or external dependencies.

**IMPORTANT:** `inject` must be run with `sudo` for proper LaunchDaemon operation. launchd requires LaunchDaemon plists to be owned by root:wheel.

```bash
# Install macOS
./vz-macos install -ipsw restore.ipsw

# Inject provisioning files into the disk image (REQUIRES SUDO)
sudo ./vz-macos inject -user testuser -password secret123

# With -skip-setup-assistant to bypass Setup Assistant entirely
sudo ./vz-macos inject -user testuser -password secret123 -skip-setup-assistant

# Run the VM (codesign again after sudo install)
codesign -s - -f --entitlements vz.entitlements ./vz-macos
./vz-macos run
```

**How inject works:**

1. Attaches the VM disk image using `hdiutil attach`
2. Mounts the APFS "Data" volume using `diskutil mount`
3. Writes a self-contained provision script to `/var/db/vz-provision.sh` (owned by root:wheel)
4. Writes a LaunchDaemon plist to `/Library/LaunchDaemons/com.vz.provision.plist` (owned by root:wheel)
5. Optionally creates `.AppleSetupDone` to skip Setup Assistant
6. Optionally creates `/etc/kcpassword` for auto-login
7. Detaches the disk

On first boot, the LaunchDaemon:
- Creates the user account with `sysadminctl`
- Configures auto-login
- Self-cleans (removes script and plist)

#### Method 2: GUI Automation (Alternative)

Use runtime keyboard automation to navigate Setup Assistant:

```bash
# Run VM with auto-provisioning (requires -gui)
./vz-macos run -gui -provision-user testuser -provision-password secret123

# This will:
# 1. Start the VM with GUI window
# 2. Wait for Setup Assistant to appear
# 3. Navigate through setup screens using keyboard automation
# 4. Create the specified user account
# 5. Proceed to desktop
```

**Manual provisioning via ctl:**

```bash
# Detect current screen state
./vz-macos ctl detect

# Run Setup Assistant automation manually
./vz-macos ctl setup-assist <username> <password>
```

**How GUI automation works:**

1. `screen_detection.go` - Analyzes screenshots to detect UI state (Setup Assistant, Login Screen, Desktop)
2. `setup_assistant.go` - Navigates Setup Assistant using keyboard automation (Tab, Enter, typing)
3. `control_client.go` - Programmatic access to the control socket from Go code

**Screen detection heuristics:**

| Screen State | Detection Method |
|-------------|------------------|
| Black | Overall brightness < 10 |
| Apple Logo | Dark background, bright center |
| Setup Assistant | Gradient background, colorful |
| Login Screen | Light overall, top brighter than bottom |
| Desktop | Has dock at bottom (translucent bar) |

### Provisioning Architecture Deep Dive

#### macOS Boot Order

Understanding when provisioning runs:

```
Kernel → launchd → LaunchDaemons (provision runs HERE) → WindowServer → loginwindow
                                                                              ↓
                                                                   Setup Assistant (if needed)
                                                                              ↓
                                                                   Login Screen / Desktop
```

The `.AppleSetupDone` file is checked by `loginwindow` to determine whether to show Setup Assistant.

#### Setup Assistant Bypass Limitations

**Research findings (from macOS 14/15 testing):**

| Mechanism | Effect | Notes |
|-----------|--------|-------|
| `.AppleSetupDone` | Skips initial setup wizard | Primary method, works on fresh installs |
| `.SetupRegComplete` in `/Library/Receipts/` | Older OS X marker | May still help on some systems |
| `.skipbuddy` in `/Library/User Template/` | Suppresses per-user dialogs | Prevents iCloud prompts at first login |

**Known limitations:**

1. **macOS 14+ Sonoma/Sequoia**: Removing `.AppleSetupDone` no longer relaunches Setup Assistant if a local user already exists. This is fine for fresh VMs but means you can't re-trigger setup.

2. **First-login dialogs**: Even with `.AppleSetupDone`, users may see:
   - Diagnostics & Usage prompts
   - iCloud sign-in requests
   - Siri setup
   - Touch ID setup (if applicable)

3. **DEP/MDM enrollment**: `.AppleSetupDone` does NOT bypass Device Enrollment Program or MDM enrollment screens.

**Recommended approach for VMs:**

```bash
# 1. Create .AppleSetupDone to skip main Setup Assistant
touch /Volumes/Data/private/var/db/.AppleSetupDone

# 2. Create user first (via plist or LaunchDaemon) so Setup Assistant
#    sees an existing user and doesn't try to create one

# 3. Optionally suppress first-login dialogs with .skipbuddy
touch "/Volumes/Data/Library/User Template/English.lproj/.skipbuddy"
```

**Sources:**
- [toru173/Skipping-the-macOS-First-Run-Setup-Assistant](https://github.com/toru173/Skipping-the-macOS-First-Run-Setup-Assistant)
- [MagerValp/SkipAppleSetupAssistant](https://github.com/MagerValp/SkipAppleSetupAssistant)
- [MacAdmins Documentation](https://macadminsdoc.readthedocs.io/en/master/General/macOS_Installation/Setup_Assistant.html)

#### File Location Mapping

When the VM disk is mounted on the host, paths map as follows:

| Guest Path | Host Path (on Data volume) |
|-----------|---------------------------|
| `/var/db/` | `/Volumes/Data/private/var/db/` |
| `/Library/` | `/Volumes/Data/Library/` |
| `/Users/` | `/Volumes/Data/Users/` |
| `/etc/` | `/Volumes/Data/private/etc/` |

#### macOS User Database Structure

Users are stored in Directory Services Local (dslocal) plists:

```
/var/db/dslocal/nodes/Default/
├── users/
│   ├── root.plist
│   ├── testuser.plist    # Our provisioned user
│   └── ...
└── groups/
    ├── admin.plist        # Admin group membership
    ├── staff.plist
    └── ...
```

**User plist structure (ShadowHashData):**
```
SALTED-SHA512-PBKDF2:
  salt: 32 random bytes
  iterations: 45000 (macOS default)
  entropy: PBKDF2-SHA512(password, salt, iterations, 128 bytes)
```

#### Inject Command Options

```bash
# LaunchDaemon mode (default) - creates user on first boot via sysadminctl
./vz-macos inject -user testuser -password secret123

# Plist mode (advanced) - directly creates user plist with password hash
./vz-macos inject -user testuser -password secret123 -plist

# Combined install+inject
./vz-macos install -ipsw restore.ipsw -provision-user testuser -provision-password secret123

# With auto-login (boots directly to desktop)
./vz-macos inject -user testuser -password secret123 -skip-setup-assistant -auto-login

# Verbose mode for debugging
VZ_DEBUG_INJECT=1 ./vz-macos inject -user testuser -password secret123 -v
```

#### Auto-Login Configuration

Auto-login requires two files:

1. **kcpassword** (`/etc/kcpassword`): XOR-encoded password
   - Key: `0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F`
   - Padded to multiple of 11 bytes

2. **loginwindow.plist** (`/Library/Preferences/com.apple.loginwindow.plist`):
   ```xml
   <key>autoLoginUser</key>
   <string>testuser</string>
   ```

#### Troubleshooting

**Manually mount VM disk:**
```bash
hdiutil attach ~/.vz/vms/default/disk.img -nobrowse -nomount
diskutil list  # Find the Data volume (e.g., disk22s5)
diskutil mount /dev/disk22s5
ls /Volumes/Data/private/var/db/
```

**Verify injected files:**
```bash
cat /Volumes/Data/private/var/db/vz-provision.sh
cat /Volumes/Data/Library/LaunchDaemons/com.vz.provision.plist
ls -la /Volumes/Data/private/var/db/.AppleSetupDone
```

**Check provisioning log inside VM:**
```bash
cat /var/log/vz-provision.log
```

**Common issues:**

| Issue | Cause | Solution |
|-------|-------|----------|
| "Resource temporarily unavailable" | VM is running | Stop VM before inject |
| "could not find Data partition" | APFS container detection failed | Check verbose output (-v) |
| User not created on boot | LaunchDaemon not loaded | **Run inject with sudo** - launchd requires root:wheel ownership |
| Setup Assistant still appears | `.AppleSetupDone` missing | Use `-skip-setup-assistant` flag |
| Auto-login not working | kcpassword incorrect or wrong owner | Run inject with sudo, check password encoding |
| Verify command shows WRONG_OWNER | Files created without sudo | Re-run inject with sudo |

## End-to-End Provisioning Workflow

### Quick Start (Recommended)

```bash
# 1. Install macOS to create a fresh VM
./vz-macos install -ipsw restore.ipsw

# 2. Inject provisioning with sudo (REQUIRED for proper ownership)
sudo ./vz-macos inject -user testuser -password secret123 -skip-setup-assistant

# 3. Verify the injection was successful
./vz-macos verify

# 4. Run the VM
./vz-macos run -gui
```

### Expected Result
With correct injection:
1. **Boot**: VM starts, Apple logo appears
2. **Skip Setup**: `.AppleSetupDone` marker skips Setup Assistant
3. **User Creation**: LaunchDaemon runs provisioning script at boot
4. **Auto-Login**: kcpassword + loginwindow.plist enable automatic login
5. **Desktop**: User is logged in automatically

### Verification

Use the `verify` command to check injection status:

```bash
./vz-macos -vm <name> verify
```

**Expected output for successful injection:**
```
✓ Library/LaunchDaemons/com.vz.provision.plist
    Status: OK
✓ private/var/db/vz-provision.sh
    Status: OK
✓ private/var/db/.AppleSetupDone
    Status: OK (uid=0 gid=0)
✓ private/etc/kcpassword
    Status: OK
✓ Library/Preferences/com.apple.loginwindow.plist
    Status: OK
```

**If you see WRONG_OWNER:** Re-run inject with sudo.

### Why Sudo is Required

macOS launchd has security requirements:
- LaunchDaemon plists **must be owned by root:wheel** (uid=0, gid=0)
- launchd silently ignores daemons with incorrect ownership
- This is a security feature to prevent privilege escalation

When `inject` runs without sudo:
- Files are created with your user's UID (e.g., uid=501)
- launchd won't load the daemon
- Provisioning script never runs
- User is never created
- Auto-login fails (no user to log in as)

### Alternative: GUI Password Prompt

Use the helper script to prompt for password via GUI:

```bash
./inject-as-root.sh -vm test-vm -user testuser -password secret123 -skip-setup-assistant
```

### Troubleshooting Workflow

```bash
# 1. Check if VM disk is available
ls -la ~/.vz/vms/<name>/disk.img

# 2. Verify injection
./vz-macos -vm <name> verify

# 3. If WRONG_OWNER, re-inject with sudo
sudo ./vz-macos -vm <name> inject -user testuser -password secret123 -skip-setup-assistant

# 4. Verify again
./vz-macos -vm <name> verify

# 5. Run VM
./vz-macos -vm <name> run -gui

# 6. If still failing, check provisioning log inside VM
# (boot VM, log in manually, then check /var/log/vz-provision.log)
```

## macOS Version Compatibility

### Tested Versions

| Guest macOS | Host macOS | Status | Notes |
|-------------|------------|--------|-------|
| macOS 14 Sonoma | macOS 14+ | ✅ Working | Primary test platform |
| macOS 15 Sequoia | macOS 15+ | ✅ Working | Latest supported |
| macOS 13 Ventura | macOS 13+ | ⚠️ Untested | Should work |

### Feature Compatibility

| Feature | macOS 13 | macOS 14 | macOS 15 |
|---------|----------|----------|----------|
| `.AppleSetupDone` marker | ✅ | ✅ | ✅ |
| LaunchDaemon provisioning | ✅ | ✅ | ✅ |
| kcpassword auto-login | ✅ | ✅ | ⚠️ May require FileVault off |
| SALTED-SHA512-PBKDF2 | ✅ | ✅ | ✅ |
| sysadminctl user creation | ✅ | ✅ | ✅ |

### Password Hash Format

macOS 10.8+ uses **SALTED-SHA512-PBKDF2** for password hashing:

```
ShadowHashData (binary plist):
├── SALTED-SHA512-PBKDF2:
│   ├── entropy: 128-byte derived key
│   ├── iterations: 45000 (macOS default)
│   └── salt: 32 random bytes
```

The password hash is stored in the user plist at:
`/var/db/dslocal/nodes/Default/users/<username>.plist`

### Known Version-Specific Issues

| Version | Issue | Workaround |
|---------|-------|------------|
| macOS 14+ | FileVault may block kcpassword | Disable FileVault or use keyboard automation |
| macOS 15 | Stricter SecureToken requirements | Use LaunchDaemon mode (not plist mode) |
| All | LaunchDaemon requires root:wheel | Always run inject with sudo |

### Virtualization Framework Requirements

| Host macOS | Virtualization API | Notes |
|------------|-------------------|-------|
| macOS 12+ | VZMacOSVirtualMachineStartOptions | Required for recovery mode |
| macOS 13+ | VZMacOSBootLoader improvements | Better IPSW support |
| macOS 14+ | VZGraphicsDisplay enhancements | Improved GUI support |

## Code-Hex/vz Reference

Key files to compare:
- `virtualization_arm64.go` - Go bindings for macOS installer
- `virtualization_12_arm64.m` - Objective-C implementation
- `virtualization_11.m` - VM creation, state observation
- `example/macOS/` - Example macOS VM application

## Advanced Features

### VM Snapshots

Save and restore VM state for quick resume.

```bash
# Save snapshot (VM must be running, use control socket)
echo '{"type":"snapshot","data":{"action":"save","name":"checkpoint1"}}' | \
  nc -U ~/.vz/vms/default/control.sock

# Restore snapshot (VM must be stopped first)
echo '{"type":"snapshot","data":{"action":"restore","name":"checkpoint1"}}' | \
  nc -U ~/.vz/vms/default/control.sock

# List snapshots
./vz-macos snapshot list

# Delete snapshot
./vz-macos snapshot delete checkpoint1
```

Snapshots are saved to `~/.vz/vms/<vmname>/snapshots/<name>.vmstate`.

**Important**: The VM must be paused (not running) to save a snapshot. The save command automatically pauses, saves, and resumes.

### Memory Balloon Control

Dynamically adjust VM memory at runtime.

```bash
# Get memory info via control socket
echo '{"type":"memory","data":{"action":"info"}}' | \
  nc -U ~/.vz/vms/default/control.sock

# Set memory target to 4GB
echo '{"type":"memory","data":{"action":"set","sizeGB":4}}' | \
  nc -U ~/.vz/vms/default/control.sock
```

Memory balloon requires guest cooperation - the guest OS must have balloon drivers.

### Advanced Networking

Support for different network modes.

```bash
# NAT networking (default)
./vz-macos run -network nat

# Bridged to host interface
./vz-macos run -network bridged:en0

# No networking
./vz-macos run -network none

# List available interfaces
./vz-macos network list
```

### USB Mass Storage

Attach disk images as USB storage devices.

```bash
# Attach USB disk (read-write)
./vz-macos run -usb /path/to/disk.img

# Attach USB disk (read-only)
./vz-macos run -usb /path/to/disk.img:ro

# Multiple USB devices
./vz-macos run -usb /path/to/data.img -usb /path/to/backup.img:ro
```

### Multi-Display Support

Configure multiple displays for the VM.

```bash
# Single display with specific resolution
./vz-macos run -display 1920x1080

# 4K display
./vz-macos run -display 4k

# Multiple displays
./vz-macos run -display 1920x1080 -display 1024x768

# Custom PPI
./vz-macos run -display 2560x1440@144
```

Presets: `4k`, `1080p`, `720p`, `retina`

### Rosetta for Linux VMs

Enable x86-64 binary translation on ARM64 Linux VMs (Apple Silicon only).

```bash
# Check Rosetta status
./vz-macos rosetta status

# Install Rosetta if needed
./vz-macos rosetta install

# Show guest setup instructions
./vz-macos rosetta setup

# Run Linux VM with Rosetta enabled
./vz-macos run -linux -rosetta
```

**Guest setup** (inside Linux VM):
```bash
sudo mkdir -p /run/rosetta
sudo mount -t virtiofs rosetta /run/rosetta
sudo /run/rosetta/rosetta --register
```

After setup, x86-64 binaries run transparently through Rosetta.

## File Structure

```
vz-macos/
├── main.go              # CLI entry point, VM running
├── macos.go             # macOS VM configuration
├── linux.go             # Linux VM configuration
├── installer.go         # macOS installation
├── linux_installer.go   # Linux installation with cloud-init
├── blocks.go            # Objective-C block helpers, run loop
├── objc_helpers.go      # Low-level objc helpers
├── control_socket.go    # Unix socket server for VM control
├── control_client.go    # Programmatic control client for Go code
├── control_socket_commands.go # Command type definitions
├── screenshots.go       # Screenshot capture using CGWindowListCreateImage
├── screen_detection.go  # Detect UI state (Setup Assistant, Login, Desktop)
├── setup_assistant.go   # Automate Setup Assistant via keyboard
├── provision.go         # Disk injection & LaunchDaemon provisioning
├── snapshots.go         # VM state save/restore
├── memory.go            # Memory balloon runtime control
├── networking.go        # Advanced networking (NAT, bridged, vmnet)
├── usb.go               # USB mass storage support
├── display.go           # Multi-display configuration
├── rosetta.go           # Rosetta 2 for Linux VMs
├── ipsw.go              # IPSW download utilities
├── volumes.go           # VirtioFS volume mounts
├── clone.go             # VM cloning
├── template.go          # VM templates
├── vm_registry.go       # VM registry and management
├── vm_ops.go            # VM operations (export/import)
└── entitlements.plist
```
