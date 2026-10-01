# Guest network-volume permissions

macOS may ask whether `vz-agent` can access files on a network volume when
it first reads a shared folder. Approving that prompt grants this service
inside the guest; it does not change the host folder's read/write mode.

To let Cove approve this specific prompt on future boots and shared-folder
changes for a VM you own:

```sh
cove doctor tcc-network-volumes -vm myvm -enable
```

The VM GUI must be open and the guest user agent connected. Cove matches the
`vz-agent` network-volume prompt and its exact Allow/Don’t Allow buttons in the
same captured frame before clicking the normalized Allow center. Missing or
ambiguous controls leave the prompt for manual approval. A prompt that remains
after a click produces one failure report and is not clicked repeatedly. Other applications
and permission categories require separate approval. The preference lives
in the VM bundle and is disabled by default. An already running older Cove
binary needs to be restarted before it can monitor prompts.

```sh
cove doctor tcc-network-volumes -vm myvm          # show preference
cove doctor tcc-network-volumes -vm myvm -disable # stop future approvals
```

Disabling this preference does not revoke a grant macOS already retained.
Manage existing grants in the guest's System Settings. Cove does not modify
the TCC database or grant Full Disk Access through this setting. UI recognition
currently requires the English network-volume prompt; localized prompts need
manual approval.

Apple documents both the [first-access consent prompt](https://developer.apple.com/documentation/bundleresources/information-property-list/nsnetworkvolumesusagedescription)
and [managed privacy permission policies](https://support.apple.com/guide/deployment/privacy-preferences-policy-control-payload-dep38df53c2a/web).
A managed policy requires the corresponding supported device-management setup;
this preference uses macOS's consent UI instead.
