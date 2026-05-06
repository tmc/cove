# boot-to-agent benchmark

Measures elapsed time until the guest agent is reachable. The protocol records
the guest family, VM reference, command path, and timeout for each attempt.

Run:

```bash
bench/boot-to-agent/run.sh --vm macos-runner --guest macos --iterations 3
bench/boot-to-agent/run.sh --vm ubuntu-runner --guest ubuntu --iterations 3
```

The script does not install a new guest by default. Fresh install timing is a
separate long-running mode and must be run on disposable VM names.
