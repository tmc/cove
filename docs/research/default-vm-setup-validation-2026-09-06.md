# Default VM setup validation

Follow-up to [the boot repair](default-vm-boot-resolution-2026-09-06.md).
Validation uses two disposable APFS clones of the repaired default disk, each with
4 CPUs and 8 GiB RAM. The default retains its clean installation and 8 CPU / 32 GiB
configuration. Both clones use the local account `cove-test` with a generated test
password held in a mode-0600 file outside the repository.

## Fixes

- GUI provisioning, cached-password login, and the default unattended flow capture
  the guest framebuffer
  and send input directly. Window capture included the host's provisioning overlay,
  which obscured Setup Assistant.
- Pointer events go through `VZVirtualMachineView` so it translates window
  coordinates. Calling the private VM pointer selector directly reported success
  without activating the intended control.
- Coordinate mapping handles the framebuffer cache's 52-pixel top inset:
  captured 1024x852, view content 1024x800. Previously, clicking Full Name landed
  on Account Name.
- Account creation locates Full Name with OCR, then fills full name, account name,
  password, and verification in keyboard traversal order.
- Setup handles Written and Spoken Languages separately from the language chooser,
  selects Not Now before attempting migration, and recognizes the Apple Account
  sign-in page, its Other Sign-In Options → Sign in Later in Settings menu, and
  Age Range. The test account selects Adult. Siri & Dictation takes priority over
  the generic analytics marker in its body text.
- The `github.com/tmc/apple` OCR service releases its owned
  NSData, image handler, and request, and drains temporary objects in an autorelease
  pool. Cleanup alone did not eliminate `CRImageReaderError` from accurate
  recognition. A failed accurate request now retries with the fast recognizer.
  These changes were committed separately in the sibling checkout as
  `91f013bb6` and are included in the published dependency revision `4c51c1664813`.
- Login resumes Setup Assistant navigation when the guest still has setup pages.
  Cached-password login uses OCR to identify the login page, focus Enter Password,
  and confirm Finder. It no longer assumes an OCR-provisioned VM has a guest agent
  or types into a fixed location based on a pixel-only desktop classification.
- Unknown pages get a second OCR pass over an enlarged menu-bar crop. The fast
  recognizer missed Finder's small menu text in full-screen captures.
- The host provisioning overlay fades when automation returns, including failure;
  it no longer relies only on agent connection or a timeout.

## Validation

Unattended disk provisioning was staged without root, then applied with `sudo` in
an iTerm2 split. Boot with `-unattended` reached Finder. Agent execution confirmed
`id cove-test` returned UID 502 and membership in admin, and `/dev/console` belonged
to `cove-test`. A subsequent ordinary cold boot, without provisioning flags, again
logged in as `cove-test` (UID 502).

A fixed full-resolution screenshot passed 250 successive OCR requests.
The final clean OCR run (`ocr-end-to-end.log`) completed without manual input:
account creation, Apple Account skip, terms, age, location/time zone, analytics,
Screen Time, Siri, dictation opt-out, FileVault, appearance, updates, then Finder.
It reported Provisioning Complete. A window capture (`ocr-success.png`) confirms
the host overlay disappeared. In guest Terminal, `id` confirmed `cove-test` UID 501
and admin membership; `/dev/console` belonged to `cove-test` UID 501
(`ocr-user-proof.png`). Earlier diagnostic runs included manual clicks and are not
the basis for the end-to-end result.

An ordinary cold boot of the OCR clone accepted the cached password without a
guest agent. Guest Terminal confirmed `cove-test` UID 501 owned `/dev/console`
again, and Finder was running (`ocr-cold-user-proof.png`). The menu bar was absent
until another app was activated, so OCR did not report login completion: the
watcher reached its three-minute bound and released the overlay. This remains a
limitation of cold-login completion detection, not of the clean setup run.

Both test VMs are stopped. The default is running at its clean hello screen with
8 CPUs and 32 GiB RAM (`default-final.png`).

`go build ./...` passed. `go test ./...` compiled after updating the renamed private
display constructor in its test, but trapped in `TestPrivateAPI_NameGetSet` while
calling `_name` on a stopped VM. `go test ./... -skip '^TestPrivateAPI'` passed, including the final run after
the cached-login changes (`verified-suite.log`).
Targeted account-field, page detection, coordinate mapping, and backend restoration
tests passed. The OCR dependency package builds (`go test ./x/vzkit/ocr`; no tests).

Artifacts and protected diagnostic logs are in:

```
/tmp/cove-setup-debug-20260906/
```

The signed binary is `cove` in that directory. Verbose input logs may contain the
throwaway test password; do not publish them unchanged. No binary is staged.
