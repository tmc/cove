# cove forward

`cove forward <vm> <hostport>:<vmport>` listens on `localhost:<hostport>` and
forwards each TCP connection to `127.0.0.1:<vmport>` inside the running guest.

Slice 1 starts a guest `vz-agent -relay` process and then uses the existing
control-socket port-forward manager:

```
cove forward dev 8080:80
```

This avoids an agent protocol bump. The forward is TCP-only, host-to-guest
only, IPv4 localhost only, and uses a derived guest vsock relay port. Reverse
forwards, UDP, IPv6, explicit relay-port selection, and a native agent
`DialTCP` RPC are deferred.
