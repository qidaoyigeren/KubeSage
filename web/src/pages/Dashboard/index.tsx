import { Row, Col, Card, Statistic, Table, Button, Form, Input, Space, Spin, Typography, Empty, Progress, Tag } from 'antd';
import type { ReactNode } from 'react';
import {
  ExperimentOutlined,
  SyncOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  SearchOutlined,
  RocketOutlined,
  ArrowRightOutlined,
  BarChartOutlined,
  LikeOutlined,
  ClockCircleOutlined,
  ApiOutlined,
  DatabaseOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getDashboardSummary, getDashboardTrends } from '../../api/dashboard';
import { useTasks } from '../../hooks/useTasks';
import { useDiagnose } from '../../hooks/useDiagnose';
import StatusTag from '../../components/StatusTag';
import ConfidenceBar from '../../components/ConfidenceBar';
import { formatTime, formatTaskDuration } from '../../utils/format';
import type { DashboardBucket, DashboardTrendPoint, DiagnosisTask } from '../../api/types';
import { canAccessRole } from '../../api/auth';
import { useCurrentUser } from '../../hooks/useCurrentUser';

const percent = (value?: number) => `${Math.round((value || 0) * 100)}%`;

const Dashboard = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data, isLoading } = useTasks(1, 10);
  const { data: summary, isLoading: summaryLoading } = useQuery({
    queryKey: ['dashboard-summary'],
    queryFn: () => getDashboardSummary(7),
  });
  const { data: trends, isLoading: trendsLoading } = useQuery({
    queryKey: ['dashboard-trends'],
    queryFn: () => getDashboardTrends(14),
  });
  const diagnose = useDiagnose();
  const { data: currentUser } = useCurrentUser();
  const canDiagnose = canAccessRole(currentUser?.role, 'operator');

  const tasks = data?.items || [];
  const fallbackStats = {
    total: data?.total || 0,
    running: tasks.filter((t) => t.status === 'running' || t.status === 'pending').length,
    success: tasks.filter((t) => t.status === 'success').length,
    failed: tasks.filter((t) => t.status === 'failed').length,
  };
  const stats = {
    total: summary?.total_tasks ?? fallbackStats.total,
    running: summary?.running_tasks ?? fallbackStats.running,
    success: summary?.success_tasks ?? fallbackStats.success,
    failed: summary?.failed_tasks ?? fallbackStats.failed,
  };

  const recentTasks = tasks.slice(0, 8);

  const handleQuickDiagnose = async (values: { namespace: string; pod_name: string }) => {
    const task = await diagnose.mutateAsync(values);
    queryClient.invalidateQueries({ queryKey: ['tasks'] });
    queryClient.invalidateQueries({ queryKey: ['dashboard-summary'] });
    queryClient.invalidateQueries({ queryKey: ['dashboard-trends'] });
    navigate(`/tasks/${task.id}`);
  };

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: 60,
      render: (id: number) => <Typography.Text code>#{id}</Typography.Text>,
    },
    {
      title: '命名空间',
      dataIndex: 'namespace',
      key: 'namespace',
      width: 110,
      render: (t: string) => <Typography.Text strong>{t}</Typography.Text>,
    },
    { title: 'Pod 名称', dataIndex: 'pod_name', key: 'pod_name', ellipsis: true },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 90,
      render: (s: DiagnosisTask['status']) => <StatusTag status={s} />,
    },
    {
      title: '故障类型',
      dataIndex: 'fault_type',
      key: 'fault_type',
      width: 150,
      render: (t: string) => t || <Typography.Text type="secondary">-</Typography.Text>,
    },
    {
      title: '置信度',
      dataIndex: 'confidence_score',
      key: 'confidence_score',
      width: 130,
      render: (v: number) => (v ? <ConfidenceBar score={v} /> : '-'),
    },
    {
      title: '耗时',
      key: 'duration',
      width: 90,
      render: (_: unknown, r: DiagnosisTask) => (
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {formatTaskDuration(r.created_at, r.finished_at)}
        </Typography.Text>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 160,
      render: (t: string) => (
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {formatTime(t)}
        </Typography.Text>
      ),
    },
    {
      title: '',
      key: 'action',
      width: 48,
      render: () => <ArrowRightOutlined style={{ color: '#bfbfbf' }} />,
    },
  ];

  return (
    <Spin spinning={isLoading || summaryLoading || trendsLoading}>
      <Space direction="vertical" size="large" style={{ width: '100%' }}>
        <Row gutter={[16, 16]}>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-total" bordered={false}>
              <Statistic title="任务总数" value={stats.total} prefix={<ExperimentOutlined />} />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-running" bordered={false}>
              <Statistic title="运行中" value={stats.running} prefix={<SyncOutlined spin={stats.running > 0} />} />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-success" bordered={false}>
              <Statistic title="成功率" value={percent(summary?.success_rate)} prefix={<CheckCircleOutlined />} />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-failed" bordered={false}>
              <Statistic title="失败任务" value={stats.failed} prefix={<CloseCircleOutlined />} />
            </Card>
          </Col>
        </Row>

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={6}>
            <Card title={<MetricTitle icon={<LikeOutlined />} text="反馈准确率" />} bordered={false}>
              <Progress percent={Math.round((summary?.feedback_accuracy_rate || 0) * 100)} />
              <Space>
                <Tag color="green">有用 {summary?.feedback_useful || 0}</Tag>
                <Tag color="red">无用 {summary?.feedback_not_useful || 0}</Tag>
              </Space>
            </Card>
          </Col>
          <Col xs={24} lg={6}>
            <Card title={<MetricTitle icon={<ClockCircleOutlined />} text="诊断耗时" />} bordered={false}>
              <Statistic value={summary?.average_duration_seconds || 0} precision={1} suffix="秒 平均" />
              <Typography.Text type="secondary">
                P95 {Number(summary?.p95_duration_seconds || 0).toFixed(1)}s
              </Typography.Text>
            </Card>
          </Col>
          <Col xs={24} lg={6}>
            <Card title={<MetricTitle icon={<ApiOutlined />} text="工具健康度" />} bordered={false}>
              <Progress
                percent={Math.round((summary?.tool_failure_rate || 0) * 100)}
                status={(summary?.tool_failure_rate || 0) > 0 ? 'exception' : 'success'}
              />
              <Space wrap>
                <Tag color="blue">调用 {summary?.tool_calls || 0}</Tag>
                <Tag color={(summary?.tool_failures || 0) > 0 ? 'red' : 'green'}>失败 {summary?.tool_failures || 0}</Tag>
                <Tag icon={<DatabaseOutlined />} color={(summary?.dead_letters || 0) > 0 ? 'volcano' : 'default'}>异常 {summary?.dead_letters || 0}</Tag>
              </Space>
            </Card>
          </Col>
          <Col xs={24} lg={6}>
            <Card title={<MetricTitle icon={<BarChartOutlined />} text="LLM 健康度" />} bordered={false}>
              <Space direction="vertical" style={{ width: '100%' }}>
                <Space wrap>
                  {Object.entries(summary?.llm_calls || {}).length > 0 ? (
                    Object.entries(summary?.llm_calls || {}).map(([status, count]) => (
                      <Tag key={status} color={status === 'success' ? 'green' : 'orange'}>
                        {status}: {count}
                      </Tag>
                    ))
                  ) : (
                    <Typography.Text type="secondary">暂无 Planner 调用</Typography.Text>
                  )}
                </Space>
                <Typography.Text type="secondary">
                  失败率 {percent(summary?.llm_failure_rate)} / Token {summary?.llm_total_tokens || 0} / 平均 {Math.round(summary?.llm_average_latency_ms || 0)}ms / 成本 ${Number(summary?.llm_estimated_cost || 0).toFixed(4)}
                </Typography.Text>
              </Space>
            </Card>
          </Col>
        </Row>

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={8}>
            <BucketCard title="高频根因" data={summary?.top_root_causes || []} />
          </Col>
          <Col xs={24} lg={8}>
            <BucketCard title="高频 Agent 工具" data={summary?.analyzer_hit_rates || []} />
          </Col>
          <Col xs={24} lg={8}>
            <TrendCard trends={trends || []} />
          </Col>
        </Row>

        {canDiagnose && (
          <Card
            className="quick-diagnose-card"
            title={
              <Space>
                <RocketOutlined style={{ color: '#1677ff' }} />
                <span>快速诊断</span>
              </Space>
            }
          >
            <Form layout="inline" onFinish={handleQuickDiagnose} style={{ flexWrap: 'wrap', gap: 8 }}>
              <Form.Item name="namespace" rules={[{ required: true, message: '请输入命名空间' }]}>
                <Input placeholder="命名空间" style={{ width: 180 }} />
              </Form.Item>
              <Form.Item name="pod_name" rules={[{ required: true, message: '请输入 Pod 名称' }]}>
                <Input placeholder="Pod 名称" style={{ width: 280 }} />
              </Form.Item>
              <Form.Item>
                <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={diagnose.isPending}>
                  开始诊断
                </Button>
              </Form.Item>
            </Form>
          </Card>
        )}

        <Card
          className="task-list-card"
          title={
            <Space>
              <span style={{ fontWeight: 600 }}>最近任务</span>
              {stats.running > 0 && (
                <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                  ({stats.running} 个运行中)
                </Typography.Text>
              )}
            </Space>
          }
          extra={
            <Button type="link" onClick={() => navigate('/tasks')}>
              查看全部 <ArrowRightOutlined />
            </Button>
          }
        >
          {recentTasks.length > 0 ? (
            <Table
              dataSource={recentTasks}
              columns={columns}
              rowKey="id"
              size="small"
              pagination={false}
              onRow={(record) => ({
                onClick: () => navigate(`/tasks/${record.id}`),
                style: { cursor: 'pointer' },
              })}
            />
          ) : (
            <Empty description="暂无诊断任务" style={{ padding: '40px 0' }}>
              {canDiagnose && (
                <Button type="primary" onClick={() => navigate('/diagnose')}>
                  创建第一个诊断
                </Button>
              )}
            </Empty>
          )}
        </Card>
      </Space>
    </Spin>
  );
};

const MetricTitle = ({ icon, text }: { icon: ReactNode; text: string }) => (
  <Space>
    {icon}
    <span>{text}</span>
  </Space>
);

const BucketCard = ({ title, data }: { title: string; data: DashboardBucket[] }) => (
  <Card title={title} bordered={false}>
    <Space direction="vertical" style={{ width: '100%' }}>
      {data.length > 0 ? (
        data.slice(0, 6).map((item) => (
          <div key={item.name || 'unknown'} style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
            <Typography.Text ellipsis>{item.name || '未知'}</Typography.Text>
            <Tag color="blue">{item.count}</Tag>
          </div>
        ))
      ) : (
        <Typography.Text type="secondary">暂无数据</Typography.Text>
      )}
    </Space>
  </Card>
);

const TrendCard = ({ trends }: { trends: DashboardTrendPoint[] }) => (
  <Card title="故障趋势" bordered={false}>
    <Space direction="vertical" style={{ width: '100%' }}>
      {trends.length > 0 ? (
        trends.slice(0, 8).map((item) => (
          <div key={`${item.date}-${item.fault_type}`} style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
            <Typography.Text ellipsis>
              {item.date} / {item.fault_type || '未知'}
            </Typography.Text>
            <Tag>{item.count}</Tag>
          </div>
        ))
      ) : (
        <Typography.Text type="secondary">暂无趋势数据</Typography.Text>
      )}
    </Space>
  </Card>
);

export default Dashboard;
