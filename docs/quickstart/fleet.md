# Fleet Quickstart

Fleet lets one cove CLI register another Mac host, run selected remote commands
over SSH, aggregate state across hosts, and copy images directly between hosts.

## Setup

On host B, make sure cove is installed, SSH is enabled, and the VM you want to
control is already running:

```sh
cove -vm ubuntu run -linux -headless
```

On host A, register host B:

```sh
cove fleet add studio tmc@mac-studio.local -vm ubuntu
```

List registered remotes:

```sh
cove fleet ls
```

Run a remote control command:

```sh
cove --fleet=studio ctl gui status
```

List remote VMs:

```sh
cove --fleet=studio vm list
```

List remote images:

```sh
cove --fleet=studio image list
```

Aggregate VMs and images across all registered hosts:

```sh
cove fleet vm ls
cove fleet image ls
cove fleet ps
```

Copy a local image to a remote host, pull it back, or sync it between remotes:

```sh
cove fleet image push agentkit/linux-base:latest studio
cove fleet image pull agentkit/linux-base:latest studio
cove fleet image sync agentkit/linux-base:latest studio mini
```

Start a VM on the host with the fewest running VMs:

```sh
cove fleet run --policy=least-loaded -linux -headless -vm ubuntu
```

Remove a remote:

```sh
cove fleet rm studio
```

## Limits

Fleet image transfer is direct host-to-host tar streaming through SSH. It does
not use a registry, content-addressed deduplication across hosts, or public
image references.

Use `--fleet` only with commands that inspect or control an existing remote
host. Least-loaded placement is opt-in through `cove fleet run
--policy=least-loaded`; default `cove run` behavior is unchanged.
