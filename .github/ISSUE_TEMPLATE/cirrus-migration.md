---
name: Cirrus migration
about: Track a repository moving a Cirrus task to cove
title: "cirrus migration: "
labels: ["cirrus-migration"]
assignees: ""
---

## Current Cirrus task

- Repository:
- `.cirrus.yml` path:
- Task name:
- Task shape: container / macOS task / persistent_worker / matrix / other

## Target cove job

- Runner host:
- Cove image ref:
- Scheduler: GitHub Actions / Buildkite / other
- Workflow path:

## Inputs to preserve

- Images:
- Environment variables:
- Secrets names only, not values:
- Artifacts:
- Cache paths or cache keys:
- Scheduled triggers:

## Cutover evidence

- Old Cirrus task result:
- Cove run id:
- `metrics.jsonl` path:
- Artifact comparison:
- Soak period:

## Gaps or blockers

- Public registry needed: yes / no
- Hosted cron replacement needed: yes / no
- Long-term artifact storage needed: yes / no
- Other:
