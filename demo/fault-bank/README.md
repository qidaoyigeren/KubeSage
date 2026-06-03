# KubeSage Fault Bank Demo

This directory contains 40 generated Kubernetes demo scenarios that mirror the offline fault-bank cases.

Apply:

```bash
kubectl apply -k demo/fault-bank
```

Watch:

```bash
kubectl get pods -n eval-bank -w
kubectl get events -n eval-bank --sort-by=.lastTimestamp
```

Delete:

```bash
kubectl delete -k demo/fault-bank --ignore-not-found
```

Notes:

- OOMKilled, CrashLoopBackOff, ImagePullBackOff, ProbeFailed, InitError, PodPending, and most Evicted scenarios are generated as real Pod/PVC/StorageClass resources.
- NodeNotReady placeholders include synthetic Events because Pod manifests cannot force a real Node Ready=False condition. Other scenarios rely on Kubernetes to create the natural Events.
- NodeNotReady cannot be safely forced by namespaced Pod manifests alone. The NodeNotReady entries in this kustomize demo are placeholders with matching names and synthetic Events; use the offline fixtures for deterministic NodeNotReady scoring.
- For a live NodeNotReady demo, use `demo/fault-bank/node-notready-kind.sh` against a disposable two-node kind cluster. It stops the kind worker node container so Kubernetes marks the node `Ready=False`.
- Several scenarios intentionally use invalid images, impossible requests, failing probes, and tight resource limits. Apply this only to a dev or disposable cluster.

List scenario pods:

```bash
kubectl get pods -n eval-bank -l kubesage.io/fault-bank=true
```

Live NodeNotReady demo:

```bash
demo/fault-bank/node-notready-kind.sh create
demo/fault-bank/node-notready-kind.sh inject
demo/fault-bank/node-notready-kind.sh status
demo/fault-bank/node-notready-kind.sh recover
demo/fault-bank/node-notready-kind.sh cleanup
```
