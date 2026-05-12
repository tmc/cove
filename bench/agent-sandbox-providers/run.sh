#!/usr/bin/env bash
set -euo pipefail

providers="${PROVIDERS:-openai anthropic gemini vertex}"
runs="${RUNS:-10}"
image="${IMAGE:-agentkit/macos-base:latest}"
task="${TASK:-take a screenshot, click button at coords, type hello, take another screenshot}"
out="${OUT:-bench/agent-sandbox-providers/results-$(date +%Y%m%d).md}"
live="${RUN_LIVE:-0}"
artifacts="${ARTIFACTS_DIR:-${out%.md}-artifacts}"

mkdir -p "$(dirname "$out")"
if [ "$live" = "1" ]; then
  mkdir -p "$artifacts"
fi

{
  echo "# Agent sandbox provider latency"
  echo
  echo "- Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- Cove: $(./cove version 2>/dev/null || cove version 2>/dev/null || echo unknown)"
  echo "- Host: $(uname -a)"
  echo "- Image: \`$image\`"
  echo "- Task: \`$task\`"
  echo "- Runs per provider: $runs"
  echo "- Live API run: $live"
  [ "$live" = "1" ] && echo "- Run artifacts: \`$artifacts\`"
  echo
  echo "| Provider | Model | Runs | Median latency s | Error rate | Notes |"
  echo "| --- | --- | ---: | ---: | ---: | --- |"
} > "$out"

for provider in $providers; do
  model_var="COVE_$(printf '%s' "$provider" | tr '[:lower:]' '[:upper:]')_MODEL"
  model="${!model_var:-default}"
  run_dir="$artifacts/$provider"
  errors=0
  latencies=()
  if [ "$live" != "1" ]; then
    echo "| $provider | $model | $runs | n/a | n/a | set RUN_LIVE=1 and provider credentials to collect |" >> "$out"
    continue
  fi
  mkdir -p "$run_dir"
  for i in $(seq 1 "$runs"); do
    start="$(python3 - <<'PY'
import time
print(time.time())
PY
)"
    if ! ./cove agent-sandbox run --provider "$provider" --image "$image" --task "$task" --max-steps 8 >"$run_dir/$i.out" 2>"$run_dir/$i.err"; then
      errors=$((errors + 1))
    fi
    end="$(python3 - <<'PY'
import time
print(time.time())
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
  notes="mechanical latency only; artifacts: $run_dir"
  echo "| $provider | $model | $runs | $median | $err_rate | $notes |" >> "$out"
done

echo "wrote $out"
