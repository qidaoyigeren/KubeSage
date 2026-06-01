import { Card, Progress, Tag, Space, Typography, Empty } from 'antd';
import type { ReactNode } from 'react';
import {
  CheckCircleOutlined,
  ExperimentOutlined,
  CloseCircleOutlined,
} from '@ant-design/icons';
import type { Hypothesis, HypothesisStatus } from '../../../api/types';
import { confidencePercent } from '../../../utils/format';

const statusConfig: Record<HypothesisStatus, { color: string; label: string; icon: ReactNode }> = {
  confirmed: { color: 'success', label: '已确认', icon: <CheckCircleOutlined /> },
  active: { color: 'processing', label: '分析中', icon: <ExperimentOutlined /> },
  rejected: { color: 'default', label: '已排除', icon: <CloseCircleOutlined /> },
};

const hypothesisName: Record<string, string> = {
  memory_limit_too_low: '内存限制过低',
  application_memory_leak: '应用内存泄漏',
  node_memory_pressure: '节点内存压力',
  bad_config: '应用配置错误',
  missing_secret_or_configmap: 'Secret/ConfigMap 缺失',
  dependency_unavailable: '下游依赖不可用',
  probe_misconfigured: '健康检查配置错误',
  pvc_unbound: 'PVC 未绑定',
  scheduling_constraint: '调度约束阻塞',
  image_pull_failed: '镜像拉取失败',
  init_container_crash: 'Init 容器失败',
  node_eviction: '节点驱逐',
  node_not_ready: '节点 NotReady',
};

const hypothesisSummary: Record<string, string> = {
  memory_limit_too_low: '容器内存 limit 可能低于实际负载，导致 OOM 或频繁重启。',
  application_memory_leak: '应用内存可能持续增长，最终触发容器被杀或重启。',
  node_memory_pressure: '节点存在内存压力，可能影响该 Pod 的稳定性。',
  bad_config: '应用启动失败可能与配置文件、环境变量或启动参数错误有关。',
  missing_secret_or_configmap: 'Pod 引用的 Secret 或 ConfigMap 可能不存在或内容不完整。',
  dependency_unavailable: '启动或健康检查期间依赖的下游服务可能不可用。',
  probe_misconfigured: 'Readiness/Liveness 探针配置可能与应用实际监听不一致。',
  pvc_unbound: 'PVC 未绑定可能阻塞调度或启动。',
  scheduling_constraint: '调度约束、资源不足、亲和性或污点容忍可能导致 Pod 无法运行。',
  image_pull_failed: '镜像名称、标签、仓库权限或镜像拉取凭据可能存在问题。',
  init_container_crash: 'Init 容器失败会阻止业务容器启动，常见原因是依赖或配置错误。',
  node_eviction: 'Pod 可能因节点资源压力被 kubelet 驱逐。',
  node_not_ready: 'Pod 所在节点处于 NotReady，可能导致服务不可用或调度异常。',
};

const HypothesesTab = ({ hypotheses }: { hypotheses: Hypothesis[] }) => {
  if (!hypotheses || hypotheses.length === 0) {
    return <Empty description="暂无假设数据" />;
  }

  const sorted = [...hypotheses].sort((a, b) => b.confidence_score - a.confidence_score);

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {sorted.map((h) => {
        const pct = confidencePercent(h.confidence_score);
        const color = pct >= 70 ? '#52c41a' : pct >= 40 ? '#faad14' : '#f5576c';
        const st = statusConfig[h.status];
        const displayName = hypothesisName[h.hypothesis_type] || h.hypothesis_type;
        const displaySummary = hypothesisSummary[h.hypothesis_type] || h.summary;

        return (
          <Card
            key={h.id}
            className={`hypothesis-card status-${h.status}`}
            size="small"
            bordered={false}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 12 }}>
              <Space>
                <Typography.Text strong style={{ fontSize: 15 }}>
                  {displayName}
                </Typography.Text>
                <Tag color={st.color} icon={st.icon} style={{ borderRadius: 12 }}>
                  {st.label}
                </Tag>
              </Space>
              <div style={{ textAlign: 'right' }}>
                <Progress
                  percent={pct}
                  size="small"
                  strokeColor={color}
                  style={{ width: 100 }}
                  format={(p) => `${p}%`}
                />
              </div>
            </div>

            <Typography.Paragraph style={{ margin: '0 0 12px', color: '#4e5969', lineHeight: 1.7 }}>
              {displaySummary}
            </Typography.Paragraph>

            {h.supporting_evidence_refs && h.supporting_evidence_refs.length > 0 && (
              <div style={{ marginBottom: 8 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 4 }}>
                  支持证据
                </Typography.Text>
                <Space size={[4, 4]} wrap>
                  {h.supporting_evidence_refs.map((ref, i) => (
                    <Tag key={i} color="green" style={{ borderRadius: 4, fontSize: 12 }}>
                      {ref}
                    </Tag>
                  ))}
                </Space>
              </div>
            )}

            {h.contradicting_evidence_refs && h.contradicting_evidence_refs.length > 0 && (
              <div style={{ marginBottom: 8 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 4 }}>
                  反驳证据
                </Typography.Text>
                <Space size={[4, 4]} wrap>
                  {h.contradicting_evidence_refs.map((ref, i) => (
                    <Tag key={i} color="red" style={{ borderRadius: 4, fontSize: 12 }}>
                      {ref}
                    </Tag>
                  ))}
                </Space>
              </div>
            )}

            {h.missing_evidence && h.missing_evidence.length > 0 && (
              <div style={{ marginBottom: 8 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 4 }}>
                  缺失证据
                </Typography.Text>
                <Space size={[4, 4]} wrap>
                  {h.missing_evidence.map((ref, i) => (
                    <Tag key={i} style={{ borderRadius: 4, fontSize: 12 }}>
                      {ref}
                    </Tag>
                  ))}
                </Space>
              </div>
            )}

            {h.rejected_reason && (
              <div style={{ marginTop: 8, padding: '8px 12px', background: '#fff2f0', borderRadius: 6, border: '1px solid #ffccc7' }}>
                <Typography.Text type="danger" style={{ fontSize: 13 }}>
                  排除原因：{h.rejected_reason}
                </Typography.Text>
              </div>
            )}
          </Card>
        );
      })}
    </Space>
  );
};

export default HypothesesTab;
