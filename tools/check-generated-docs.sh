#!/bin/sh
# Snapshot gate for generated documentation pages.
#
# Proves the committed generated pages match a fresh run of their generators.
# Regenerate, stage, and diff happen in ONE invocation: on a machine where
# other sessions churn the workspace, a gap between regenerating and diffing
# produces spurious failures.
#
# The generators are in the diff list too — an edited generator with a stale
# snapshot is the same lie as a hand-edited page.
#
# Usage: sh tools/check-generated-docs.sh
# Exit 0 and "SNAPSHOT-CLEAN" means the committed output is current.
set -eu

cd "$(dirname "$0")/.."

GENERATED='docs/reliability/test-home-audit.md'
GENERATORS='tools/audit-test-home.sh tools/check-generated-docs.sh'

sh tools/audit-test-home.sh . > docs/reliability/test-home-audit.md \
  && git add -- $GENERATED $GENERATORS \
  && git diff --exit-code HEAD -- $GENERATED $GENERATORS > /dev/null \
  && echo SNAPSHOT-CLEAN
