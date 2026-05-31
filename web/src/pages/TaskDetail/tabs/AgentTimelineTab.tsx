import { Timeline, Tag, Typography, Collapse, Space, Empty } from 'antd';
import type { ReactNode } from 'react';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  MinusCircleOutlined,
  LoadingOutlined,
  AimOutlined,
  ToolOutlined,
  EyeOutlined,
  BulbOutlined,
  AuditOutlined,
  ThunderboltOutlined,
  SafetyOutlined,
} from '@ant-design/icons';
import JsonViewer from '../../../components/JsonViewer';
import { formatDuration } from '../../../utils/format';
import type { AgentStep, AgentStepStage, AgentStepStatus } from '../../../api/types';

const stageConfig: Record<AgentStepStage, { color: string; label: string; icon: ReactNode }> = {
  plan: { color: '#1677ff', label: 'PLAN', icon: <AimOutlined /> },
  tool_call: { color: '#722ed1', label: 'TOOL', icon: <ToolOutlined /> },
  observation: { color: '#13c2c2', label: 'OBSERVE', icon: <EyeOutlined /> },
  reflection: { color: '#faad14', label: 'REFLECT', icon: <BulbOutlined /> },
  decision: { color: '#52c41a', label: 'DECIDE', icon: <AuditOutlined /> },
  action: { color: '#eb2f96', label: 'ACTION', icon: <ThunderboltOutlined /> },
  verification: { color: '#2f54eb', label: 'VERIFY', icon: <SafetyOutlined /> },
};

const statusIcon: Record<AgentStepStatus, ReactNode> = {
  success: <CheckCircleOutlined style={{ color: '#52c41a', fontSize: 16 }} />,
  failed: <CloseCircleOutlined style={{ color: '#f5576c', fontSize: 16 }} />,
  skipped: <MinusCircleOutlined style={{ color: '#bfbfbf', fontSize: 16 }} />,
};

const groupOrder = ['PLAN', 'EXECUTE', 'HYPOTHESIZE', 'REFLECT'] as const;
type TimelineGroup = (typeof groupOrder)[number];

const groupStep = (step: AgentStep): TimelineGroup => {
  if (step.stage === 'plan') return 'PLAN';
  if (step.stage === 'tool_call' || step.stage === 'observation' || step.stage === 'action' || step.stage === 'verification') {
    return 'EXECUTE';
  }
  if (step.stage === 'reflection' && step.reasoning_summary?.toLowerCase().includes('hypothes')) {
    return 'HYPOTHESIZE';
  }
  return 'REFLECT';
};

const AgentTimelineTab = ({ steps }: { steps: AgentStep[] }) => {
  if (!steps || steps.length === 0) {
    return <Empty description="No Agent trace recorded" />;
  }

  const sorted = [...steps].sort((a, b) => a.step_index - b.step_index);
  const grouped = groupOrder.map((group) => ({
    group,
    steps: sorted.filter((step) => groupStep(step) === group),
  })).filter((item) => item.steps.length > 0);

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {grouped.map(({ group, steps: groupSteps }) => (
        <div key={group}>
          <Typography.Title level={5} style={{ margin: '0 0 12px' }}>
            {group}
          </Typography.Title>
          <Timeline items={groupSteps.map(renderStep)} />
        </div>
      ))}
    </Space>
  );
};

const renderStep = (step: AgentStep) => {
  const stage = stageConfig[step.stage] || { color: '#999', label: step.stage, icon: null };
  const latency = step.tool_latency_ms || step.duration_ms || 0;
  return {
    dot: statusIcon[step.status] || <LoadingOutlined />,
    color: stage.color,
    children: (
      <div className="agent-timeline-step">
        <div className="step-header">
          <Tag color={stage.color} icon={stage.icon} style={{ borderRadius: 4, margin: 0 }}>
            {stage.label}
          </Tag>
          <Typography.Text strong style={{ fontSize: 14 }}>
            #{step.step_index}
            {step.tool_name && (
              <Typography.Text code style={{ marginLeft: 6, fontWeight: 400 }}>
                {step.tool_name}
              </Typography.Text>
            )}
          </Typography.Text>
          <Tag style={{ borderRadius: 4, fontSize: 12 }}>{formatDuration(latency)}</Tag>
          {step.parallel_group && (
            <Tag color="geekblue" style={{ borderRadius: 4, fontSize: 12 }}>
              {step.parallel_group}
            </Tag>
          )}
        </div>

        {(step.observation_summary || step.reasoning_summary) && (
          <div className="step-reasoning">
            {step.observation_summary || step.reasoning_summary}
          </div>
        )}

        {(step.input_json || step.output_json) && (
          <Collapse
            size="small"
            style={{ marginTop: 8 }}
            items={[
              ...(step.input_json
                ? [{
                    key: 'input',
                    label: <Typography.Text type="secondary" style={{ fontSize: 12 }}>Input</Typography.Text>,
                    children: <JsonViewer data={step.input_json} maxHeight={200} />,
                  }]
                : []),
              ...(step.output_json
                ? [{
                    key: 'output',
                    label: <Typography.Text type="secondary" style={{ fontSize: 12 }}>Output</Typography.Text>,
                    children: <JsonViewer data={step.output_json} maxHeight={200} />,
                  }]
                : []),
            ]}
          />
        )}
      </div>
    ),
  };
};

export default AgentTimelineTab;
