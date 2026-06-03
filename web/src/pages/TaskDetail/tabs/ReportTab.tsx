import { Card, Descriptions, Typography, Alert, Space, Divider, List, Tag, Button, message, Modal, Form, Input, Select, Progress } from 'antd';
import type { ReactNode } from 'react';
import { useState } from 'react';
import {
  WarningOutlined,
  ExperimentOutlined,
  FileTextOutlined,
  SafetyOutlined,
  RobotOutlined,
  BulbOutlined,
  LikeOutlined,
  DislikeOutlined,
  CommentOutlined,
} from '@ant-design/icons';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import ConfidenceBar from '../../../components/ConfidenceBar';
import RiskLevelTag from '../../../components/RiskLevelTag';
import JsonViewer from '../../../components/JsonViewer';
import { submitFeedback } from '../../../api/tasks';
import type { DiagnosisReport } from '../../../api/types';

const { Paragraph, Text } = Typography;

const ReportTab = ({ report }: { report: DiagnosisReport }) => {
  const queryClient = useQueryClient();
  const [feedbackModalOpen, setFeedbackModalOpen] = useState(false);
  const [feedbackRating, setFeedbackRating] = useState<'useful' | 'not_useful'>('useful');
  const [form] = Form.useForm();

  const feedback = useMutation({
    mutationFn: (values: { rating: 'useful' | 'not_useful'; corrected_root_cause?: string; comment?: string }) =>
      submitFeedback(report.task_id, values),
    onSuccess: () => {
      message.success('反馈已记录');
      queryClient.invalidateQueries({ queryKey: ['dashboard-summary'] });
      setFeedbackModalOpen(false);
      form.resetFields();
    },
  });

  const handleQuickFeedback = (rating: 'useful' | 'not_useful') => {
    feedback.mutate({ rating });
  };

  const handleDetailedFeedback = () => {
    form.validateFields().then((values) => {
      feedback.mutate({
        rating: feedbackRating,
        corrected_root_cause: values.corrected_root_cause,
        comment: values.comment,
      });
    });
  };

  if (!report) return <Typography.Text type="secondary">暂无报告数据</Typography.Text>;

  const snapshot = report.agent_report_snapshot;

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Card
        className="report-section-card"
        title={
          <Space>
            <ExperimentOutlined style={{ color: '#1677ff' }} />
            <span>根因分析</span>
          </Space>
        }
        extra={
          <Space>
            <Button icon={<LikeOutlined />} size="small" onClick={() => handleQuickFeedback('useful')} loading={feedback.isPending}>
              有用
            </Button>
            <Button icon={<DislikeOutlined />} size="small" onClick={() => handleQuickFeedback('not_useful')} loading={feedback.isPending}>
              无用
            </Button>
            <Button
              icon={<CommentOutlined />}
              size="small"
              onClick={() => {
                setFeedbackRating('useful');
                setFeedbackModalOpen(true);
              }}
            >
              详细反馈
            </Button>
          </Space>
        }
        bordered={false}
      >
        <Descriptions column={2} size="small">
          <Descriptions.Item label="故障类型">
            {report.fault_type ? <Text strong>{report.fault_type}</Text> : '-'}
          </Descriptions.Item>
          <Descriptions.Item label="置信度">
            <ConfidenceBar score={report.confidence_score} />
          </Descriptions.Item>
          <Descriptions.Item label="风险等级">
            <RiskLevelTag level={report.risk_level} />
          </Descriptions.Item>
          <Descriptions.Item label="人工确认">
            {report.need_human_confirm ? (
              <Alert message="需要确认" type="warning" showIcon icon={<WarningOutlined />} banner style={{ display: 'inline-block', padding: '2px 10px' }} />
            ) : (
              <Text type="success">无需确认</Text>
            )}
          </Descriptions.Item>
        </Descriptions>
        {report.root_cause_summary && (
          <>
            <Divider style={{ margin: '16px 0' }} />
            <div style={{ padding: '12px 16px', background: '#f6ffed', borderRadius: 8, border: '1px solid #b7eb8f' }}>
              <Text strong style={{ color: '#389e0d' }}>结论：</Text>
              <div style={{ marginTop: 4 }}>{report.root_cause_summary}</div>
            </div>
          </>
        )}
      </Card>

      {snapshot?.primary_root_cause && (
        <Card className="report-section-card" title="主根因" bordered={false}>
          <Descriptions column={2} size="small">
            <Descriptions.Item label="假设类型">
              <Text strong>{snapshot.primary_root_cause.hypothesis_type}</Text>
            </Descriptions.Item>
            <Descriptions.Item label="置信度">
              <ConfidenceBar score={snapshot.primary_root_cause.confidence_score} />
            </Descriptions.Item>
            <Descriptions.Item label="状态">
              <Tag color={hypothesisStatusColor(snapshot.primary_root_cause.status)} style={{ borderRadius: 4 }}>
                {snapshot.primary_root_cause.status}
              </Tag>
            </Descriptions.Item>
            <Descriptions.Item label="证据">
              <Space size={[4, 4]} wrap>
                {(snapshot.primary_root_cause.evidence_refs || []).map((ref) => (
                  <Tag key={ref} color="red" style={{ borderRadius: 4 }}>{ref}</Tag>
                ))}
              </Space>
            </Descriptions.Item>
          </Descriptions>
          {snapshot.primary_root_cause.summary && (
            <Paragraph style={{ margin: '12px 0 0', lineHeight: 1.8, whiteSpace: 'pre-wrap' }}>
              {snapshot.primary_root_cause.summary}
            </Paragraph>
          )}
        </Card>
      )}

      {snapshot?.contributing_factors && snapshot.contributing_factors.length > 0 && (
        <Card className="report-section-card" title="促成因素" bordered={false}>
          <List
            size="small"
            dataSource={snapshot.contributing_factors}
            renderItem={(item) => (
              <List.Item>
                <Space direction="vertical" size={4} style={{ width: '100%' }}>
                  <Space size="small" wrap>
                    <Tag color={hypothesisStatusColor(item.status)} style={{ borderRadius: 4 }}>{item.status}</Tag>
                    <Text strong>{item.hypothesis_type}</Text>
                    <Progress percent={Math.round((item.confidence_score || 0) * 100)} size="small" style={{ width: 140 }} />
                  </Space>
                  {item.summary && <Text>{item.summary}</Text>}
                  <Space size={[4, 4]} wrap>
                    {(item.evidence_refs || []).map((ref) => (
                      <Tag key={ref} color="geekblue" style={{ borderRadius: 4 }}>{ref}</Tag>
                    ))}
                    {(item.missing_evidence || []).map((missing) => (
                      <Tag key={missing} color="orange" style={{ borderRadius: 4 }}>{missing}</Tag>
                    ))}
                  </Space>
                </Space>
              </List.Item>
            )}
          />
        </Card>
      )}

      {report.impact_analysis && (
        <Card className="report-section-card" title={<SectionTitle icon={<SafetyOutlined />} text="影响分析" />} bordered={false}>
          <Paragraph style={{ margin: 0, lineHeight: 1.8, whiteSpace: 'pre-wrap' }}>{report.impact_analysis}</Paragraph>
        </Card>
      )}

      {report.suggested_actions && (
        <Card className="report-section-card" title={<SectionTitle icon={<BulbOutlined />} text="建议动作" />} bordered={false}>
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>{report.suggested_actions}</Paragraph>
        </Card>
      )}

      {report.rule_based_result && (
        <Card className="report-section-card" title={<SectionTitle icon={<FileTextOutlined />} text="规则诊断结果" />} bordered={false}>
          <JsonViewer data={safeJSON(report.rule_based_result)} maxHeight={360} />
        </Card>
      )}

      {report.llm_enhanced_summary && (
        <Card className="report-section-card" title={<SectionTitle icon={<RobotOutlined />} text="LLM 增强摘要" />} bordered={false}>
          <JsonViewer data={safeJSON(report.llm_enhanced_summary)} maxHeight={360} />
        </Card>
      )}

      {report.agent_execution_summary && (
        <Card className="report-section-card" title={<SectionTitle icon={<ExperimentOutlined />} text="Agent 执行摘要" />} bordered={false}>
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>{report.agent_execution_summary}</Paragraph>
        </Card>
      )}

      {snapshot?.stop_reason && (
        <Card className="report-section-card" title="Agent 停止原因" bordered={false}>
          <Tag color="blue" style={{ fontSize: 13, padding: '2px 12px' }}>{snapshot.stop_reason}</Tag>
        </Card>
      )}

      {snapshot?.root_cause_evidence_refs && snapshot.root_cause_evidence_refs.length > 0 && (
        <Card className="report-section-card" title="根因证据" bordered={false}>
          <Space size={[6, 6]} wrap>
            {snapshot.root_cause_evidence_refs.map((ref) => (
              <Tag key={ref} color="red" style={{ borderRadius: 4 }}>{ref}</Tag>
            ))}
          </Space>
        </Card>
      )}

      {snapshot?.confidence_breakdown && snapshot.confidence_breakdown.length > 0 && (
        <Card className="report-section-card" title="置信度拆解" bordered={false}>
          <Space direction="vertical" size="small" style={{ width: '100%' }}>
            {snapshot.confidence_breakdown.map((item) => (
              <div
                key={item.source}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '140px 140px 1fr',
                  gap: 12,
                  alignItems: 'center',
                  padding: '8px 0',
                  borderBottom: '1px solid #f0f0f0',
                }}
              >
                <Space>
                  <Text strong>{item.source}</Text>
                  {item.missing && <Tag color="default">缺失</Tag>}
                </Space>
                <Progress
                  percent={Math.round((item.weight || 0) * 100)}
                  size="small"
                  status={item.missing ? 'normal' : 'active'}
                  showInfo
                />
                <Space size={[4, 4]} wrap>
                  {(item.evidence_refs || []).map((ref) => (
                    <Tag key={ref} color="geekblue" style={{ borderRadius: 4 }}>{ref}</Tag>
                  ))}
                  <Text type="secondary" style={{ fontSize: 12 }}>{item.reason}</Text>
                </Space>
              </div>
            ))}
          </Space>
        </Card>
      )}

      {snapshot?.missing_evidence && snapshot.missing_evidence.length > 0 && (
        <Card className="report-section-card" title="缺失证据" bordered={false}>
          <Space size={[6, 6]} wrap>
            {snapshot.missing_evidence.map((item) => (
              <Tag key={item} color="orange" style={{ borderRadius: 4 }}>{item}</Tag>
            ))}
          </Space>
        </Card>
      )}

      {snapshot?.evidence_chain && snapshot.evidence_chain.length > 0 && (
        <Card className="report-section-card" title="证据链" bordered={false}>
          <List
            size="small"
            dataSource={snapshot.evidence_chain}
            renderItem={(item) => (
              <List.Item>
                <Space size="small" wrap>
                  <Tag color="blue" style={{ borderRadius: 4 }}>{item.ref}</Tag>
                  <Tag style={{ borderRadius: 4 }}>{item.source_type}</Tag>
                  <RiskLevelTag level={item.severity === 'critical' ? 'high' : item.severity === 'warning' ? 'medium' : 'low'} />
                  <Text>{item.title}</Text>
                </Space>
              </List.Item>
            )}
          />
        </Card>
      )}

      {snapshot?.residual_risks && snapshot.residual_risks.length > 0 && (
        <Card className="report-section-card" title="残余风险" bordered={false}>
          <List
            size="small"
            dataSource={snapshot.residual_risks}
            renderItem={(item) => (
              <List.Item>
                <WarningOutlined style={{ color: '#faad14', marginRight: 8 }} />
                {item}
              </List.Item>
            )}
          />
        </Card>
      )}

      {snapshot?.verification_plan && snapshot.verification_plan.length > 0 && (
        <Card className="report-section-card" title="验证计划" bordered={false}>
          <Space direction="vertical" size="small" style={{ width: '100%' }}>
            {snapshot.verification_plan.map((vp, i) => (
              <Card key={`${vp.action_id}-${i}`} type="inner" size="small" title={vp.action_id}>
                <Descriptions column={1} size="small">
                  <Descriptions.Item label="检查项">{vp.what_to_check}</Descriptions.Item>
                  <Descriptions.Item label="使用工具">{vp.tool_to_use}</Descriptions.Item>
                  <Descriptions.Item label="成功条件">{vp.success_condition}</Descriptions.Item>
                  <Descriptions.Item label="超时时间">{vp.timeout_seconds}s</Descriptions.Item>
                </Descriptions>
              </Card>
            ))}
          </Space>
        </Card>
      )}

      {report.agent_report_snapshot && (
        <Card className="report-section-card" title="Agent 报告快照" bordered={false}>
          <JsonViewer data={report.agent_report_snapshot} maxHeight={500} />
        </Card>
      )}

      {/* Detailed feedback modal */}
      <Modal
        title="详细反馈"
        open={feedbackModalOpen}
        onCancel={() => setFeedbackModalOpen(false)}
        onOk={handleDetailedFeedback}
        confirmLoading={feedback.isPending}
        okText="提交反馈"
        cancelText="取消"
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item label="评价">
            <Select value={feedbackRating} onChange={setFeedbackRating}>
              <Select.Option value="useful">有用 - 诊断准确</Select.Option>
              <Select.Option value="not_useful">无用 - 诊断不准确</Select.Option>
            </Select>
          </Form.Item>
          <Form.Item label="正确根因" name="corrected_root_cause">
            <Input.TextArea
              rows={3}
              placeholder="如果诊断不准确，实际根因是什么？"
            />
          </Form.Item>
          <Form.Item label="补充说明" name="comment">
            <Input.TextArea
              rows={3}
              placeholder="补充说明诊断质量、缺失证据或误判点..."
            />
          </Form.Item>
        </Form>
      </Modal>
    </Space>
  );
};

const SectionTitle = ({ icon, text }: { icon: ReactNode; text: string }) => (
  <Space>
    {icon}
    <span>{text}</span>
  </Space>
);

const hypothesisStatusColor = (status: string) => {
  switch (status) {
    case 'confirmed':
      return 'green';
    case 'active':
      return 'blue';
    case 'rejected':
      return 'default';
    default:
      return 'purple';
  }
};

const safeJSON = (value: string) => {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
};

export default ReportTab;
