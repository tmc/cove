#!/usr/bin/env bash
set -euo pipefail

providers="${PROVIDERS:-openai anthropic gemini vertex}"
runs="${RUNS:-10}"
image="${IMAGE:-agentkit/macos-base:latest}"
out="${OUT:-bench/agent-sandbox-providers/cold-fork-results-$(date +%Y%m%d).md}"
live="${RUN_LIVE:-0}"
task="${TASK:-take one screenshot and stop}"
artifacts="${ARTIFACTS_DIR:-${out%.md}-artifacts}"

mkdir -p "$(dirname "$out")"
if [ "$live" = "1" ]; then
  mkdir -p "$artifacts"
fi

{
  echo "# Cold fork to first observable action"
  echo
  echo "- Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- Cove: $(./cove version 2>/dev/null || cove version 2>/dev/null || echo unknown)"
  echo "- Host: $(uname -a)"
  echo "- Image: \`$image\`"
  echo "- First action proxy: first screenshot/control event in replay bundle"
  echo "- Live API run: $live"
  [ "$live" = "1" ] && echo "- Run artifacts: \`$artifacts\`"
  echo
  echo "| Provider | Runs | Median fork-to-first-action s | Error rate | Notes |"
  echo "| --- | ---: | ---: | ---: | --- |"
} > "$out"

for provider in $providers; do
  run_dir="$artifacts/$provider"
  errors=0
  latencies=()
  if [ "$live" != "1" ]; then
    echo "| $provider | $runs | n/a | n/a | set RUN_LIVE=1 and provider credentials to collect |" >> "$out"
    continue
  fi
  mkdir -p "$run_dir"
  for i in $(seq 1 "$runs"); do
    start="$(python3 - <<'PY'
import time
print(time.time())
PY
)"
    log="$run_dir/$i.log"
    if ! ./cove agent-sandbox run --provider "$provider" --image "$image" --task "$task" --max-steps 3 >"$log" 2>&1; then
      errors=$((errors + 1))
      continue
    fi
    replay="$(awk '/agent-sandbox replay:/ {print $3}' "$log" | tail -1)"
    if [ -z "$replay" ] || [ ! -s "$replay/control-events.jsonl" ]; then
      errors=$((errors + 1))
      continue
    fi
    first_ts="$(python3 - "$replay/control-events.jsonl" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    for line in f:
        try:
            row=json.loads(line)
        except Exception:
            continue
        if row.get("event") or row.get("action"):
            print(row.get("ts") or row.get("timestamp") or "")
            break
PY
)"
    end="$(python3 - "$first_ts" <<'PY'
import datetime, sys, time
ts=sys.argv[1]
if not ts:
    print(time.time())
else:
    print(datetime.datetime.fromisoformat(ts.replace("Z","+00:00")).timestamp())
PY
)"
    latencies+=("$(python3 - "$start" "$end" <<'PY'
import sys
print(f"{float(sys.argv[2]) - float(sys.argv[1]):.3f}")
PY
)")
  done
  median="n/a"
  if [ "${#latencies[@]}" -gt 0 ]; then
    median="$(printf '%s\n' "${latencies[@]}" | sort -n | python3 -c 'import sys; vals=[float(x) for x in sys.stdin if x.strip()]; print("n/a" if not vals else f"{vals[len(vals)//2]:.3f}")')"
  fi
  err_rate="$(python3 - "$errors" "$runs" <<'PY'
import sys
print(f"{int(sys.argv[1]) / int(sys.argv[2]):.2f}")
PY
)"
  notes="instant provisioning metric; artifacts: $run_dir"
  echo "| $provider | $runs | $median | $err_rate | $notes |" >> "$out"
done

echo "wrote $out"
