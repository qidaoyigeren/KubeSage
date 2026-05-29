# OOMKilled Runbook

## 故障现象

容器被 kubelet 杀死，`lastState.terminated.reason=OOMKilled`，或退出码为 `137`。

## 常见原因

- memory limit 设置过低。
- 应用内存泄漏或缓存无上限。
- 批量查询、批量反序列化、大对象处理导致瞬时内存峰值。
- JVM、Go heap 或运行时参数不合理。

## 推荐排查步骤

1. 查看容器 memory request 和 memory limit。
2. 查看 previous logs 中 OOM 前的业务行为。
3. 接入 Prometheus 后查询容器内存曲线。
4. 对比最近发布、流量变化、批处理任务和缓存命中情况。

## 处理建议

- 临时提高 memory limit 可以缓解故障，但只能作为临时方案。
- 检查内存泄漏、大对象缓存、批量查询、JVM/Go heap。
- 为核心服务补充内存告警和压测基线。

## 风险提示

直接提高 limit 可能导致节点资源被挤压，风险等级通常为 high，需要人工确认和容量评估。
