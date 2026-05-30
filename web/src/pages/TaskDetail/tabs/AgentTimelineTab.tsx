import { Timeline, Tag, Typography, Collapse, Space, Empty } from 'antd';
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

const stageConfig: Record<AgentStepStage, { color: string; label: string; icon: React.ReactNode }> = {
  plan: { color: '#1677ff', label: '规划', icon: <AimOutlined /> },
  tool_call: { color: '#722ed1', label: '工具调用', icon: <ToolOutlined /> },
  observation: { color: '#13c2c2', label: '观察', icon: <EyeOutlined /> },
  reflection: { color: '#faad14', label: '反思', icon: <BulbOutlined /> },
  decision: { color: '#52c41a', label: '决策', icon: <AuditOutlined /> },
  action: { color: '#eb2f96', label: '执行', icon: <ThunderboltOutlined /> },
  verification: { color: '#2f54eb', label: '验证', icon: <SafetyOutlined /> },
};

const statusIcon: Record<AgentStepStatus, React.ReactNode> = {
  success: <CheckCircleOutlined style={{ color: '#52c41a', fontSize: 16 }} />,
  failed: <CloseCircleOutlined style={{ color: '#f5576c', fontSize: 16 }} />,
  skipped: <MinusCircleOutlined style={{ color: '#bfbfbf', fontSize: 16 }} />,
};

const AgentTimelineTab = ({ steps }: { steps: AgentStep[] }) => {
  if (!steps || steps.length === 0) {
    return <Empty description="暂无 Agent 执行记录" />;
  }

  const sorted = [...steps].sort((a, b) => a.step_index - b.step_index);

  return (
    <Timeline
      items={sorted.map((step) => {
        const stage = stageConfig[step.stage] || { color: '#999', label: step.stage, icon: null };

        return {
          dot: statusIcon[step.status] || <LoadingOutlined />,
          color: stage.color,
          children: (
            <div className="agent-timeline-step">
              <div className="step-header">
                <Tag
                  color={stage.color}
                  icon={stage.icon}
                  style={{ borderRadius: 4, margin: 0 }}
                >
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
                <Tag style={{ borderRadius: 4, fontSize: 12 }}>
                  {formatDuration(step.duration_ms)}
                </Tag>
              </div>

              {step.reasoning_summary && (
                <div className="step-reasoning">
                  {step.reasoning_summary}
                </div>
              )}

              {(step.input_json || step.output_json) && (
                <Collapse
                  size="small"
                  style={{ marginTop: 8 }}
                  items={[
                    ...(step.input_json
                      ? [
                          {
                            key: 'input',
                            label: (
                              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                                输入参数
                              </Typography.Text>
                            ),
                            children: <JsonViewer data={step.input_json} maxHeight={200} />,
                          },
                        ]
                      : []),
                    ...(step.output_json
                      ? [
                          {
                            key: 'output',
                            label: (
                              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                                输出结果
                              </Typography.Text>
                            ),
                            children: <JsonViewer data={step.output_json} maxHeight={200} />,
                          },
                        ]
                      : []),
                  ]}
                />
              )}
            </div>
          ),
        };
      })}
    />
  );
};

export default AgentTimelineTab;
