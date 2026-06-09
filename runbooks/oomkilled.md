---
fault_type: OOMKilled
checks:
  - container.lastState.terminated.reason
  - container.state.terminated.exitCode
  - container.resources.requests.memory
  - container.resources.limits.memory
  - pod events for oom/killed keywords
  - previous container logs before OOM
  - prometheus container_memory_working_set_bytes
recommended_tools:
  - kubectl_logs
  - kubectl_describe
  - prometheus_query
remediation_candidates:
  - increase memory limit
  - fix memory leak
  - add memory alerting
  - optimize batch query size
risk_policy:
  increase_memory_limit: high
  restart_pod: medium
stop_conditions:
  - memory limit already at node allocatable
  - repeated OOM after limit increase
---

# OOMKilled Runbook

## 故障现象

容器被 kubelet 杀死，`lastState.terminated.reason=OOMKilled`，或退出码为 `137`。
`kubectl get pod` 显示 `CrashLoopBackOff` 或 `OOMKilled`，`restartCount` 持续增加。

## 常见原因

- **memory limit 设置过低**：容器实际内存使用超出 limits.memory 配置。
- **应用内存泄漏**：Go heap、JVM 堆、Python 对象等持续增长不释放。
- **缓存无上限**：进程内缓存（如 map、LRU）未设大小限制，随请求量增长。
- **批量操作内存峰值**：大批量查询、反序列化、大对象处理导致瞬时内存尖峰。
- **JVM / Go runtime 参数不合理**：JVM Xmx 与 container limit 不匹配，Go GOMEMLIMIT 未设置。
- **Sidecar 容器 OOM**：istio-proxy、log-agent 等 sidecar 内存超限导致整个 Pod 被杀。

## 子模式分析

系统通过 Prometheus 内存曲线细分为以下模式，有助于精准定位根因：

| 子模式 | 含义 | 典型表现 |
|--------|------|----------|
| `memory_limit_too_low` | limit 配置过低 | 内存使用接近 limit 但无增长趋势 |
| `application_memory_leak` | 内存泄漏 | 内存持续上升，重启后短暂下降再上升 |
| `sustained_near_limit` | 持续接近 limit | 长期在 90%+ 运行，偶发突增即 OOM |
| `near_limit_spike` | 临近 limit 的突发 | 基线正常，某次请求/批处理导致尖峰 |
| `no_limit_pressure` | 无 limit 压力 | 实际使用远低于 limit，可能是 cgroup 统计问题 |
| `insufficient_shape` | 数据不足 | Prometheus 数据点不够，无法判断模式 |

## 推荐排查步骤

1. **确认 OOM 事实**：查看 `lastState.terminated.reason=OOMKilled` 和 `exitCode=137`。
2. **查看 OOM 前日志**：`kubectl logs <pod> -c <container> --previous --tail=200`，关注 OOM 前的业务行为。
3. **核对资源配额**：对比 `resources.requests.memory` 和 `resources.limits.memory`。
4. **查询内存曲线**：接入 Prometheus 后查询 `container_memory_working_set_bytes`，判断内存趋势。
5. **排查内存泄漏**：
   - Go 应用：检查 pprof heap profile，关注 `runtime.mallocgc` 和大对象分配。
   - Java 应用：检查 `-Xmx` 是否与 container limit 匹配，查看 GC 日志。
   - Python 应用：检查是否有未关闭的连接或全局缓存。
6. **对比变更和流量**：对比最近发布、流量变化、批处理任务和缓存命中情况。
7. **检查 sidecar 容器**：确认 istio-proxy、log-agent 等 sidecar 是否也有 OOM 记录。

## 处理建议

### 短期缓解

- 临时提高 memory limit 可以缓解故障，但只能作为临时方案。
- 如果是流量突增导致，可考虑临时扩容副本数分摊内存压力。

### 根因修复

| 根因 | 修复方案 |
|------|----------|
| limit 过低 | 根据实际使用量合理设置 limit，预留 20-30% buffer |
| 内存泄漏 | 修复代码中的泄漏点，Go 应用设置 `GOMEMLIMIT` |
| 缓存无上限 | 为缓存设置最大条目数或 TTL 淘汰策略 |
| 批量操作 | 限制单次查询条数，改为分页或流式处理 |
| JVM 不匹配 | 确保 `-Xmx` < container limit，预留 25-30% 给非堆内存 |
| Sidecar OOM | 为 sidecar 单独设置合理的 resource limits |

### 监控补充

- 为核心服务补充内存告警（建议阈值：limit 的 80%）。
- 建立内存使用压测基线，发布前对比内存回归。

## 风险提示

- 直接提高 limit 可能导致节点资源被挤压，影响同节点其他 Pod，风险等级通常为 **high**。
- 需要人工确认和容量评估后才能操作。
- 生产环境修改 resource limits 前建议先在预发环境验证。

## 常用命令

```bash
# 查看 OOM 详情
kubectl describe pod <pod> | grep -A5 "Last State"

# 查看 OOM 前日志
kubectl logs <pod> -c <container> --previous --tail=200

# 查看容器内存使用
kubectl top pod <pod> --containers

# 查询 Prometheus 内存曲线（需要 Prometheus 接入）
# container_memory_working_set_bytes{pod="<pod>", container="<container>"}
```
