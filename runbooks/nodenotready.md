---
fault_type: NodeNotReady
checks:
  - node.status.conditions[Ready]
  - node.status.conditions[MemoryPressure]
  - node.status.conditions[DiskPressure]
  - node.status.conditions[PIDPressure]
  - node events
  - kubelet service status
  - container runtime status
  - node network connectivity
recommended_tools:
  - kubectl_describe
  - kubectl_get_nodes
  - kubectl_get_events
remediation_candidates:
  - restart kubelet
  - restart container runtime
  - fix node network
  - clean up disk pressure
  - cordon and drain node
risk_policy:
  restart_kubelet: high
  cordon_drain: high
  restart_container_runtime: high
stop_conditions:
  - node hardware failure (requires cloud provider intervention)
  - network partition (requires network team intervention)
---

# NodeNotReady Runbook

## 故障现象

`kubectl get nodes` 显示节点状态为 `NotReady`，Pod 所在节点的 `Ready` condition 为 `False`。
节点 NotReady 会导致该节点上的 Pod 停止接收流量，且新 Pod 不会被调度到该节点。
超过 `pod-eviction-timeout`（默认 5 分钟）后，节点上的 Pod 将被驱逐。

## 常见原因

### 按节点条件分类

| 节点条件 | 含义 | 常见根因 |
|----------|------|----------|
| `Ready=False` | 节点不健康 | kubelet 停止上报、容器运行时异常、网络不可达 |
| `MemoryPressure=True` | 节点内存不足 | 节点可用内存低于阈值（默认 100Mi） |
| `DiskPressure=True` | 节点磁盘不足 | 磁盘空间或 inode 低于阈值 |
| `PIDPressure=True` | 节点 PID 不足 | 系统 PID 资源耗尽 |

### 按根因分类

| 根因 | 典型表现 | 排查方向 |
|------|----------|----------|
| kubelet 进程异常 | 节点突然 NotReady，无 pressure 条件 | 检查 kubelet 服务状态和日志 |
| 容器运行时异常 | kubelet 报 `PLEG is not healthy` | 检查 containerd/docker 服务状态 |
| 节点网络不可达 | API server 无法连接 kubelet | 检查节点网络连通性、防火墙规则 |
| 节点磁盘满 | DiskPressure=True | 检查磁盘使用、清理无用数据 |
| 节点内存满 | MemoryPressure=True | 检查内存使用、排查内存泄漏进程 |
| 节点 CPU 过载 | 节点响应极慢但未完全失联 | 检查 CPU 使用率、排查 CPU 密集进程 |
| 内核死锁/hang | 节点无响应，SSH 无法连接 | 需要重启节点或联系云厂商 |
| 硬件故障 | 节点完全不可达 | 需要云厂商介入处理 |

## 推荐排查步骤

1. **查看节点状态**：`kubectl get nodes -o wide` 确认哪些节点 NotReady。
2. **查看节点 Conditions**：`kubectl describe node <node>` 查看详细 conditions 和 last transition time。
3. **查看节点事件**：`kubectl get events --field-selector involvedObject.name=<node>`。
4. **检查 kubelet 状态**：SSH 到节点后执行 `systemctl status kubelet`。
5. **检查容器运行时**：SSH 到节点后执行 `systemctl status containerd` 或 `systemctl status docker`。
6. **检查节点资源**：
   - `free -h` 查看内存
   - `df -h` 查看磁盘空间
   - `df -i` 查看 inode
   - `top` 查看 CPU 和进程
7. **检查网络连通性**：确认节点是否可以访问 API server。
8. **查看 kubelet 日志**：`journalctl -u kubelet --since "10 minutes ago"`。

## 处理建议

### 按场景处理

| 场景 | 处理方案 |
|------|----------|
| kubelet 进程挂了 | SSH 到节点，`systemctl restart kubelet` |
| 容器运行时挂了 | SSH 到节点，`systemctl restart containerd` |
| 节点磁盘满 | 清理无用镜像、日志、临时文件 |
| 节点内存满 | 排查内存泄漏进程，必要时重启高内存进程 |
| 节点网络不可达 | 检查 VPC、安全组、路由表，联系网络团队 |
| 节点长期 NotReady | `kubectl cordon <node>` 标记不可调度，`kubectl drain <node>` 迁移工作负载 |
| 硬件/内核故障 | 在云控制台重启节点实例或联系云厂商 |

### 节点维护操作

```bash
# 标记节点为不可调度（不影响现有 Pod）
kubectl cordon <node>

# 迁移节点上的所有 Pod（需要先 cordon）
kubectl drain <node> --ignore-daemonsets --delete-emptydir-data --force

# 节点恢复后取消不可调度标记
kubectl uncordon <node>
```

### kubelet 恢复

```bash
# SSH 到节点后

# 检查 kubelet 状态
systemctl status kubelet

# 重启 kubelet
systemctl restart kubelet

# 查看 kubelet 日志
journalctl -u kubelet -f --since "5 minutes ago"

# 检查容器运行时
systemctl status containerd
systemctl restart containerd
```

### 节点磁盘清理

```bash
# SSH 到节点后

# 清理无用容器镜像
crictl rmi --prune

# 查看大文件
du -sh /var/log/* | sort -rh | head -10
du -sh /var/lib/docker/* | sort -rh | head -10

# 清理旧日志
journalctl --vacuum-time=3d

# 清理临时文件
rm -rf /tmp/*
```

## 风险提示

- 重启 kubelet 会导致节点上所有 Pod 短暂不可用，风险等级为 **high**。
- 重启容器运行时会导致所有容器重建，影响面更大。
- `kubectl drain` 会驱逐节点上的 Pod，可能导致服务容量下降。
- 节点维护操作需要提前评估对集群容量的影响。
- 生产环境操作需保留变更记录，建议先在非生产节点验证。

## 常用命令

```bash
# 查看节点状态
kubectl get nodes -o wide

# 查看节点详细信息
kubectl describe node <node>

# 查看节点上的 Pod
kubectl get pods --field-selector spec.nodeName=<node> -o wide

# 查看节点事件
kubectl get events --field-selector involvedObject.name=<node>

# 标记节点不可调度
kubectl cordon <node>

# 迁移节点工作负载
kubectl drain <node> --ignore-daemonsets --delete-emptydir-data --force
```
