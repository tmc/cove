# Fleet Image Transfer

Fleet image transfer copies local cove image tar streams over SSH between
registered hosts. It does not use a registry.

## Commands

Push a local image to a remote host:

```sh
cove fleet image push agentkit/linux-base:latest studio
```

Pull an image from a remote host into the local image store:

```sh
cove fleet image pull agentkit/linux-base:latest studio
```

Copy an image between two remote hosts through the local machine:

```sh
cove fleet image sync agentkit/linux-base:latest studio mini
```

## Placement

Start a VM on the host with the fewest running VMs:

```sh
cove fleet run --policy=least-loaded -linux -headless -vm ubuntu
```

The policy is opt-in. Plain `cove run` and named `--fleet=<host>` routing keep
their existing behavior.
