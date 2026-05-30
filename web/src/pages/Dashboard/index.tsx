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
      title: 'Namespace',
      dataIndex: 'namespace',
      key: 'namespace',
      width: 110,
      render: (t: string) => <Typography.Text strong>{t}</Typography.Text>,
    },
    { title: 'Pod', dataIndex: 'pod_name', key: 'pod_name', ellipsis: true },
    {
      title: 'Status',
      dataIndex: 'status',
      key: 'status',
      width: 90,
      render: (s: DiagnosisTask['status']) => <StatusTag status={s} />,
    },
    {
      title: 'Fault',
      dataIndex: 'fault_type',
      key: 'fault_type',
      width: 150,
      render: (t: string) => t || <Typography.Text type="secondary">-</Typography.Text>,
    },
    {
      title: 'Confidence',
      dataIndex: 'confidence_score',
      key: 'confidence_score',
      width: 130,
      render: (v: number) => (v ? <ConfidenceBar score={v} /> : '-'),
    },
    {
      title: 'Duration',
      key: 'duration',
      width: 90,
      render: (_: unknown, r: DiagnosisTask) => (
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {formatTaskDuration(r.created_at, r.finished_at)}
        </Typography.Text>
      ),
    },
    {
      title: 'Created',
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
              <Statistic title="Total Tasks" value={stats.total} prefix={<ExperimentOutlined />} />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-running" bordered={false}>
              <Statistic title="Running" value={stats.running} prefix={<SyncOutlined spin={stats.running > 0} />} />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-success" bordered={false}>
              <Statistic title="Success Rate" value={percent(summary?.success_rate)} prefix={<CheckCircleOutlined />} />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-failed" bordered={false}>
              <Statistic title="Failed" value={stats.failed} prefix={<CloseCircleOutlined />} />
            </Card>
          </Col>
        </Row>

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={8}>
            <Card title={<MetricTitle icon={<LikeOutlined />} text="Feedback Accuracy" />} bordered={false}>
              <Progress percent={Math.round((summary?.feedback_accuracy_rate || 0) * 100)} />
              <Space>
                <Tag color="green">Useful {summary?.feedback_useful || 0}</Tag>
                <Tag color="red">Not useful {summary?.feedback_not_useful || 0}</Tag>
              </Space>
            </Card>
          </Col>
          <Col xs={24} lg={8}>
            <Card title={<MetricTitle icon={<ClockCircleOutlined />} text="Mean Diagnosis Time" />} bordered={false}>
              <Statistic value={summary?.average_duration_seconds || 0} precision={1} suffix="s" />
            </Card>
          </Col>
          <Col xs={24} lg={8}>
            <Card title={<MetricTitle icon={<BarChartOutlined />} text="LLM Planner Calls" />} bordered={false}>
              <Space direction="vertical" style={{ width: '100%' }}>
                <Space wrap>
                {Object.entries(summary?.llm_calls || {}).length > 0 ? (
                  Object.entries(summary?.llm_calls || {}).map(([status, count]) => (
                    <Tag key={status} color={status === 'success' ? 'green' : 'orange'}>
                      {status}: {count}
                    </Tag>
                  ))
                ) : (
                  <Typography.Text type="secondary">No planner calls yet</Typography.Text>
                )}
                </Space>
                <Typography.Text type="secondary">
                  Tokens {summary?.llm_total_tokens || 0} · Avg latency {Math.round(summary?.llm_average_latency_ms || 0)}ms · Cost ${Number(summary?.llm_estimated_cost || 0).toFixed(4)}
                </Typography.Text>
              </Space>
            </Card>
          </Col>
        </Row>

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={8}>
            <BucketCard title="Top Root Causes" data={summary?.top_root_causes || []} />
          </Col>
          <Col xs={24} lg={8}>
            <BucketCard title="Top Agent Tools" data={summary?.analyzer_hit_rates || []} />
          </Col>
          <Col xs={24} lg={8}>
            <TrendCard trends={trends || []} />
          </Col>
        </Row>

        <Card
          className="quick-diagnose-card"
          title={
            <Space>
              <RocketOutlined style={{ color: '#1677ff' }} />
              <span>Quick Diagnosis</span>
            </Space>
          }
        >
          <Form layout="inline" onFinish={handleQuickDiagnose} style={{ flexWrap: 'wrap', gap: 8 }}>
            <Form.Item name="namespace" rules={[{ required: true, message: 'Namespace is required' }]}>
              <Input placeholder="Namespace" style={{ width: 180 }} />
            </Form.Item>
            <Form.Item name="pod_name" rules={[{ required: true, message: 'Pod name is required' }]}>
              <Input placeholder="Pod name" style={{ width: 280 }} />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={diagnose.isPending}>
                Start
              </Button>
            </Form.Item>
          </Form>
        </Card>

        <Card
          className="task-list-card"
          title={
            <Space>
              <span style={{ fontWeight: 600 }}>Recent Tasks</span>
              {stats.running > 0 && (
                <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                  ({stats.running} running)
                </Typography.Text>
              )}
            </Space>
          }
          extra={
            <Button type="link" onClick={() => navigate('/tasks')}>
              View all <ArrowRightOutlined />
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
            <Empty description="No diagnosis tasks yet" style={{ padding: '40px 0' }}>
              <Button type="primary" onClick={() => navigate('/diagnose')}>
                Create first diagnosis
              </Button>
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
            <Typography.Text ellipsis>{item.name || 'unknown'}</Typography.Text>
            <Tag color="blue">{item.count}</Tag>
          </div>
        ))
      ) : (
        <Typography.Text type="secondary">No data</Typography.Text>
      )}
    </Space>
  </Card>
);

const TrendCard = ({ trends }: { trends: DashboardTrendPoint[] }) => (
  <Card title="Fault Trend" bordered={false}>
    <Space direction="vertical" style={{ width: '100%' }}>
      {trends.length > 0 ? (
        trends.slice(0, 8).map((item) => (
          <div key={`${item.date}-${item.fault_type}`} style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
            <Typography.Text ellipsis>
              {item.date} · {item.fault_type || 'unknown'}
            </Typography.Text>
            <Tag>{item.count}</Tag>
          </div>
        ))
      ) : (
        <Typography.Text type="secondary">No trend data</Typography.Text>
      )}
    </Space>
  </Card>
);

export default Dashboard;
