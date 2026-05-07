# Coved Observability

`coved` exposes host-local observability. Metrics use a localhost-only listener
by default. The web UI is preview/status-only and opt-in; set `ui_addr` to
enable it.

```toml
[daemon]
metrics_addr = "127.0.0.1:9876"
# ui_addr = "127.0.0.1:9877"

[daemon.webhook]
url = "https://example.com/cove-events"
events = ["lifecycle.policy.stop", "image.gc.run"]
```

## Prometheus

Scrape the metrics endpoint:

```yaml
scrape_configs:
  - job_name: coved
    static_configs:
      - targets: ["127.0.0.1:9876"]
```

Useful series include `coved_uptime_seconds`, `coved_vms_managed`,
`coved_lifecycle_enforced_total`, `coved_image_gc_runs_total`, and
`coved_image_gc_bytes_freed_total`.

Without Prometheus:

```sh
cove daemon metrics
cove daemon metrics --json
```

## Fleet Metrics

Aggregate registered hosts:

```sh
cove fleet metrics
cove fleet metrics --json
```

The fleet command asks each remote host for `cove daemon metrics --json` through
the existing SSH fleet route and returns partial results when a host is
unreachable.

## Web UI

Enable the local web UI by setting `daemon.ui_addr` in `~/.vz/cove.toml`, then
open it:

```sh
cove daemon ui
```

The page fetches `/metrics`, `/api/status`, and `/api/events` every five seconds.
Use `http://127.0.0.1:9877/?mode=fleet` alongside `cove fleet metrics --json`
for the matching fleet view.

The UI is an operator status surface. It is not the VM control plane and should
not replace `cove ctl`, per-VM control sockets, or the native AppKit VM window.

## Event Bus

Daemon loops publish in-process events to an internal event bus. Subscribers
write the existing `~/.vz/metrics.jsonl` shape, update Prometheus event counters,
and optionally deliver webhooks. Webhooks are best-effort: non-2xx responses
retry and then log a warning. Slow subscribers use buffered channels and do not
block lifecycle or image-GC loops.

## Idle Behavior

The daemon runs one lifecycle scan immediately, then every 30 seconds, and image
GC runs hourly after startup. On an idle host with no VMs, expected steady-state
work is one directory scan per lifecycle interval plus sleeping HTTP listeners.
For release signoff, measure with:

```sh
/usr/bin/time -lp sleep 60
ps -o pid,pcpu,rss,command -p "$(cat ~/.vz/cove.pid)"
```
