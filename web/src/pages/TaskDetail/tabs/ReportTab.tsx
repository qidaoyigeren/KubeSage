import { Card, Descriptions, Typography, Alert, Space, Divider, List, Tag } from 'antd';
import {
  WarningOutlined,
  ExperimentOutlined,
  FileTextOutlined,
  SafetyOutlined,
  RobotOutlined,
  BulbOutlined,
} from '@ant-design/icons';
import ConfidenceBar from '../../../components/ConfidenceBar';
import RiskLevelTag from '../../../components/RiskLevelTag';
import JsonViewer from '../../../components/JsonViewer';
import type { DiagnosisReport } from '../../../api/types';

const { Paragraph, Text } = Typography;

const ReportTab = ({ report }: { report: DiagnosisReport }) => {
  if (!report) return <Typography.Text type="secondary">暂无报告数据</Typography.Text>;

  const snapshot = report.agent_report_snapshot;

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {/* Root Cause */}
      <Card
        className="report-section-card"
        title={
          <Space>
            <ExperimentOutlined style={{ color: '#667eea' }} />
            <span>根因分析</span>
          </Space>
        }
        bordered={false}
      >
        <Descriptions column={2} size="small">
          <Descriptions.Item label="故障类型">
            {report.fault_type ? (
              <Text strong style={{ color: '#667eea' }}>{report.fault_type}</Text>
            ) : '-'}
          </Descriptions.Item>
          <Descriptions.Item label="置信度">
            <ConfidenceBar score={report.confidence_score} />
          </Descriptions.Item>
          <Descriptions.Item label="风险等级">
            <RiskLevelTag level={report.risk_level} />
          </Descriptions.Item>
          <Descriptions.Item label="需要人工确认">
            {report.need_human_confirm ? (
              <Alert
                message="是"
                type="warning"
                showIcon
                icon={<WarningOutlined />}
                banner
                style={{ display: 'inline-block', padding: '2px 10px' }}
              />
            ) : (
              <Text type="success">否</Text>
            )}
          </Descriptions.Item>
        </Descriptions>
        {report.root_cause_summary && (
          <>
            <Divider style={{ margin: '16px 0' }} />
            <div style={{ padding: '12px 16px', background: '#f6ffed', borderRadius: 8, border: '1px solid #b7eb8f' }}>
              <Text strong style={{ color: '#389e0d' }}>根因摘要：</Text>
              <div style={{ marginTop: 4 }}>{report.root_cause_summary}</div>
            </div>
          </>
        )}
      </Card>

      {/* Impact Analysis */}
      {report.impact_analysis && (
        <Card
          className="report-section-card"
          title={
            <Space>
              <SafetyOutlined style={{ color: '#faad14' }} />
              <span>影响分析</span>
            </Space>
          }
          bordered={false}
        >
          <Paragraph style={{ margin: 0, lineHeight: 1.8 }}>{report.impact_analysis}</Paragraph>
        </Card>
      )}

      {/* Suggested Actions */}
      {report.suggested_actions && (
        <Card
          className="report-section-card"
          title={
            <Space>
              <BulbOutlined style={{ color: '#52c41a' }} />
              <span>建议操作</span>
            </Space>
          }
          bordered={false}
        >
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>
            {report.suggested_actions}
          </Paragraph>
        </Card>
      )}

      {/* Rule-based Result */}
      {report.rule_based_result && (
        <Card
          className="report-section-card"
          title={
            <Space>
              <FileTextOutlined style={{ color: '#722ed1' }} />
              <span>规则引擎分析结果</span>
            </Space>
          }
          bordered={false}
        >
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>
            {report.rule_based_result}
          </Paragraph>
        </Card>
      )}

      {/* LLM Summary */}
      {report.llm_enhanced_summary && (
        <Card
          className="report-section-card"
          title={
            <Space>
              <RobotOutlined style={{ color: '#13c2c2' }} />
              <span>LLM 增强摘要</span>
            </Space>
          }
          bordered={false}
        >
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>
            {report.llm_enhanced_summary}
          </Paragraph>
        </Card>
      )}

      {/* Agent Execution Summary */}
      {report.agent_execution_summary && (
        <Card
          className="report-section-card"
          title={
            <Space>
              <ExperimentOutlined style={{ color: '#667eea' }} />
              <span>Agent 执行摘要</span>
            </Space>
          }
          bordered={false}
        >
          <Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap', lineHeight: 1.8 }}>
            {report.agent_execution_summary}
          </Paragraph>
        </Card>
      )}

      {/* Stop Reason */}
      {snapshot?.stop_reason && (
        <Card className="report-section-card" title="Agent 停止原因" bordered={false}>
          <Tag color="blue" style={{ fontSize: 13, padding: '2px 12px' }}>
            {snapshot.stop_reason}
          </Tag>
        </Card>
      )}

      {/* Residual Risks */}
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

      {/* Verification Plan */}
      {snapshot?.verification_plan && snapshot.verification_plan.length > 0 && (
        <Card className="report-section-card" title="验证计划" bordered={false}>
          <Space direction="vertical" size="small" style={{ width: '100%' }}>
            {snapshot.verification_plan.map((vp, i) => (
              <Card key={i} type="inner" size="small" title={vp.action_id}>
                <Descriptions column={1} size="small">
                  <Descriptions.Item label="验证内容">{vp.what_to_check}</Descriptions.Item>
                  <Descriptions.Item label="使用工具">{vp.tool_to_use}</Descriptions.Item>
                  <Descriptions.Item label="成功条件">{vp.success_condition}</Descriptions.Item>
                  <Descriptions.Item label="超时">{vp.timeout_seconds}s</Descriptions.Item>
                </Descriptions>
              </Card>
            ))}
          </Space>
        </Card>
      )}

      {/* Raw Snapshot */}
      {report.agent_report_snapshot && (
        <Card
          className="report-section-card"
          title="Agent 报告快照 (原始 JSON)"
          bordered={false}
        >
          <JsonViewer data={report.agent_report_snapshot} maxHeight={500} />
        </Card>
      )}
    </Space>
  );
};

export default ReportTab;
