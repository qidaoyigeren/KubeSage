# KubeSage SLO

KubeSage is designed as a production-cautious RCA assistant. The service should prefer explainable, auditable diagnosis over unsafe automation.

## User-Facing Objectives

| Objective | Target | Measurement |
| --- | --- | --- |
| Diagnosis latency | 95% of successful diagnoses finish within 30 seconds | `diagnosis_tasks.created_at` to `finished_at`, surfaced as dashboard P95 |
| Diagnosis success rate | 99% of accepted diagnosis tasks reach `success` or a controlled `failed` state | Dashboard success rate |
| Tool resilience | A failed optional tool does not stop the main diagnosis chain | Agent step status and missing evidence |
| Remediation safety | 100% of high-risk actions are proposal-only | Remediation execution status |
| Queue recovery | 100% of exhausted queue retries are written to the dead letter queue | `diagnosis_queue_dead_letters` |

## Error Budget Policy

If P95 diagnosis latency is above 30 seconds for a rolling 7-day window, prioritize reducing slow tool calls, Kubernetes API timeouts, and LLM planner retries before adding new diagnostic features.

If tool failure rate exceeds 5%, keep diagnosis running with degraded confidence and add missing evidence to the report. Optional observability tools such as Prometheus and Loki must return explicit missing evidence when they are not configured.

If LLM planner failures exceed 2%, fall back to rule-based plans and runbook retrieval. The report should say which evidence sources were missing or degraded.

## Operational Signals

The dashboard tracks:

- Diagnosis success rate and P95 diagnosis latency.
- Agent tool calls, failures, and failure rate.
- LLM planner call status, latency, token volume, and estimated cost.
- Dead letter queue size for failed diagnosis jobs.

Every diagnosis, feedback event, runbook mutation, remediation approval, and dead-letter retry is written to the audit log.

## Remediation Safety

Low-risk read-only `kubectl get`, `describe`, `logs`, `top`, and `auth can-i` previews can be marked as dry-run success because no shell execution occurs.

Low-risk mutating actions must include `--dry-run=server` to be marked as dry-run success.

Medium-risk actions require operator approval.

High-risk actions remain proposal-only.

Forbidden commands, such as namespace deletion, PVC/PV deletion, secret deletion, destructive database operations, and scaling core workloads to zero, are blocked by policy.
