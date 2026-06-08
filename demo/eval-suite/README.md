# KubeSage Eval Suite Demo

This directory contains 59 generated Kubernetes demo scenarios that mirror the full offline eval suite:

- eval/cases/core.yaml
- eval/cases/fault-bank.yaml
- eval/cases/supplementary.yaml

Apply:

```bash
kubectl apply -k demo/eval-suite
```

Watch:

```bash
kubectl get pods -n eval-bank -w
kubectl get events -n eval-bank --sort-by=.lastTimestamp
```

Delete:

```bash
kubectl delete -k demo/eval-suite --ignore-not-found
```

Notes:

- This is the live-cluster companion for the complete 59-case offline suite.
- NodeNotReady cases use dedicated synthetic Node API objects. After applying the manifests, run the status patch commands printed by the preparation script so their real Kubernetes Node status becomes Ready=False.
- Several scenarios intentionally use invalid images, impossible requests, failing probes, and tight resource limits. Apply this only to a dev or disposable cluster.

List scenario pods:

```bash
kubectl get pods -n eval-bank -l kubesage.io/fault-bank=true
```
