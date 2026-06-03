import { Card, Tag, Typography, Space, Collapse, Empty } from 'antd';
import {
  MedicineBoxOutlined,
  WarningOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ClockCircleOutlined,
  StopOutlined,
} from '@ant-design/icons';
import RiskLevelTag from '../../../components/RiskLevelTag';
import JsonViewer from '../../../components/JsonViewer';
import type {
  RemediationAction,
  RemediationExecution,
  RemediationStatus,
} from '../../../api/types';

const execStatusConfig: Record<RemediationStatus, { color: string; label: string; icon: React.ReactNode }> = {
  proposed: { color: 'blue', label: '已提议', icon: <ClockCircleOutlined /> },
  blocked: { color: 'red', label: '已阻止', icon: <StopOutlined /> },
  dry_run_pending: { color: 'orange', label: 'Dry Run 等待中', icon: <ClockCircleOutlined /> },
  dry_run_success: { color: 'green', label: 'Dry Run 成功', icon: <CheckCircleOutlined /> },
  dry_run_failed: { color: 'red', label: 'Dry Run 失败', icon: <CloseCircleOutlined /> },
  pending_approval: { color: 'gold', label: '待审批', icon: <WarningOutlined /> },
  manual_acknowledged: { color: 'cyan', label: '人工已确认', icon: <CheckCircleOutlined /> },
};

interface RemediationTabProps {
  actions?: RemediationAction[] | null;
  executions?: RemediationExecution[] | null;
}

const RemediationTab = ({ actions, executions }: RemediationTabProps) => {
  if ((!actions || actions.length === 0) && (!executions || executions.length === 0)) {
    return <Empty description="暂无修复建议" />;
  }

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {actions && actions.length > 0 && (
        <Card
          className="report-section-card"
          title={
            <Space>
              <MedicineBoxOutlined style={{ color: '#52c41a' }} />
              <span>修复建议</span>
            </Space>
          }
          bordered={false}
        >
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            {actions.map((a, i) => (
              <Card
                key={i}
                className="remediation-card"
                type="inner"
                size="small"
                bordered={false}
              >
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 8 }}>
                  <Space wrap>
                    <Typography.Text strong style={{ fontSize: 14 }}>{a.action_id}</Typography.Text>
                    <Tag color="blue" style={{ borderRadius: 4 }}>{a.action_type}</Tag>
                    <RiskLevelTag level={a.risk_level} />
                    {a.need_human_confirm && (
                      <Tag color="orange" icon={<WarningOutlined />} style={{ borderRadius: 4 }}>
                        需人工确认
                      </Tag>
                    )}
                  </Space>
                </div>
                <Typography.Paragraph style={{ margin: '0 0 8px', color: '#4e5969' }}>
                  {a.description}
                </Typography.Paragraph>
                {a.command_preview && (
                  <pre
                    style={{
                      background: '#1e1e2e',
                      color: '#cdd6f4',
                      padding: '10px 14px',
                      borderRadius: 6,
                      margin: 0,
                      fontSize: 12,
                      fontFamily: "'SF Mono', 'Fira Code', Menlo, Consolas, monospace",
                    }}
                  >
                    $ {a.command_preview}
                  </pre>
                )}
              </Card>
            ))}
          </Space>
        </Card>
      )}

      {executions && executions.length > 0 && (
        <Card
          className="report-section-card"
          title={
            <Space>
              <MedicineBoxOutlined style={{ color: '#722ed1' }} />
              <span>修复执行记录</span>
            </Space>
          }
          bordered={false}
        >
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            {executions.map((e) => {
              const st = execStatusConfig[e.status];
              return (
                <Card
                  key={e.id}
                  className="remediation-card"
                  type="inner"
                  size="small"
                  bordered={false}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 8 }}>
                    <Space wrap>
                      <Typography.Text strong>{e.action_id}</Typography.Text>
                      <Tag
                        color={st?.color}
                        icon={st?.icon}
                        style={{ borderRadius: 4 }}
                      >
                        {st?.label || e.status}
                      </Tag>
                      <RiskLevelTag level={e.risk_level} />
                    </Space>
                    {e.approval_by && (
                      <Tag style={{ borderRadius: 4 }}>审批人: {e.approval_by}</Tag>
                    )}
                  </div>

                  {e.command_preview && (
                    <pre
                      style={{
                        background: '#1e1e2e',
                        color: '#cdd6f4',
                        padding: '10px 14px',
                        borderRadius: 6,
                        margin: '0 0 8px',
                        fontSize: 12,
                        fontFamily: "'SF Mono', 'Fira Code', Menlo, Consolas, monospace",
                      }}
                    >
                      $ {e.command_preview}
                    </pre>
                  )}

                  {e.dry_run_output && (
                    <Collapse
                      size="small"
                      items={[
                        {
                          key: 'dryrun',
                          label: e.status === 'manual_acknowledged' ? '处理说明' : 'Dry Run 输出',
                          children: <JsonViewer data={e.dry_run_output} maxHeight={200} />,
                        },
                      ]}
                    />
                  )}
                </Card>
              );
            })}
          </Space>
        </Card>
      )}
    </Space>
  );
};

export default RemediationTab;
