import { Card, Progress, Tag, Space, Typography, Empty } from 'antd';
import {
  CheckCircleOutlined,
  ExperimentOutlined,
  CloseCircleOutlined,
} from '@ant-design/icons';
import type { Hypothesis, HypothesisStatus } from '../../../api/types';
import { confidencePercent } from '../../../utils/format';

const statusConfig: Record<HypothesisStatus, { color: string; label: string; icon: React.ReactNode }> = {
  confirmed: { color: 'success', label: '已确认', icon: <CheckCircleOutlined /> },
  active: { color: 'processing', label: '活跃', icon: <ExperimentOutlined /> },
  rejected: { color: 'default', label: '已拒绝', icon: <CloseCircleOutlined /> },
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
                  {h.hypothesis_type}
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
              {h.summary}
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
                  拒绝原因：{h.rejected_reason}
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
