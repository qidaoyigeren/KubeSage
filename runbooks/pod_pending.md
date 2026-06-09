---
fault_type: PodPending
checks:
  - pod.status.phase
  - pod.status.conditions[PodScheduled]
  - events for FailedScheduling
  - pod.spec.resources.requests
  - nodeSelector/affinity/tolerations
  - PVC/PV/StorageClass status
  - node allocatable resources
recommended_tools:
  - kubectl_describe
  - kubectl_get_nodes
  - kubectl_get_pvc
remediation_candidates:
  - scale up node pool
  - adjust resource requests
  - fix node selector/affinity
  - fix PVC binding
  - add tolerations
risk_policy:
  scale_nodes: medium
  reduce_requests: high
  modify_scheduling: medium
stop_conditions:
  - PVC pending due to storage class provisioner failure
  - cluster autoscaler already enabled but max nodes reached
---

# Pod Pending Runbook

## 故障现象

Pod 长时间处于 `Pending` 状态，`kubectl get pod` 显示 `Pending`，`PodScheduled=False`，Events 中出现 `FailedScheduling`。

## 常见原因

### 按调度失败原因分类

| 调度失败原因 | 含义 | Events 关键词 |
|-------------|------|---------------|
| CPU 不足 | 集群可用 CPU 无法满足 requests | `Insufficient cpu` |
| 内存不足 | 集群可用内存无法满足 requests | `Insufficient memory` |
| 污点不容忍 | 节点有 taint 但 Pod 没有对应 toleration | `untolerated taint` |
| 节点选择器不匹配 | nodeSelector/affinity 与节点标签不匹配 | `did not match node selector` |
| PVC 未绑定 | PersistentVolumeClaim 尚未绑定到 PV | `unbound immediate PersistentVolumeClaims` |
| GPU/特殊资源不足 | 集群没有足够的 GPU 或自定义资源 | `Insufficient <resource>` |
| 节点不可调度 | 所有节点都被标记为 unschedulable | `0/N nodes are available` |

### 按 PVC 问题分类

| PVC 状态 | 含义 | 常见根因 |
|----------|------|----------|
| Pending | PV 未创建 | StorageClass provisioner 异常、存储后端容量不足 |
| Lost | PV 丢失 | 手动删除了 PV 但 PVC 仍引用 |
| 等待 WaitForFirstConsumer | 等待 Pod 调度后才创建 PV | 正常行为，需先解决 Pod 调度问题 |

## 推荐排查步骤

1. **查看 FailedScheduling 事件**：`kubectl describe pod <pod>` 中的 Events 部分，获取具体失败原因。
2. **查看资源请求**：检查 `requests.cpu` 和 `requests.memory`，与集群可用资源对比。
3. **核对调度约束**：检查 `nodeSelector`、`affinity`、`tolerations` 配置。
4. **检查 PVC 状态**：`kubectl get pvc` 确认 PVC 是否 Bound。
5. **查看节点状态**：`kubectl get nodes -o wide` 确认节点 Ready 状态。
6. **查看节点资源**：`kubectl describe node <node>` 查看 Allocatable 和已分配资源。
7. **检查 StorageClass**：`kubectl get sc` 确认 StorageClass 和 provisioner 状态。

## 处理建议

### 按场景处理

| 场景 | 处理方案 |
|------|----------|
| `Insufficient cpu/memory` | 扩容节点池或降低 Pod 的 resource requests |
| `untolerated taint` | 为 Pod 补充 tolerations 或调整节点池 taint 配置 |
| `did not match node selector` | 修正 Pod 的 nodeSelector/affinity 或修改节点标签 |
| `unbound PersistentVolumeClaims` | 检查 StorageClass provisioner 状态，确认存储后端可用 |
| GPU/特殊资源不足 | 确认集群是否有 GPU 节点，检查 nvidia-device-plugin 状态 |
| 节点不可调度 | 检查是否有节点被 `kubectl cordon` 标记为不可调度 |

### 资源 requests 调整建议

```bash
# 查看集群资源使用情况
kubectl top nodes
kubectl describe nodes | grep -A5 "Allocated resources"

# 查看 Pending Pod 的资源请求
kubectl get pod <pod> -o jsonpath='{.spec.containers[*].resources.requests}'
```

### PVC 问题排查

```bash
# 查看 PVC 状态
kubectl get pvc -n <namespace>

# 查看 PV 状态
kubectl get pv

# 查看 StorageClass
kubectl get sc

# 查看 provisioner 日志（CSI driver）
kubectl logs -n kube-system -l app=<csi-driver>
```

## 风险提示

- 降低 requests 可能造成运行期资源争抢，导致 OOMKilled 或 CPU throttling，风险等级为 **high**。
- 扩容节点涉及成本和时间，需评估集群配额。
- 修改调度约束可能导致 Pod 被调度到不合适的节点。
- 生产环境操作需保留变更记录，建议先在预发环境验证。

## 常用命令

```bash
# 查看 Pending 原因
kubectl describe pod <pod> | grep -A10 "Events"

# 查看集群节点资源
kubectl top nodes
kubectl describe node <node> | grep -A10 "Allocated"

# 查看 PVC 状态
kubectl get pvc -n <namespace>

# 查看 Pod 调度约束
kubectl get pod <pod> -o jsonpath='{.spec}' | jq '.nodeSelector, .affinity, .tolerations'
```
