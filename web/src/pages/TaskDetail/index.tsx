import { useParams, useNavigate } from 'react-router-dom';
import {
  Card,
  Tabs,
  Descriptions,
  Spin,
  Result,
  Button,
  Space,
  Tag,
  Typography,
  Progress,
} from 'antd';
import {
  ArrowLeftOutlined,
  ReloadOutlined,
  ClockCircleOutlined,
  CheckCircleOutlined,
  SyncOutlined,
  CloseCircleOutlined,
  MinusCircleOutlined,
} from '@ant-design/icons';
import { useTask } from '../../hooks/useTask';
import StatusTag from '../../components/StatusTag';
import ConfidenceBar from '../../components/ConfidenceBar';
import RiskLevelTag from '../../components/RiskLevelTag';
import { formatTime, formatTaskDuration } from '../../utils/format';
import ReportTab from './tabs/ReportTab';
import EvidenceTab from './tabs/EvidenceTab';
import AgentTimelineTab from './tabs/AgentTimelineTab';
import HypothesesTab from './tabs/HypothesesTab';
import RemediationTab from './tabs/RemediationTab';
import type { TaskStatus } from '../../api/types';

const statusIcon: Record<TaskStatus, React.ReactNode> = {
  pending: <MinusCircleOutlined style={{ fontSize: 20 }} />,
  running: <SyncOutlined spin style={{ fontSize: 20 }} />,
  success: <CheckCircleOutlined style={{ fontSize: 20 }} />,
  failed: <CloseCircleOutlined style={{ fontSize: 20 }} />,
};

const statusBg: Record<TaskStatus, string> = {
  pending: '#f0f0f0',
  running: 'linear-gradient(135deg, #e6f7ff 0%, #bae7ff 100%)',
  success: 'linear-gradient(135deg, #f6ffed 0%, #d9f7be 100%)',
  failed: 'linear-gradient(135deg, #fff2f0 0%, #ffccc7 100%)',
};

const TaskDetail = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const taskId = Number(id);
  const { data: task, isLoading, error, refetch, isRefetching } = useTask(taskId);

  if (isLoading) {
    return (
      <div style={{ textAlign: 'center', padding: 120 }}>
        <Spin size="large" />
        <div style={{ marginTop: 16 }}>
          <Typography.Text type="secondary">加载任务详情...</Typography.Text>
        </div>
      </div>
    );
  }

  if (error || !task) {
    return (
      <Result
        status="404"
        title="任务未找到"
        subTitle={error?.message || `任务 ID ${id} 不存在`}
        extra={
          <Button type="primary" onClick={() => navigate('/tasks')}>
            返回任务列表
          </Button>
        }
      />
    );
  }

  const report = task.report;
  const evidences = task.evidences || report?.evidences || [];
  const timeline = report?.agent_timeline || [];
  const hypotheses = report?.hypotheses || [];
  const remediationExecutions = report?.remediation_executions || [];
  const remediationActions = report?.remediation_actions;
  const snapshot = report?.agent_report_snapshot;

  const tabItems = [
    {
      key: 'report',
      label: '诊断报告',
      children: report ? <ReportTab report={report} /> : <div>暂无报告</div>,
    },
    {
      key: 'evidence',
      label: `证据`,
      badge: evidences.length,
      children: <EvidenceTab evidences={evidences} />,
    },
    {
      key: 'timeline',
      label: `Agent 时间线`,
      badge: timeline.length,
      children: <AgentTimelineTab steps={timeline} />,
    },
    {
      key: 'hypotheses',
      label: `假设分析`,
      badge: hypotheses.length,
      children: <HypothesesTab hypotheses={hypotheses} />,
    },
    {
      key: 'remediation',
      label: '修复建议',
      children: (
        <RemediationTab
          actions={remediationActions}
          executions={remediationExecutions}
        />
      ),
    },
  ];

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {/* Back + Refresh */}
      <Space>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/tasks')}>
          返回列表
        </Button>
        <Button
          icon={<ReloadOutlined />}
          onClick={() => refetch()}
          loading={isRefetching}
        >
          刷新
        </Button>
      </Space>

      {/* Status Header Card */}
      <Card
        className="task-detail-header"
        bordered={false}
        style={{ background: statusBg[task.status] }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
          <Space size="large" align="start">
            <div style={{ fontSize: 36, lineHeight: 1 }}>
              {statusIcon[task.status]}
            </div>
            <div>
              <Space size="middle" style={{ marginBottom: 4 }}>
                <Typography.Title level={4} style={{ margin: 0 }}>
                  {task.namespace} / {task.pod_name}
                </Typography.Title>
                <StatusTag status={task.status} />
              </Space>
              <Space size="middle" style={{ marginTop: 4 }}>
                {task.fault_type && <Tag color="geekblue">{task.fault_type}</Tag>}
                {task.alert_name && <Tag>{task.alert_name}</Tag>}
                {task.alert_severity && <Tag>{task.alert_severity}</Tag>}
                <Typography.Text type="secondary">
                  <ClockCircleOutlined style={{ marginRight: 4 }} />
                  {formatTaskDuration(task.created_at, task.finished_at)}
                </Typography.Text>
                <Typography.Text type="secondary">
                  任务 #{task.id}
                </Typography.Text>
              </Space>
            </div>
          </Space>
          <div style={{ textAlign: 'right' }}>
            {task.confidence_score > 0 && (
              <div style={{ marginBottom: 4 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>置信度</Typography.Text>
                <Progress
                  percent={Math.round(task.confidence_score * 100)}
                  size="small"
                  strokeColor={task.confidence_score >= 0.7 ? '#52c41a' : task.confidence_score >= 0.4 ? '#faad14' : '#ff4d4f'}
                  style={{ width: 120 }}
                />
              </div>
            )}
            {report?.risk_level && <RiskLevelTag level={report.risk_level} />}
          </div>
        </div>
        {task.root_cause_summary && (
          <div className="root-cause-banner">
            <Typography.Text strong style={{ color: '#389e0d' }}>根因：</Typography.Text>{' '}
            {task.root_cause_summary}
          </div>
        )}
      </Card>

      {/* Info Grid */}
      <Card bordered={false} bodyStyle={{ padding: '12px 24px' }}>
        <Descriptions column={4} size="small">
          <Descriptions.Item label="创建时间">{formatTime(task.created_at)}</Descriptions.Item>
          <Descriptions.Item label="完成时间">{formatTime(task.finished_at)}</Descriptions.Item>
          <Descriptions.Item label="Namespace">{task.namespace}</Descriptions.Item>
          <Descriptions.Item label="Pod">{task.pod_name}</Descriptions.Item>
        </Descriptions>
      </Card>

      {/* Tabs */}
      <Card className="task-detail-tabs" bordered={false} bodyStyle={{ padding: '16px 24px' }}>
        <Tabs
          items={tabItems}
          tabBarGutter={24}
        />
      </Card>
    </Space>
  );
};

export default TaskDetail;
