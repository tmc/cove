# Fleet Quickstart

Slice 1 lets one cove CLI register another Mac host and run selected remote
commands over SSH.

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

Remove a remote:

```sh
cove fleet rm studio
```

## Limits

Fleet Slice 1 is SSH-routed remote control. It does not schedule VMs, replicate
state, start a run on the least busy host, push images across hosts, or manage
host health. Those are deferred to later slices.

Use `--fleet` only with commands that inspect or control an existing remote
host. VM creation and placement remain explicit per host.
