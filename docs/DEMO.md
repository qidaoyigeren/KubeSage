# KubeSage Demo Script

This demo tells one clear story: reproducible Kubernetes faults feed a real tool-using Agent, and the final RCA report explains its conclusion with stable evidence refs, confidence sources, missing evidence, and safe remediation boundaries.

## 1. Start A Local Kind Cluster

```bash
kind create cluster --name kubesage-eval --config eval/kind/two-node.yaml
```

The two-node profile is required for the NodeNotReady golden case. The worker node is paused during injection and resumed during teardown.

## 2. Run Golden RCA Evaluation

```bash
go run ./cmd/rca-eval \
  --cases eval/golden \
  --out eval-results.json \
  --summary eval-results.md
```

The evaluator creates an isolated namespace per case, injects the manifest, waits for the target state, runs the Agent Runtime, and writes:

- Root cause hit rate.
- False confirmed hypothesis count.
- Average diagnosis duration.
- Per-case evidence and confidence checks.

Core cases:

- OOMKilled
- CrashLoopBackOff
- ImagePullBackOff
- Pending
- ProbeFailed
- NodeNotReady

## 3. Show One Live RCA

Pick the OOMKilled case and open the generated task in the UI.

Show the Agent timeline:

- Planner selected Kubernetes, log, metrics, runbook, and remediation tools.
- Tool calls produced structured observations.
- Missing Prometheus or Loki data is recorded as missing evidence instead of being silently treated as success.
- Hypotheses move toward confirmed or rejected states.

## 4. Show The Evidence Chain Report

In the task report, point out:

- `E1`, `E2`, and later refs are stable report evidence IDs.
- Root cause conclusions reference evidence refs directly.
- Confidence is split by source: Pod status, Events, Logs, Metrics, Topology/PVC, and Runbook.
- Missing evidence lowers confidence and is shown to the user.
- Suggested actions are separate from approval and dry-run status.

Interview phrasing:

> I am not asking an LLM to guess failures. I run reproducible Kubernetes fault injection, evaluate RCA quality against golden cases, and force every conclusion to cite evidence.

## 5. Show Production Boundaries

Open:

- Dashboard: success rate, P95 diagnosis latency, tool failure rate, LLM failure rate, token cost, and dead letters.
- Approvals: medium-risk remediation waits for operator approval.
- Dead Letter Queue: exhausted queue jobs are visible and can be retried.
- Audit Logs: diagnosis, feedback, runbook changes, approvals, and retry actions are recorded.
- Runbook page: viewers can read, admins can edit.

## 6. Cleanup

```bash
kind delete cluster --name kubesage-eval
```
