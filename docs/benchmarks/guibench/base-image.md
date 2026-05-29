# Pre-granted base image build procedure

How to build the curated macOS base image the GUI-agent benchmark forks from
(design [047](../../designs/047-gui-agent-benchmark-harness.md) §5, §6, §8,
slice 3). Every task forks one fresh ephemeral RAM-overlay child of this image,
so the image — not the per-run fork — carries the TCC grants the verifier needs,
a pinned clock, and pinned app versions. A child cannot acquire a TCC grant on
its own (the prompt is interactive and the RAM overlay discards the answer), so
**the grant must already be in the image**, verified before the image is saved.

This is an operator procedure on Apple-Silicon hardware. The verification step
is automated (`cove bench gui image-check`); the build itself is manual because
TCC grants are approved through the macOS GUI.

## Why grants must be baked in

A naive "non-Apple-Events getters need no grant" assumption is wrong on modern
macOS (design 047 §5). Reading a protected app's SQLite store (Mail, Messages,
Safari history) or many `~/Library` paths needs **Full Disk Access**; an
under-granted read does not error, it *silently fails*, so the benchmark would
read as "agents fail at macOS" when the truth is "the image was under-granted".
Three distinct, independent TCC services are involved — do not conflate them:

| Tier | Grant | Getters it unlocks |
|---|---|---|
| A | none | `exec`, `file` (user-space), `defaults`, `screen_ocr` |
| B | Full Disk Access | `sqlite`, `protected_file`, `tccdb` |
| C | Apple Events **and** Accessibility | `applescript`, `accessibility` |

Full Disk Access does **not** cover Apple Events, and Apple Events does **not**
cover Accessibility; each is granted separately. The base image must carry every
grant its corpus needs and no more — `cove bench gui image-check` reads the
required level off the corpus (`guibench.MaxTier`) and refuses an image missing
any of them.

## Procedure

1. **Start from a clean install.** Provision a macOS VM through the normal cove
   flow (`cove run` / setup-assistant automation), logged into a local,
   non-iCloud account. Exclude iCloud/Keychain/Apple-ID state: cloned siblings
   share the SEP identity (design 047 §6), so the corpus excludes Apple-ID tasks
   in v1; do not sign the base image into iCloud.

2. **Pin the system clock.** Disable automatic time and set a fixed date so
   time-derived task state is reproducible across forks (AndroidWorld pins
   `2023-10-15T15:34Z`; pick one date and record it):

   ```sh
   sudo systemsetup -setusingnetworktime off
   sudo systemsetup -setdate MM:DD:YYYY
   sudo systemsetup -settime HH:MM:SS
   ```

3. **Pin app versions.** Install the exact app builds the corpus targets (Safari
   ships with the OS; pin the macOS build, and pin any added app). Record every
   version in the image's notes — a task that reads a Safari tab URL via the
   `accessibility` getter is sensitive to the app's AX-tree layout, which moves
   across versions.

4. **Grant Full Disk Access (Tier B).** System Settings → Privacy & Security →
   Full Disk Access → add `/usr/local/bin/vz-agent`, approve the prompt. This is
   what lets the `sqlite`/`protected_file`/`tccdb` getters read protected stores.

5. **Grant Apple Events and Accessibility (Tier C).** These are separate
   toggles:
   - Privacy & Security → Automation → allow `vz-agent` to control the apps the
     corpus scripts (this is the Apple Events grant the `applescript` getter
     needs).
   - Privacy & Security → Accessibility → add and enable `vz-agent` (this is the
     Accessibility grant the `accessibility` AX-tree getter needs).

   Reset any stale denial first with `tccutil reset AppleEvents` /
   `tccutil reset Accessibility`.

6. **Settle the image.** Let the VM sit at the desktop past the macOS boot-churn
   window (design 004) so background indexing and first-run work finish before
   capture, then flush pending preference writes (`killall cfprefsd`) so the
   saved image holds settled state.

7. **Verify the grants before saving.** Leave the VM running and, from the host,
   run the check against it:

   With no `-corpus`, all three grants are required — the safe default for a
   general-purpose base image:

   ```sh
   cove bench gui image-check -vm <running-fork>
   ```

   Or scope the check to exactly what a corpus needs (`image-check` derives the
   required tier from the corpus's getters via `guibench.MaxTier`, so an
   all-Tier-A corpus requires no grants):

   ```sh
   cove bench gui image-check -vm <running-fork> -corpus path/to/corpus
   ```

   Either way `image-check` runs the `cove doctor` TCC probes
   (`verifyTCCFDAProbe` for FDA, strict probes for Apple Events and
   Accessibility), prints one line per required grant, and exits nonzero if any
   is missing — **do not save the image until it passes.**

8. **Save the image.** Once `image-check` reports `all required grants present`,
   stop the VM and commit it to the image store (`cove image build` / save flow,
   design [024](../../designs/024-cove-runner-images.md)). Record the pinned
   clock date, the app versions, and the `image-check` output in the image's
   provenance notes.

## Gating an image-save script

`image-check` is exit-code gated, so a build script can refuse to save an
under-granted image:

```sh
if cove bench gui image-check -vm "$FORK" -corpus "$CORPUS"; then
    cove image build ...   # save only on a clean check
else
    echo "image is under-granted; not saving" >&2
    exit 1
fi
```

## What this does not do

- It does not build the image for you: TCC grants are approved through the GUI,
  which needs an operator on hardware.
- It does not cover iCloud/Keychain/Apple-ID grants — those are excluded from
  the v1 corpus by the shared-SEP-identity constraint (design 047 §6).
- A passing check means the grants are present *now*; re-run it after any image
  edit, since a macOS update or a `tccutil reset` can revoke them.
