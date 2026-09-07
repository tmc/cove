# IPSW download stall handling

The current checkout used curl for both terminal and GUI IPSW downloads. Both
paths could wait indefinitely, required a manual restart to resume, and duplicated
their progress loops. The GUI path also treated any file larger than 10 GiB as
complete, even without a ZIP end record.

Both paths now share one context-aware downloader. Each attempt has a 15-second
connection bound and aborts sustained transfer rates below 1 KiB/s for 30 seconds.
Interrupted transfers and transient HTTP failures retry up to four total attempts,
with a one-second delay. Each new curl process uses `--continue-at -`, calculating
its Range request from the bytes currently saved on disk. Cancellation stops and
reaps the process; failed transfers retain their partial file. HTTP errors are not
written into the image. Permanent failures, including unsupported Range requests,
return without retrying or deleting saved data.

The HEAD request is also bounded to 15 seconds and honors cancellation. GUI and
terminal downloads require the existing minimum-size and ZIP-end-record checks
before reporting completion. There is no longer a size-only GUI shortcut.

## Validation

Local HTTP server tests used the installed curl binary and verified:

- A response sends 65,536 bytes, then stalls. The retry sends
  `Range: bytes=65536-` and produces a byte-for-byte identical complete payload.
- HTTP 503 retries are bounded; HTTP 404 is not retried.
- A server ignoring Range cannot append a full response to the partial file.
- Cancellation interrupts both a body transfer and a stalled HEAD request.
- A sparse 11 GiB partial IPSW still triggers a Range request and is not reported
  complete. The existing too-small-file test continues to pass.

`go build ./...` and `go test ./... -skip '^TestPrivateAPI_NameGetSet$'` passed.
The excluded native diagnostic remains unchanged and is documented in the P0
report. The cached 18 GiB IPSW was not downloaded again or modified.

Builds, the signed final binary, test logs, and a final agent round-trip are under
`/tmp/cove-p3-20260906/`. The default still reports console owner cove (UID 501)
and a running Finder through its guest agent.
