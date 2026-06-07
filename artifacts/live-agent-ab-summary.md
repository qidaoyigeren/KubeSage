# Live Agent A/B Evaluation

- Generated: 2026-06-07T16:40:05+08:00
- Namespace: `eval-bank`
- Cases: `eval/cases/core.yaml`

## Methodology

- All evidence is collected from the live Kubernetes cluster; fixture files are not loaded.
- Evaluation labels under kubesage.io are removed before diagnosis.
- Rule and Direct LLM arms receive the same full live snapshot.
- Agent receives only namespace and pod name, then selects registered read-only tools through Plan-Execute-Reflect.
- Prometheus and Loki usage is counted only when live samples or log bytes are returned.

## Summary

| Arm | Strict RCA | Fault type | Root cause | Evidence | Remediation | Safety | Avg latency | P95 latency | Avg tokens | LLM calls |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| rule_baseline | 60.0% | 100.0% | 80.0% | 66.7% | 100.0% | 100.0% | 99ms | 212ms | 0 | 0 |
| direct_llm | 0.0% | 53.3% | 66.7% | 0.0% | 6.7% | 100.0% | 3373ms | 5595ms | 3955 | 15 |
| agent | 46.7% | 100.0% | 73.3% | 73.3% | 100.0% | 100.0% | 16715ms | 24734ms | 16261 | 113 |

## Business Delta

- Agent vs Direct LLM fault-type accuracy: +46.7 percentage points
- Agent vs Direct LLM strict RCA: +46.7 percentage points
- Agent vs rule strict RCA: -13.3 percentage points
- Agent tokens: 243925 total, 16261 average per case
- Agent tool calls: 72 total, 0 failed
- Live source coverage: Prometheus samples in 11/15 cases, Loki logs in 10/15 cases
- Agent tool coverage: Prometheus called in 4 cases, Loki used directly or through log enrichment in 10 cases

## Cases

| Case | Expected | Rule | Direct LLM | Agent | Direct tokens | Agent tokens | Agent tools |
| --- | --- | --- | --- | --- | ---: | ---: | --- |
| `oom-limit-too-low` | `OOMKilled` | PASS `OOMKilled` | PARTIAL `OOMKilled` | PASS `OOMKilled` | 3861 | 21432 | k8s.get_pod, k8s.get_logs, prometheus.query_range, k8s.get_topology, runbook.search, remediation.generate_actions |
| `oom-java-heap` | `OOMKilled` | PASS `OOMKilled` | PARTIAL `OOMKilled` | PASS `OOMKilled` | 3858 | 14980 | k8s.get_pod, k8s.get_logs, prometheus.query_range, k8s.get_topology, remediation.generate_actions |
| `oom-sudden-spike` | `OOMKilled` | PARTIAL `OOMKilled` | PARTIAL `OOMKilled` | PARTIAL `OOMKilled` | 3818 | 15508 | k8s.get_pod, k8s.get_logs, prometheus.query_range, k8s.get_topology, remediation.generate_actions |
| `oom-no-metrics` | `OOMKilled` | PASS `OOMKilled` | PARTIAL `OOMKilled` | PASS `OOMKilled` | 3838 | 21229 | k8s.get_pod, k8s.get_topology, k8s.get_logs, prometheus.query_range, runbook.search, remediation.generate_actions |
| `crashloop-missing-config` | `CrashLoopBackOff` | PASS `CrashLoopBackOff` | PARTIAL `CrashLoopBackOff` | PASS `CrashLoopBackOff` | 3782 | 15129 | k8s.get_pod, k8s.get_events, k8s.get_logs, k8s.get_topology, remediation.generate_actions |
| `crashloop-connection-refused` | `CrashLoopBackOff` | PASS `CrashLoopBackOff` | FAIL `application_crash_loop_backoff` | PARTIAL `CrashLoopBackOff` | 3817 | 14786 | k8s.get_pod, k8s.get_events, k8s.get_logs, k8s.get_topology, remediation.generate_actions |
| `crashloop-permission-denied` | `CrashLoopBackOff` | PASS `CrashLoopBackOff` | PARTIAL `CrashLoopBackOff` | PASS `CrashLoopBackOff` | 3880 | 14880 | k8s.get_pod, k8s.get_events, k8s.get_logs, k8s.get_topology, remediation.generate_actions |
| `crashloop-panic` | `CrashLoopBackOff` | PASS `CrashLoopBackOff` | FAIL `application_crash_loop_backoff` | PASS `CrashLoopBackOff` | 3831 | 14651 | k8s.get_pod, k8s.get_events, k8s.get_logs, k8s.get_topology, remediation.generate_actions |
| `pending-insufficient-cpu` | `PodPending` | PASS `PodPending` | FAIL `resource_insufficiency` | PARTIAL `PodPending` | 2574 | 14089 | k8s.get_pod, k8s.get_events, k8s.get_topology, remediation.generate_actions |
| `pending-unbound-pvc` | `PodPending` | PARTIAL `PodPending` | FAIL `PersistentVolumeClaimUnschedulable` | PARTIAL `PodPending` | 2609 | 13691 | k8s.get_pod, k8s.get_events, k8s.get_pvc, k8s.get_topology, remediation.generate_actions |
| `pending-untolerated-taint` | `PodPending` | PARTIAL `PodPending` | FAIL `unschedulable_pod` | PARTIAL `PodPending` | 2626 | 13897 | k8s.get_pod, k8s.get_events, k8s.get_topology, remediation.generate_actions |
| `imagepull-tag-not-found` | `ImagePullBackOff` | PARTIAL `ImagePullBackOff` | PARTIAL `ImagePullBackOff` | PARTIAL `ImagePullBackOff` | 3718 | 14153 | k8s.get_pod, k8s.get_events, remediation.generate_actions |
| `imagepull-unauthorized` | `ImagePullBackOff` | PARTIAL `ImagePullBackOff` | PARTIAL `ImagePullBackOff` | PARTIAL `ImagePullBackOff` | 3897 | 14530 | k8s.get_pod, k8s.get_events, remediation.generate_actions |
| `probe-readiness-404` | `ProbeFailed` | PARTIAL `CrashLoopBackOff,ProbeFailed` | FAIL `misconfigured_readiness_probe` | PARTIAL `CrashLoopBackOff,ProbeFailed` | 5120 | 14854 | k8s.get_pod, k8s.get_events, k8s.get_logs, k8s.get_topology, remediation.generate_actions |
| `probe-liveness-timeout` | `ProbeFailed` | PASS `ProbeFailed` | FAIL `liveness_probe_timeout` | PASS `ProbeFailed` | 8096 | 26116 | k8s.get_pod, k8s.get_events, k8s.get_logs, k8s.get_topology, runbook.search, remediation.generate_actions |

## Resume Evidence

搭建真实 Kind 故障集三路在线 A/B 评测，15 个案例统一从 Kubernetes、Prometheus、Loki 实时采证并过滤答案标签；Agent 相比 Direct LLM 将故障类型准确率从 53.3% 提升至 100.0%、幻觉率从 20.0% 降至 0.0%，但平均 token 从 3955 增至 16261、平均延迟从 3373ms 增至 16715ms；结合规则基线 60.0% 严格 RCA / 99ms 延迟，验证规则快路径 + Agent 低置信慢路径的生产策略。
