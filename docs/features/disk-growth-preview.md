# Disk growth preview

`cove disk resize-plan [-json] <vm> <size>` reads the primary raw disk image without attaching it, running host tools, contacting the VM or creating transaction state. It validates the protective MBR, primary and backup GPT header/table CRCs, matching disk identities and entries, sector bounds, partition identities and overlaps. Inspection supports 512 and 4096 byte sectors with at most 128 entries of at most 1024 bytes each. Other image formats and hybrid MBRs are refused.

```sh
cove disk resize-plan -json disposable-macos 128G
```

A validated GPT inventory is not filesystem, encryption, stopped ownership or boot validation. The candidate macOS layout is ISC, APFS and trailing APFS Recovery. Other layouts appear with an explicit unsupported-layout blocker. A backup GPT left before the end of an already enlarged image is reported rather than silently repaired. JSON always reports `mutation_available: false`.

Recovery-preserving offline growth is **not implemented**. It needs an exclusive stopped/attached-device gate, verified backup, durable phase journal, byte-preserving Recovery relocation and checksum, updates to both GPT copies, bounded host APFS growth, rollback at every phase, and physical normal and Recovery boot receipts. This preview does not satisfy those gates and does not relocate any partition.

Keep the existing live APFS Recovery refusal. Do not delete or recreate Recovery to bypass it. Preserve the original image and a verified cold checkpoint before any future operation. If a backing-image resize already succeeded but APFS expansion failed, retain the image: enlarging it again does not remove trailing Recovery. Pending checkpoint restore requires `cove checkpoint recover` before mutation. There is no offline disk-growth transaction to recover in this implementation. Until that path is qualified, use a larger separately provisioned guest and verified data migration.

The existing `cove disk resize` backing-image growth and guest-agent `cove ctl -vm <vm> disk resize 0 <size>` behavior remain available under their existing safeguards. The preview introduces no bypass for those safeguards.
