# RCA Golden Evaluation

KubeSage evaluates RCA quality with replayable golden fixtures instead of asking the LLM to guess from a blank prompt. Each fixture is a serialized `DiagnosticContext`, so CI can run the full rule engine and scorer without a live Kubernetes cluster.

## Run Offline Eval

```bash
go run ./cmd/rca-eval run -cases eval/cases -out eval-results.json -summary eval-summary.md
```

The command emits:

- `eval-results.json`: machine-readable case results and aggregate metrics.
- `eval-summary.md`: human-readable accuracy, hallucination, safety, and latency summary.

The default quality gate requires 100% case pass rate, 0 hallucinations, 0 overconfidence failures, 0 dangerous suggestions, and every case under its duration threshold.

## Compare Two Runs

```bash
go run ./cmd/rca-eval compare -out eval-comparison.md before.json after.json
```

## Generate A Fixture From A Live Pod

```bash
go run ./cmd/rca-eval gen-fixture \
  -namespace demo \
  -pod oom-demo \
  -expected-fault-type OOMKilled \
  -out eval/fixtures/oom-demo.json
```

Use `-kubeconfig` when the default kubeconfig is not the target cluster. Fixture generation is read-only: it collects Pod, Events, Logs, PVC, topology, node snapshots, and optional metric context.

## Case Contract

Each YAML case contains:

- `id`
- `fixture`
- `tags`
- `max_duration_seconds`
- `golden_answer.expected_fault_type`
- `golden_answer.root_cause`
- `golden_answer.key_evidences`
- `golden_answer.should_not_contain`
- `golden_answer.acceptable_remediations`
- `golden_answer.confidence_min`

The default suite currently covers 55 cases:

- `core.yaml`: 15 hand-written cases covering OOMKilled x4, CrashLoopBackOff x4, PodPending x3, ImagePullBackOff x2, and ProbeFailed x2.
- `fault-bank.yaml`: 40 generated cases covering OOMKilled, CrashLoopBackOff, PodPending, ImagePullBackOff, ProbeFailed, InitError, Evicted, and NodeNotReady.

Regenerate the fault bank after editing scenarios:

```bash
go run ./scripts/generate_eval_fault_bank.go
```

The generator also writes live-cluster demo manifests to `demo/fault-bank`:

```bash
kubectl apply -k demo/fault-bank
kubectl delete -k demo/fault-bank --ignore-not-found
```
