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
- NodeNotReady cannot be safely forced by namespaced Pod manifests alone. The NodeNotReady entries are placeholders with matching names and synthetic Events; use the offline fixtures for deterministic NodeNotReady scoring, or reproduce node health failures only in a disposable cluster.
- Several scenarios intentionally use invalid images, impossible requests, failing probes, and tight resource limits. Apply this only to a dev or disposable cluster.

List scenario pods:

```bash
kubectl get pods -n eval-bank -l kubesage.io/fault-bank=true
```
