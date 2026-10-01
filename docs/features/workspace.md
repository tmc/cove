# Go workspaces

`cove workspace` composes a prepared guest, explicit repository access, tool
checks, and a task run with an inspectable result. It supports macOS and Linux
with a logged-in user agent. It does not install or replace an existing guest.
Prepare an appropriate base with `cove up` first.

```sh
mkdir -p "$HOME/tmp/go-workspace-output"
cove workspace plan -vm go-task -from prepared-go-base \
  -source ./my-repository -output "$HOME/tmp/go-workspace-output"
cove workspace open -vm go-task -from prepared-go-base \
  -source ./my-repository -output "$HOME/tmp/go-workspace-output" \
  -- go test ./...
```

The base must be stopped with its disk closed. The first `open` creates a fresh
fork using the existing fork API and starts it headlessly. Reuse the retained
guest by omitting `-from` on the next invocation. An existing target combined
with `-from`, an OS mismatch, or a conflicting saved share fails explicitly.
There is no forced reinstall, automatic cold restart, or task retry.

Source access defaults to `-source-mode ro`. `-source-mode rw` explicitly permits
the task to write source. Output must be a separate directory, with no ancestor
relationship to the source, including through symlinks. Go build/module caches,
temporary directories, and binary outputs point at this writable share.
`COVE_WORKSPACE_OUTPUT` identifies it for custom tasks. `GOFLAGS=-mod=readonly`
prevents the default Go task from updating module metadata. Task arguments are
passed as positional parameters rather than concatenated shell text.

The profile independently checks root execution, user-session execution, guest
and output capacity, the user Go version, and mounted shares. Root readiness
does not substitute for the user route. A reachable user agent does not prove
the desktop is unlocked; the first profile runs noninteractive Go tasks. The
minimum capacity is one GiB by default and can be raised with `-min-free-gib`.
It is an advisory check under concurrent allocation and does not delete files.

`-prepare golang` opts into prerequisite recipes if the current user Go check
fails. Dependencies are planned with `vzscript plan`, their source bytes are retained
privately for the invocation, run in dependency order,
and followed by another Go check. A past success record never replaces this
check. Templates must be rendered before use. Preparation recipes with extra mount/injection directives must be applied to
the base with existing `up`/`vzscript` routes first. Preparation uses existing recipe
execution; it does not introduce a separate provisioning interpreter.

Each invocation writes a shared run bundle with step events, readiness and Go
version evidence, task exit status, and bounded stdout/stderr artifacts. Git
HEAD and dirty-state observations are captured before and after task dispatch;
they are labeled as observations, not immutable source snapshots. Non-Git or
unavailable source identity remains unknown. No image digest is inferred from
a base name. Task arguments and environment values are omitted from plan and
provenance records. Task output is captured as supplied by the task, so avoid
printing credentials. Each output artifact is capped at two MiB and truncation
is recorded in the task result. Task output is streamed live. A stream must include a final exit status; EOF
without that status leaves the result unknown and retains its partial evidence.
The task timeout defaults to ten minutes and cannot exceed the existing server
execution limit of ten minutes.

`-json` writes a machine-readable receipt to stdout and task/progress output to
stderr. Use `cove runs show RUN_ID --summary-json` to inspect the shared bundle.
The receipt identifies the guest directory, failed step, result bundle, and
retained/discarded disposition. Failure capture has its own five-second budget
and cannot replace the task error.

Guests are retained by default. `-retain discard-success` can delete only a
fresh fork created by this invocation after a successful task and graceful
stop; it is refused for an existing workspace. Failures and unknown results
retain the guest. Cancellation or timeout ends host observation and does not
prove guest command termination. Inspect the retained guest before retrying
any command whose result was not received. Owned guest cleanup failures remain
separate from task outcomes and report the retained location.

Fixture tests cover planning, conflicts, prerequisite failures, checked reruns,
argument/environment handling, retention, and result separation. Physical
fresh/prepared guest qualification remains a separate release gate.
