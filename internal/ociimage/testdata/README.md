# testdata for ociimage

## tart_disk_layer_prefix.bin

First 64 KiB of the first disk layer of `cirruslabs/macos-sequoia-vanilla:latest`
as published on `ghcr.io`. Captured 2026-04-25.

The layer's full size is 3,812,730 bytes (compressed) covering 536,870,912
bytes (512 MiB) uncompressed. We only freeze a 64 KiB Range prefix because
that's enough to validate Apple-LZ4 framing without committing megabytes of
binary into the repo.

The fixture is intentionally truncated mid-stream: tests should expect the
final block to be incomplete and stop iterating there, NOT decode an
end-of-stream marker.

### To refresh

```
TOKEN=$(curl -s "https://ghcr.io/token?service=ghcr.io&scope=repository:cirruslabs/macos-sequoia-vanilla:pull" | jq -r .token)

# 1. Fetch the manifest to find the first disk layer's digest:
curl -sH "Authorization: Bearer $TOKEN" \
     -H "Accept: application/vnd.oci.image.manifest.v1+json" \
     https://ghcr.io/v2/cirruslabs/macos-sequoia-vanilla/manifests/latest \
  | jq '.layers[] | select(.mediaType == "application/vnd.cirruslabs.tart.disk.v2") | .digest' \
  | head -1

# 2. Range-fetch the first 64 KiB:
DIGEST=sha256:061db4500522298fc97ef69015d6b919868187625be21f24f652e2f0be507929
curl -sLH "Authorization: Bearer $TOKEN" \
     -H "Range: bytes=0-65535" \
     https://ghcr.io/v2/cirruslabs/macos-sequoia-vanilla/blobs/$DIGEST \
  -o tart_disk_layer_prefix.bin
```

The digest above will rotate when cirruslabs rebuilds the image. Re-run
step 1 to find the current first-layer digest.
