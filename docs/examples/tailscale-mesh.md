---
title: Tailscale Mesh VM
---
# Tailscale Mesh VM

Use cove to bring a fresh macOS VM onto your Tailscale tailnet at first boot. The result is a headless dev box reachable from any other tailnet node by stable hostname, with no port forwarding, no public IP, and Tailscale SSH wired up by default. Useful as a persistent build sandbox, a mesh-routed CI runner, or a remote desktop you can reach from anywhere you are logged in to your tailnet.

## Prerequisites

- A Tailscale account and a reusable auth key from [https://login.tailscale.com/admin/settings/keys](https://login.tailscale.com/admin/settings/keys).
- cove built and signed -- see the [Quick Start](../getting-started/quickstart.md).
- The `homebrew` and `tailscale` vzscripts (shipped in-tree, no extra setup).

## 1. Provide the Auth Key

The `tailscale` vzscript reads three host environment variables and forwards them to the guest:

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `TS_AUTHKEY` | yes | -- | Reusable or single-use auth key. Empty value falls back to a browser login URL printed by the guest. |
| `TS_HOSTNAME` | no | `$(hostname -s)-cove` inside the guest | The name your node shows up under in the admin console. |
| `TS_TAGS` | no | -- | Comma-separated tags, e.g. `tag:cove-vm,tag:dev`. Requires the auth key to permit those tags. |

```bash
export TS_AUTHKEY=tskey-auth-...
export TS_HOSTNAME=cove-dev
export TS_TAGS=tag:cove-vm
```

## 2. Install, Provision, and Join

`cove up` installs macOS, provisions the user, boots the VM, and runs the requested vzscripts. Pass `homebrew,tailscale` so the recipe's `homebrew` dependency is built first:

```bash
cove up -user me -password 'changeme' -vzscripts homebrew,tailscale
```

The first run takes the usual macOS install time (~5 minutes) plus a homebrew install. Subsequent runs against the same VM are fast: `tailscale` is idempotent and skips re-`up` if `BackendState` is already `Running`.

## 3. Verify Tailnet Membership

From the host (or any tailnet peer):

```bash
tailscale status | grep cove-dev
```

You should see the VM listed with an IPv4 address in your tailnet's CGNAT range and a `100.x.x.x` Tailscale IP.

## 4. SSH from Anywhere

The vzscript brings the node up with `--ssh`, so [Tailscale SSH](https://tailscale.com/kb/1193/tailscale-ssh) is enabled by default. From any tailnet peer:

```bash
tailscale ssh me@cove-dev
```

If your tailnet has [MagicDNS](https://tailscale.com/kb/1081/magicdns) enabled, plain `ssh` works too:

```bash
ssh me@cove-dev.tailnet-name.ts.net
```

## 5. Cleanup

Removing the VM:

```bash
cove vm delete cove-dev
```

The Tailscale node will appear offline in the admin console within a few minutes. Either delete it from [https://login.tailscale.com/admin/machines](https://login.tailscale.com/admin/machines) or let it auto-expire per the auth key's TTL.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Node never appears in admin console | Auth key wrong, expired, or out of uses | Mint a fresh key, re-run with `cove vzscript run -vm <vm> tailscale`. |
| `cove-dev.tailnet-name.ts.net` does not resolve | MagicDNS is off for the tailnet | Enable it in [DNS settings](https://login.tailscale.com/admin/dns), or use the raw `100.x.x.x` IP. |
| `tailscale status` errors with "permission denied" inside the VM | `tailscaled` runs as root via LaunchDaemon; user must use `sudo tailscale status` or be in the `admin` group | Provisioned user is in `admin`; rerun after a fresh login or use `sudo`. |
| vzscript exits early with "no admin user found" | Provisioning did not complete before the script ran | Use `cove up` (which orders provision before vzscripts). The `tailscale` recipe declares `runs-on: daemon` so it runs as root via the daemon agent -- verify with `head vzscripts/tailscale.vzscript`. |

## See also

- [macOS CI Runner](ci-runner.md) -- pair a tailnet-joined VM with snapshot rollback for a long-lived headless runner reachable by hostname.
- [`vzscripts/tailscale.vzscript`](../../vzscripts/tailscale.vzscript) -- the recipe driving this guide.
- [`tailscale up` flag reference](https://tailscale.com/kb/1080/cli) -- full set of CLI options if you want to fork the recipe.
