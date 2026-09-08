# Windows ARM64 installation under VZ

On macOS 27.0 build 26A5425a, a disposable 64 GiB NVMe disk reached a
Windows 11 Pro desktop under Apple Virtualization.framework. Cold boots
and a Windows-initiated restart reconnected the guest display. Visible
checks covered text, mouse selection, right-click, dragging, scrolling,
Windows+R, and Ctrl+A. QEMU was not used for these runs.

The native AppKit window displays guest-captured PNGs. It is not a native
VZ scanout: the synthetic GOP framebuffer lives in guest RAM. The research
transport captures about twice per second and polls input independently.
Its scope is desktop interaction, not accelerated graphics or a production
remote desktop service. Earlier input-effect checks used the HTTP control protocol directly.
In `cove-03`, the user physically opened Notepad through the native window
and entered `NATIBE WINDOW VHECK`. No assistant input preceded that check.
Two letters differ from the requested text; user clarification remains
pending. Windows restarted and the same viewer reconnected; physical input
in that warm-restart session was not checked before the deadline.
A subsequent cold boot from the clean `cove-02` checkpoint (`cove-05`)
received physical mouse and keyboard input, and the user confirmed basic
interaction worked. The screenshot includes repeated K characters and
`VKBD`; exact intended keys and repeat behavior remain unconfirmed.
See the [native input receipt](receipts/native-input.json). Host event-injection preflight returned false, so no automation
permission was requested or changed.

[Receipt manifest](receipts/20260908.json) records exact frame paths,
hashes, sessions, launch commands and binaries. Raw artifacts remain under
`~/.vz/research/windows-vz-20260908`, outside git. The
[acceptance plan](../installation-plan.md) records remaining physical AppKit input and recovery limitations.
The research implementation and audit notes have been pushed.

## Verified progression

| Run | Result |
| --- | --- |
| `inventory-01` | Stock NVMe driver bound to the new 64 GiB target; NetKVM obtained DHCP; WinPE PNGs reached the host. |
| `install-01` | Stopped before partitioning: WinPE had written a four-byte MBR disk signature. |
| `install-02` | Guard tests passed in WinPE; DISM applied Windows and NetKVM; BCDBoot and recovery configuration succeeded; guest shut down. Applied master preserved. |
| `desktop-01` | Installer-free clone completed setup phase 4, then VZ reported an internal error. |
| `desktop-02` | Persisted cold boot failed in about 3.6 seconds. Windows' new vendor EFI entry bypassed the fallback shim. |
| `vendor-01` | Two-path shim reached OOBE, installed two OOBE updates, rebooted and finally delivered desktop frames 124–134. An earlier live check missed those final frames. |
| `visual-01` | Cold boot reconnected before an unnecessary diagnostic key sequence. Subsequent input checks failed; that diagnostic sequence was removed. |
| `visual-02` | Clean cold boot: frame 258 shows mouse-opened Search; frame 267 shows typed `notepad`. |
| `interactive-01` | Cold boot of the desktop clone; upgraded to session-bound input. Windows restart reconnected as session `20260909T034008-6836`; v4 receiver frame 537 shows fresh Notepad text and a right-click menu. |
| `visual-03` | Updated GUI agent verified text, context menu, drag, wheel and Windows+R. Windows restarted through `shutdown /r /t 0`; the new session opened Notepad, dismissed its first-run prompt and displayed the test line in frame 522. |

The last two sets of frame numbers refer to different receiver runs; use
the full paths in the manifest. Guest session strings contain guest wall
clock time and PID. Host receipt timestamps and guest uptime distinguish
restarts; guest time changed timezone during setup.

`visual-03` ultimately reached its 1800-second host deadline before guest
shutdown was confirmed. Its stopped disk and EFI state were preserved as
`desktop-checkpoint`; the native user session uses a separate `interactive`
clone. Do not describe that checkpoint as a confirmed clean shutdown.

`interactive-01` also ended at its 1800-second host deadline. The final
v4 disk and EFI state were preserved as `installed-v4-checkpoint` after
confirming the VM process had exited. Read-only inspection verified both
shim entry hashes, the retained Microsoft loader, the v4 agent hash, and
an exact match between the installed user Startup script and maintained
source. See the [checkpoint receipt](receipts/installed-v4-checkpoint.json).
This checkpoint also lacks a confirmed guest-initiated shutdown. The viewer
may remain open with its last frame, but this VM run is no longer live.


## Required boot paths

Windows adds a firmware entry for `EFI/Microsoft/Boot/bootmgfw.efi` during
first boot. Patching only `EFI/BOOT/BOOTAA64.EFI` is insufficient afterward.
On the stopped disposable clone, retain Microsoft's loader as
`EFI/Microsoft/Boot/bootmgfw-real.efi`, then place `INSTALLED.EFI` at both
entry paths. This variant chainloads the retained loader without recursion.

| File | SHA-256 |
| --- | --- |
| Original `SHIM.EFI` | `bab5bd9e8822c51c0cc03713f63912886d51a510d5cd7d4ad6c3cc2ba876ef40` |
| `INSTALLED.EFI` | `cb9d6acde04dcd644cb5a18f7e7cb8cd93fb098909744b4d1fd8fcc142a0f042` |
| Microsoft `bootmgfw-real.efi` | `26085cabe01870a8921b61efd135a59898a51daae25b64b6bedba5830fb472f9` |
| Final guest display agent | `9d7c96acc9865d64200edd0e888702366fc5b06973533f83c67b95715ac49d2e` |

`patch_installed.py` verifies supplied hashes, preserves the loader,
rejects an unexpected vendor loader, and permits an identical repeat.
It was run twice on a fresh APFS clone of the first-boot checkpoint; both
runs produced [these hashes](receipts/efi-patch.json). The vendor shim also
retained its hash after the OOBE updates in `vendor-01`. The hashes were checked again after the final guest restart in `visual-03`
and still matched. This does not establish that future Windows servicing
will preserve either entry.

## Reproduction

Run host commands from the repository root. Choose a new experiment root;
existing directories are deliberately refused by preparation and run steps.
The scripts use macOS disk-image tools, APFS cloning, Python 3, an ARM64 Go
toolchain, wimlib, clang and lld-link. All compiled outputs belong outside git.

```sh
ROOT="$HOME/.vz/research/windows-vz-new"
mkdir -p "$ROOT/media"
GOOS=windows GOARCH=arm64 go build -ldflags=-H=windowsgui \
  -o "$ROOT/screen.exe" docs/research/windows-efi/installation/screen_windows.go
GOOS=windows GOARCH=arm64 go build -o "$ROOT/diskcheck.exe" \
  ./docs/research/windows-efi/installation/diskcheck
GOOS=windows GOARCH=arm64 go test -c -o "$ROOT/diskcheck-test.exe" \
  ./docs/research/windows-efi/installation/diskcheck
go build -o "$ROOT/winbootprobe" ./cmd/winbootprobe
codesign -s - -f --entitlements cmd/cove/vz.entitlements "$ROOT/winbootprobe"
bash docs/research/windows-efi/shim/build.sh "$ROOT/shim"
```

Recover the catalog entry from Microsoft's HTTPS catalog at
`https://go.microsoft.com/fwlink?linkid=2156292`, or inspect the retained
[entry](receipts/catalog-entry.json) and [verified media hashes](receipts/media-verified.json).
Put the reviewed entry at `ROOT/media/catalog-entry.json`, then run:

```sh
python3 docs/research/windows-efi/installation/download.py "$ROOT/media"
python3 docs/research/windows-efi/installation/prepare.py "$ROOT" \
  --wimlib /path/to/wimlib-imagex
python3 docs/research/windows-efi/installation/viewer.py "$ROOT/viewer" --seconds 3600
```

The receiver is a foreground process; use another terminal for VM runs.
Preparation expects `~/.vz/windows-drivers/virtio-win-0.1.285.iso` with the
retained SHA-256
`bff25ed15cc6af578547e63b7c4afa8b5be4660f94f5c6b41d1bea34979ede02`.
It exports Windows 11 Pro, splits its WIM for FAT32, injects the WinPE hook
and NetKVM, and writes an unbooted installer master. Never boot the master.

```sh
python3 docs/research/windows-efi/installation/run.py "$ROOT" inventory-01
```

Review the recovered disk and driver inventory before writing
`ROOT/inventory-passed.json`. The runner creates a new target for each
installation, and the guest guard checks its exact size and first MiB.
Only the four-byte MBR disk signature at offsets 440–443 may be nonzero.
The guard is supplementary: it does not authorize an existing system disk.

```sh
python3 docs/research/windows-efi/installation/run.py "$ROOT" install-01 \
  --mode install --seconds 600
```

Require successful `INSTALL.LOG` and `APPLIED.TAG`, then preserve that
applied target and state. APFS-clone the target with `cp -c`, copy its
state directory, and boot the clone without installation media:

```sh
python3 docs/research/windows-efi/installation/run.py "$ROOT" firstboot \
  --mode boot --target "$ROOT/desktop/target.img" \
  --state "$ROOT/desktop/state" --seconds 180
```

After the guest stops, inspect Panther logs for first-boot progress.
Preserve a checkpoint before patching. Attach only the stopped disposable
clone, identify its EFI partition from `hdiutil attach -plist`, and mount
that partition. Pass its actual mount point as `ESP`:

```sh
python3 docs/research/windows-efi/installation/patch_installed.py "$ESP" \
  "$ROOT/shim/INSTALLED.EFI" \
  --loader-sha256 26085cabe01870a8921b61efd135a59898a51daae25b64b6bedba5830fb472f9 \
  --shim-sha256 cb9d6acde04dcd644cb5a18f7e7cb8cd93fb098909744b4d1fd8fcc142a0f042
```

Detach the image before booting. Retain its EFI state; do not attach USB
installation media. Allow up to five minutes for OOBE and its updates.

```sh
python3 docs/research/windows-efi/installation/run.py "$ROOT" desktop-01 \
  --mode boot --target "$ROOT/desktop/target.img" \
  --state "$ROOT/desktop/state" --seconds 1800
bash docs/research/windows-efi/installation/build-window.sh "$ROOT/window"
open "$ROOT/window/Windows on VZ.app" --args "$ROOT/viewer/viewer.url"
```

This sequence composes the final sources; the recorded initial installation
used the earlier display-agent version, subsequently updated inside the
scratch guest. The final agent rebuilt to the same hash as the binary
verified after restart. A fresh installation with the final agent has not
been rerun. Its ordinary Startup script is `desktop.cmd`.
`desktop-user.cmd` records the live upgrade workaround used in this run:
the user Startup folder launches `C:\CoveDisplayV4\screen-v4.exe` after stopping
the old agents. No elevation or host security change was used.

Input requests and polling carry the guest session identifier. A new guest
session clears pending receiver commands; the native window clears its local
queue and held-key state. Requests for an old or stale session are rejected.
This fixes a recorded failure in which shutdown command fragments queued near
the previous VM deadline reached the next boot and opened Recycle Bin.
The regression test covers that queue boundary; the v4 guest restart separately
verified fresh text and a context menu after reconnecting.

For scripted input, `control.py` binds the whole sequence to the initially
visible session and stops on a session change:

```sh
python3 docs/research/windows-efi/installation/control.py "$ROOT/viewer" \
  'text:hello' 'key:13'
```

## Cove opt-in and remaining gates

Cove adds `-windows-native-pmu`, default false. It enables the private PMU
selector and checks that the setting is retained. The explicit research
setting `COVE_WINDOWS_MEDIA_TOPOLOGY=probe` suppresses audio and vsock while
retaining the probe's balloon, keyboard, pointing, entropy and USB devices.
The PMU and topology settings remain opt-in.

For a separate cove launch clone, copy the probe's `efi.nvram` into its
state directory and copy `machine.id` as `windows-machine.id`. The disk
must already have both installed EFI paths patched. Build and sign the current
cove source, then launch with an isolated host home directory:

```sh
go build -o "$ROOT/cove" ./cmd/cove
codesign -s - -f --entitlements cmd/cove/vz.entitlements "$ROOT/cove"
mkdir -p "$ROOT/cove-clone/home"
HOME="$ROOT/cove-clone/home" COVE_WINDOWS_MEDIA_TOPOLOGY=probe \
  "$ROOT/cove" run -windows -windows-backend vz \
  -windows-native-pmu -windows-graphics virtio -cpu 4 -memory 8 \
  -display 1920x1200 -network nat -serial none -headless -no-resume \
  -disk "$ROOT/cove-clone/target.img" -vm-dir "$ROOT/cove-clone/state"
```

The synced `cove-marina` and `cove-ocispec` modules resolved the dependency
blocker; neither dependency was removed. Two pre-existing framebuffer binding
compile errors were corrected. `go build ./...`, the run-directory regression
and Windows configuration tests, and `go test ./...` with an isolated `HOME`
pass. With the host home directory, the full suite instead traps in the
existing `TestPrivateAPI_NameGetSet` default-VM diagnostic. Isolated-home
success does not validate that diagnostic or other skipped host-VM fixtures.

`cove-02` cold-booted the installed clone through the actual cove executable,
reconnected after a Windows restart, and displayed new Notepad text and a
right-click menu in both sessions. `shutdown /s /t 0` then stopped Windows;
cove logged `VM stopped` and exited 0 before its 600-second deadline.
The stopped clone is preserved. See the [cove launch receipt](receipts/cove-launch.json).

The first cove launch exposed an isolation bug: `resolveRunTarget` rejected
an explicit state directory whose disk was elsewhere and silently selected
the active `mlx-lm.covevm` directory. The run was stopped. Its original saved
state file was restored and newly created run artifacts moved to scratch.
The default disk and auxiliary image retain their earlier modification times.
The overwritten CPU/memory fields were subsequently restored to 8/32 from
a recorded successful config write matching the pre-incident modification
time. The recovered file is 686 bytes, matching the historical listing;
other parsed fields were preserved. This is semantic field recovery, not
proof of exact original file bytes. A pre-incident directory listing had no
`suspend.config.json`, so no fingerprint was recreated. The restored saved
state correlates with an earlier 4-CPU/8-GB run. No default-VM boot was used
to test recovery. A regression test reproduced the
fallback; the fix honors explicit directories and rejects invalid targets.
Subsequent validation used a separate `HOME` as additional containment.

Focused `cmd/winbootprobe` and `internal/filehandle` tests pass. Run the
Python checks with:

```sh
python3 docs/research/windows-efi/installation/transport_test.py
python3 docs/research/windows-efi/installation/patch_installed_test.py
```

They cover bounded receiver behavior, queue ordering, stale-session rejection,
queue discard on session change, independent input
polling without duplicate screenshot delivery, media resume/hash checks,
and EFI backup/idempotence/refusal. Actual guest frames establish desktop
interaction separately.

The required commit helper was attempted with `CGPT_MODEL=claude-haiku-4-5`.
It reported insufficient Anthropic credits and HEAD stayed at `53655f33`
despite a success exit status. The user subsequently authorized direct
commits in Go project style. Source changes are separated into probe,
installed-loader shim, cove opt-in, and reproduction/evidence commits.
The research branch and `refs/notes/windows-vz-research` carry the commits
and their audit notes. The receipt manifest's earlier remote fields record
the pre-landing snapshot; use branch history for the current landing state.
The former module blocker is resolved; the validation scope and metadata incident are recorded above.

The result depends on private VZ PMU and storage APIs, the synthetic GOP,
and this exact host build. Other macOS builds and future Windows servicing
remain unverified. No restricted entitlement, host security setting change,
or QEMU execution was used for these installation and desktop receipts.

## Operational findings

- Historical `/tmp` media were absent. Microsoft ESD size and catalog SHA-1
  were verified before use; the published media URL is HTTP. Substituting
  HTTPS failed certificate hostname validation, which was not bypassed.
- Curl timed out 30,071,366 bytes short and its retry truncated the output.
  `download.py` instead preserves validated range parts and checks total
  size plus catalog hash. An optional address must currently resolve for
  the published hostname; it does not change the HTTP Host header.
- NetKVM extraction needs the full directory in a fresh location because
  ISO entries contain cross-directory hard links.
- A separate-thread input experiment reported successful SendInput calls
  without visible effects. The working final agent performs input on its
  main locked GUI thread and uses a GUI subsystem binary. That combined
  change worked; the experiment does not isolate which change was necessary.
- A reported unnamed macOS developer warning initially had uncertain
  provenance. Detailed logs later showed `amfid` rejecting
  `/Applications/UTM.app/Contents/MacOS/utmctl` at 12:41:57.612, followed by
  the unnamed policy event at 12:41:57.636. This strongly identifies the
  likely target, not its caller. Our viewer's earlier launch was separate.
  Packaging the viewer with an explicit identity does not confer trusted
  signing or establish provenance of that unrelated dialog.

Procedure references:
[Microsoft image application and boot setup](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/capture-and-apply-windows-system-and-recovery-partitions?view=windows-11),
[local account answer-file settings](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/microsoft-windows-shell-setup-useraccounts-localaccounts-localaccount),
and [mouse input layout](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-mouseinput).
