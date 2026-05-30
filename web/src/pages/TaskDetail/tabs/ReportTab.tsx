import { Card, Descriptions, Typography, Alert, Space, Divider, List, Tag, Button, message, Modal, Form, Input, Select } from 'antd';
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
      message.success('Feedback recorded');
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

  if (!report) return <Typography.Text type="secondary">No report data</Typography.Text>;

  const snapshot = report.agent_report_snapshot;

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Card
        className="report-section-card"
        title={
          <Space>
            <ExperimentOutlined style={{ color: '#1677ff' }} />
            <span>Root Cause Analysis</span>
          </Space>
        }
        extra={
          <Space>
            <Button icon={<LikeOutlined />} size="small" onClick={() => handleQuickFeedback('useful')} loading={feedback.isPending}>
              Useful
            </Button>
            <Button icon={<DislikeOutlined />} size="small" onClick={() => handleQuickFeedback('not_useful')} loading={feedback.isPending}>
              Not useful
            </Button>
            <Button
              icon={<CommentOutlined />}
              size="small"
              onClick={() => {
                setFeedbackRating('useful');
                setFeedbackModalOpen(true);
              }}
            >
              Detailed Feedback
            </Button>
          </Space>
        }
        bordered={false}
      >
        <Descriptions column={2} size="small">
          <Descriptions.Item label="Fault type">
            {report.fault_type ? <Text strong>{report.fault_type}</Text> : '-'}
          </Descriptions.Item>
          <Descriptions.Item label="Confidence">
            <ConfidenceBar score={report.confidence_score} />
          </Descriptions.Item>
          <Descriptions.Item label="Risk">
            <RiskLevelTag level={report.risk_level} />
          </Descriptions.Item>
          <Descriptions.Item label="Human confirmation">
            {report.need_human_confirm ? (
              <Alert message="Required" type="warning" showIcon icon={<WarningOutlined />} banner style={{ display: 'inline-block', padding: '2px 10px' }} />
            ) : (
              <Text type="success">Not required</Text>
            )}
          </Descriptions.Item>
        </Descriptions>
        {report.root_cause_summary && (
          <>
            <Divider style={{ margin: '16px 0' }} />
            <div style={{ padding: '12px 16px', background: '#f6ffed', borderRadius: 8, border: '1px solid #b7eb8f' }}>
              <Text strong style={{ color: '#389e0d' }}>Summary: </Text>
              <div style={{ marginTop: 4 }}>{report.root_cause_summary}</div>
            </div>
          </>
        )}
      </Card>

      {report.impact_analysis && (
        <Card className="report-section-card" title={<SectionTitle icon={<SafetyOutlined />} text="Impact Analysis" />} bordered={false}>
          <Paragraph style={{ margin: 0, lineHeight: 1.8, whiteSpace: 'pre-wrap' }}>{report.impact_analysis}</Paragraph>
        </Card>
      )}

      {report.suggested_actions && (
        <Card className="report-section-card" title={<SectionTitle icon={<BulbOutlined />} text="Suggested Actions" />} bordered={false}>
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>{report.suggested_actions}</Paragraph>
        </Card>
      )}

      {report.rule_based_result && (
        <Card className="report-section-card" title={<SectionTitle icon={<FileTextOutlined />} text="Rule Result" />} bordered={false}>
          <JsonViewer data={safeJSON(report.rule_based_result)} maxHeight={360} />
        </Card>
      )}

      {report.llm_enhanced_summary && (
        <Card className="report-section-card" title={<SectionTitle icon={<RobotOutlined />} text="LLM Summary" />} bordered={false}>
          <JsonViewer data={safeJSON(report.llm_enhanced_summary)} maxHeight={360} />
        </Card>
      )}

      {report.agent_execution_summary && (
        <Card className="report-section-card" title={<SectionTitle icon={<ExperimentOutlined />} text="Agent Summary" />} bordered={false}>
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>{report.agent_execution_summary}</Paragraph>
        </Card>
      )}

      {snapshot?.stop_reason && (
        <Card className="report-section-card" title="Agent Stop Reason" bordered={false}>
          <Tag color="blue" style={{ fontSize: 13, padding: '2px 12px' }}>{snapshot.stop_reason}</Tag>
        </Card>
      )}

      {snapshot?.residual_risks && snapshot.residual_risks.length > 0 && (
        <Card className="report-section-card" title="Residual Risks" bordered={false}>
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
        <Card className="report-section-card" title="Verification Plan" bordered={false}>
          <Space direction="vertical" size="small" style={{ width: '100%' }}>
            {snapshot.verification_plan.map((vp, i) => (
              <Card key={`${vp.action_id}-${i}`} type="inner" size="small" title={vp.action_id}>
                <Descriptions column={1} size="small">
                  <Descriptions.Item label="Check">{vp.what_to_check}</Descriptions.Item>
                  <Descriptions.Item label="Tool">{vp.tool_to_use}</Descriptions.Item>
                  <Descriptions.Item label="Success">{vp.success_condition}</Descriptions.Item>
                  <Descriptions.Item label="Timeout">{vp.timeout_seconds}s</Descriptions.Item>
                </Descriptions>
              </Card>
            ))}
          </Space>
        </Card>
      )}

      {report.agent_report_snapshot && (
        <Card className="report-section-card" title="Agent Report Snapshot" bordered={false}>
          <JsonViewer data={report.agent_report_snapshot} maxHeight={500} />
        </Card>
      )}

      {/* Detailed Feedback Modal */}
      <Modal
        title="Detailed Feedback"
        open={feedbackModalOpen}
        onCancel={() => setFeedbackModalOpen(false)}
        onOk={handleDetailedFeedback}
        confirmLoading={feedback.isPending}
        okText="Submit Feedback"
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item label="Rating">
            <Select value={feedbackRating} onChange={setFeedbackRating}>
              <Select.Option value="useful">Useful - Diagnosis was accurate</Select.Option>
              <Select.Option value="not_useful">Not Useful - Diagnosis was inaccurate</Select.Option>
            </Select>
          </Form.Item>
          <Form.Item label="Corrected Root Cause" name="corrected_root_cause">
            <Input.TextArea
              rows={3}
              placeholder="If the diagnosis was wrong, what was the actual root cause?"
            />
          </Form.Item>
          <Form.Item label="Additional Comments" name="comment">
            <Input.TextArea
              rows={3}
              placeholder="Any additional feedback about the diagnosis quality..."
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

const safeJSON = (value: string) => {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
};

export default ReportTab;
