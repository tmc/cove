# cove vs Cirrus benchmark

Cirrus Labs announced Cirrus CI shuts down on 2026-06-01. This benchmark keeps
the comparison protocol for teams still able to run a Cirrus/Tart-backed task
during the migration window.

The comparable workload is:

1. start a clean runner image;
2. run `uname -a` and a small shell workload;
3. collect logs;
4. tear the runner down.

If the Cirrus CLI or hosted service is unavailable, record the cell as
`not measured`. Do not fabricate hosted-service numbers after shutdown.
