# T86 live verification: cove logs and cove cp

Date: 2026-05-05
Branch: conductor/t86-verify-logs-cp
Base: e96ec12
VM: ubuntu-gh-runner-headed

## Build

Command:

```sh
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Result: pass. The binary was signed successfully.

## VM state

`ubuntu-gh-runner-headed` was stopped at the start of verification. It was started with:

```sh
./cove -vm ubuntu-gh-runner-headed run -linux -headless
```

The guest agent became available and reported:

```text
agent version: 300abe63
```

## cove logs -f

Follow command:

```sh
timeout 25 ./cove logs ubuntu-gh-runner-headed -f > /tmp/t86-logs-follow.out 2> /tmp/t86-logs-follow.err
```

Live guest log injection:

```sh
for i in 1 2 3 4 5 6; do
  ./cove -vm ubuntu-gh-runner-headed ctl agent-exec logger T86_COVE_LOGS_LIVE_R2_$i
  sleep 1
done
```

`timeout` exited with status 124, which is expected for a follow stream. `stderr` was empty. The captured stream contained 6 live markers:

```text
May 05 12:48:43 ubuntu-vm root[2145]: T86_COVE_LOGS_LIVE_R2_1
May 05 12:48:45 ubuntu-vm root[2148]: T86_COVE_LOGS_LIVE_R2_2
May 05 12:48:47 ubuntu-vm root[2153]: T86_COVE_LOGS_LIVE_R2_3
May 05 12:48:50 ubuntu-vm root[2156]: T86_COVE_LOGS_LIVE_R2_4
May 05 12:48:51 ubuntu-vm root[2157]: T86_COVE_LOGS_LIVE_R2_5
May 05 12:48:53 ubuntu-vm root[2160]: T86_COVE_LOGS_LIVE_R2_6
```

Result: pass. `cove logs <vm> -f` followed Linux journal output and received more than five live tail lines.

## cove cp host -> guest -> host

Commands:

```sh
printf 'T86 cove cp round trip %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > /tmp/host-test.txt
./cove cp /tmp/host-test.txt ubuntu-gh-runner-headed:/tmp/vm-test.txt
./cove cp ubuntu-gh-runner-headed:/tmp/vm-test.txt /tmp/host-test-roundtrip.txt
shasum -a 256 /tmp/host-test.txt /tmp/host-test-roundtrip.txt
cmp -s /tmp/host-test.txt /tmp/host-test-roundtrip.txt
```

Output:

```text
host sha:      6a03bb3cbac6ed02b78e05c79abbb70946a8811f1829f4fd810de0d71e6c4b57  /tmp/host-test.txt
roundtrip sha: 6a03bb3cbac6ed02b78e05c79abbb70946a8811f1829f4fd810de0d71e6c4b57  /tmp/host-test-roundtrip.txt
roundtrip cmp: ok
```

Result: pass. The round-trip SHA-256 hashes match.

## cove cp guest -> host

Command:

```sh
./cove cp ubuntu-gh-runner-headed:/etc/os-release /tmp/vm-os-release.txt
```

Copied file excerpt:

```text
PRETTY_NAME="Ubuntu 24.04.3 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION="24.04.3 LTS (Noble Numbat)"
VERSION_CODENAME=noble
ID=ubuntu
```

Result: pass. Reverse copy from guest to host produced Ubuntu 24.04.3 LTS `/etc/os-release` content.

## Bugs filed

None. No fix required.
