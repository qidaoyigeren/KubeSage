# Pod Pending Runbook

## 故障现象

Pod 长时间处于 `Pending`，`PodScheduled=False`，Events 中出现 `FailedScheduling`。

## 常见原因

- 节点 CPU 或内存不足。
- nodeSelector 或 affinity 与节点标签不匹配。
- 节点存在 Pod 未容忍的 taint。
- PVC 未绑定或 StorageClass 异常。

## 推荐排查步骤

1. 查看 `FailedScheduling` Event message。
2. 查看 Pod requests.cpu 和 requests.memory。
3. 核对 nodeSelector、affinity、tolerations。
4. 检查 PVC/PV/StorageClass 状态。
5. 查看节点 Ready 状态和 allocatable 资源。

## 处理建议

- `Insufficient cpu/memory`：扩容节点或调整 requests。
- `untolerated taint`：补充 tolerations 或调整节点池。
- `did not match node selector`：修正节点标签或调度约束。
- `unbound immediate PersistentVolumeClaims`：修复 PVC 绑定问题。

## 风险提示

降低 requests 可能造成运行期资源争抢，扩容节点涉及成本；风险等级通常为 medium，需要人工确认。
