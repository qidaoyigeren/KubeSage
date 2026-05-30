import { Row, Col, Card, Statistic, Table, Button, Form, Input, Space, Spin, Typography, Empty } from 'antd';
import {
  ExperimentOutlined,
  SyncOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  SearchOutlined,
  RocketOutlined,
  ArrowRightOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { useTasks } from '../../hooks/useTasks';
import { useDiagnose } from '../../hooks/useDiagnose';
import StatusTag from '../../components/StatusTag';
import ConfidenceBar from '../../components/ConfidenceBar';
import { formatTime, formatTaskDuration } from '../../utils/format';
import type { DiagnosisTask } from '../../api/types';

const Dashboard = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data, isLoading } = useTasks(1, 100);
  const diagnose = useDiagnose();

  const tasks = data?.items || [];
  const stats = {
    total: data?.total || 0,
    running: tasks.filter((t) => t.status === 'running' || t.status === 'pending').length,
    success: tasks.filter((t) => t.status === 'success').length,
    failed: tasks.filter((t) => t.status === 'failed').length,
  };

  const recentTasks = tasks.slice(0, 8);

  const handleQuickDiagnose = async (values: { namespace: string; pod_name: string }) => {
    const task = await diagnose.mutateAsync(values);
    queryClient.invalidateQueries({ queryKey: ['tasks'] });
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
    <Spin spinning={isLoading}>
      <Space direction="vertical" size="large" style={{ width: '100%' }}>
        {/* Stat Cards */}
        <Row gutter={[16, 16]}>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-total" bordered={false}>
              <Statistic
                title="总任务数"
                value={stats.total}
                prefix={<ExperimentOutlined />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-running" bordered={false}>
              <Statistic
                title="运行中"
                value={stats.running}
                prefix={<SyncOutlined spin={stats.running > 0} />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-success" bordered={false}>
              <Statistic
                title="成功"
                value={stats.success}
                prefix={<CheckCircleOutlined />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card className="stat-card card-failed" bordered={false}>
              <Statistic
                title="失败"
                value={stats.failed}
                prefix={<CloseCircleOutlined />}
              />
            </Card>
          </Col>
        </Row>

        {/* Quick Diagnosis */}
        <Card
          className="quick-diagnose-card"
          title={
            <Space>
              <RocketOutlined style={{ color: '#667eea' }} />
              <span>快速诊断</span>
            </Space>
          }
        >
          <Form layout="inline" onFinish={handleQuickDiagnose} style={{ flexWrap: 'wrap', gap: 8 }}>
            <Form.Item
              name="namespace"
              rules={[{ required: true, message: '请输入 Namespace' }]}
            >
              <Input
                placeholder="Namespace"
                style={{ width: 180 }}
                prefix={<Typography.Text type="secondary" style={{ fontSize: 12 }}>ns</Typography.Text>}
              />
            </Form.Item>
            <Form.Item
              name="pod_name"
              rules={[{ required: true, message: '请输入 Pod 名称' }]}
            >
              <Input
                placeholder="Pod 名称"
                style={{ width: 280 }}
                prefix={<Typography.Text type="secondary" style={{ fontSize: 12 }}>pod</Typography.Text>}
              />
            </Form.Item>
            <Form.Item>
              <Button
                type="primary"
                htmlType="submit"
                icon={<SearchOutlined />}
                loading={diagnose.isPending}
                style={{ background: 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)', border: 'none' }}
              >
                开始诊断
              </Button>
            </Form.Item>
          </Form>
        </Card>

        {/* Recent Tasks */}
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
            <Empty
              description="暂无诊断任务"
              style={{ padding: '40px 0' }}
            >
              <Button type="primary" onClick={() => navigate('/diagnose')}>
                创建第一个诊断
              </Button>
            </Empty>
          )}
        </Card>
      </Space>
    </Spin>
  );
};

export default Dashboard;
