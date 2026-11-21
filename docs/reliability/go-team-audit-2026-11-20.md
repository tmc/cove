# Cove Go-team audit, November 20, 2026

The simulated NotebookLM panel reviewed release tooling, the public Go-run
path, and worker and storage code at `9306e9a7` through six lenses. Local
source inspection and tests verified the fixes in the audited history
range `9306e9a7..86fd590d`.

- `2e7a707d` disables workspace inheritance when Cove builds the guest agent
  from a versioned module. Checkout builds and explicit source overrides can
  still use a workspace.
- `86fd590d` validates cached manifest content against its digest on write
  and load. Garbage collection uses the checked load when marking blobs.

The code review also flagged the exit behavior of late command help.
Both `cove run -h` and `cove install -h` printed command-specific help and
exited successfully in local tests. The worker heartbeat and host record
use distinct wire messages. The controller reads the heartbeat capacity
and writes it to the nested field in its response. The private controller
module remains a separate checkout-only build.

The history panel recommended a fast-forward with both behavior changes
in separate commits. Git confirmed both cited hashes and disjoint file
sets. Build, vet, and the full Go suite passed at the reviewed tip.
