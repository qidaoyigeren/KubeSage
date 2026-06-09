---
fault_type: Evicted
checks:
  - pod.status.reason
  - pod.status.phase
  - pod.status.message
  - pod events for evict
  - node conditions (MemoryPressure, DiskPressure, PIDPressure)
  - node allocatable ephemeral-storage
  - container resource requests/limits
  - pod ephemeral-storage usage
recommended_tools:
  - kubectl_describe
  - kubectl_get_events
  - kubectl_top
remediation_candidates:
  - clean up disk space
  - set ephemeral-storage limits
  - reduce container log size
  - scale up node pool
  - fix memory leak
risk_policy:
  increase_ephemeral_storage: medium
  cleanup_disk: low
  scale_nodes: medium
stop_conditions:
  - node DiskPressure caused by other workloads
  - repeated eviction after resource adjustment
---

# Evicted Runbook

## 故障现象

Pod 状态为 `Failed`，`pod.status.reason=Evicted`，`pod.status.message` 包含 eviction 相关描述。
被驱逐的 Pod 已进入终态，不会自动恢复，需要由控制器（Deployment/StatefulSet）创建替代 Pod。

## 常见原因

### 按驱逐原因分类

| 驱逐原因 | 节点条件 | Events 关键词 | 含义 |
|----------|----------|---------------|------|
| 磁盘压力 | `DiskPressure=True` | `evict` + `ephemeral-storage` | 节点临时存储或磁盘空间耗尽 |
| 内存压力 | `MemoryPressure=True` | `evict` + `memory` | 节点级内存不足 |
| PID 耗尽 | `PIDPressure=True` | `evict` + `pid` | 节点 PID 资源耗尽 |
| 镜像文件系统满 | `DiskPressure=True` | `imagefs` | 容器镜像存储空间不足 |
| inode 耗尽 | `DiskPressure=True` | `inodes` | 节点 inode 资源耗尽 |

### 按常见场景分类

| 场景 | 典型原因 |
|------|----------|
| 容器日志过大 | 应用输出大量日志，占满 ephemeral-storage |
| 临时文件未清理 | 应用在 /tmp 或 emptyDir 中写入大量临时文件 |
| 镜像堆积 | 节点上积累了大量未清理的容器镜像 |
| 内存泄漏导致节点 OOM | 某个 Pod 内存泄漏导致节点整体 MemoryPressure |
| PID 泄漏 | 应用创建大量线程/进程未回收 |
| 未设置 ephemeral-storage limits | Pod 没有 ephemeral-storage limits，kubelet 无法预估使用量 |

## 推荐排查步骤

1. **确认驱逐原因**：查看 `pod.status.reason=Evicted` 和 `pod.status.message` 获取具体原因。
2. **查看 Events**：`kubectl describe pod <pod>` 获取驱逐事件详情。
3. **检查节点状态**：`kubectl describe node <node>` 查看节点 conditions（MemoryPressure、DiskPressure、PIDPressure）。
4. **查看节点资源使用**：`kubectl top node <node>` 查看 CPU 和内存使用情况。
5. **检查 ephemeral-storage**：查看 Pod 的 ephemeral-storage requests/limits 和实际使用量。
6. **检查容器日志大小**：在节点上查看 `/var/log/pods/` 和 `/var/lib/docker/containers/` 的日志大小。
7. **检查 emptyDir 使用**：确认 emptyDir 卷的实际使用量。
8. **查看节点磁盘**：`df -h` 和 `df -i` 查看磁盘空间和 inode 使用情况。

## 处理建议

### 短期缓解

| 场景 | 处理方案 |
|------|----------|
| 节点磁盘满 | 清理无用容器镜像：`crictl rmi --prune` 或 `docker system prune` |
| 日志文件过大 | 清理节点上的旧日志文件，配置日志轮转 |
| 临时文件堆积 | 清理 `/tmp` 和 emptyDir 中的临时文件 |
| 节点内存压力 | 驱逐低优先级 Pod 或扩容节点 |

### 长期修复

| 场景 | 修复方案 |
|------|----------|
| 容器日志过多 | 降低应用日志级别，配置日志轮转策略 |
| ephemeral-storage 未限制 | 为 Pod 设置 ephemeral-storage requests/limits |
| emptyDir 无上限 | 设置 emptyDir.sizeLimit 限制卷大小 |
| 内存泄漏 | 修复应用内存泄漏（参见 OOMKilled Runbook） |
| PID 泄漏 | 修复应用线程/进程泄漏 |
| 镜像堆积 | 配置 kubelet 镜像垃圾回收策略（`--image-gc-high-threshold`） |

### ephemeral-storage 配置建议

```yaml
resources:
  requests:
    ephemeral-storage: "1Gi"
  limits:
    ephemeral-storage: "2Gi"
```

### emptyDir 限制

```yaml
volumes:
  - name: tmp-volume
    emptyDir:
      sizeLimit: "500Mi"
```

## 风险提示

- 清理节点磁盘前确认不会删除正在使用的数据，风险等级为 **medium**。
- 设置 ephemeral-storage limits 可能导致正常 Pod 被误杀（如果 limit 过低）。
- 扩容节点涉及成本和时间，需评估集群配额。
- 生产环境操作需保留变更记录，建议先在预发环境验证。

## 常用命令

```bash
# 查看被驱逐的 Pod
kubectl get pods --field-selector=status.phase=Failed -o wide | grep Evicted

# 查看驱逐原因
kubectl describe pod <pod> | grep -A5 "Status\|Reason\|Message"

# 查看节点压力条件
kubectl describe node <node> | grep -A10 "Conditions"

# 查看节点磁盘使用
kubectl describe node <node> | grep -A5 "Allocated resources"

# 清理节点上的无用镜像
crictl rmi --prune
```
