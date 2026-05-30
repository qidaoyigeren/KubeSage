import { useState } from 'react';
import { Table, Card, Select, Input, Space, Button, Typography, Tag, Empty, Tooltip } from 'antd';
import { ReloadOutlined, PlusOutlined, FilterOutlined, ArrowRightOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useTasks } from '../../hooks/useTasks';
import StatusTag from '../../components/StatusTag';
import ConfidenceBar from '../../components/ConfidenceBar';
import { formatTime, formatTaskDuration } from '../../utils/format';
import type { DiagnosisTask, TaskStatus } from '../../api/types';

const TaskList = () => {
  const navigate = useNavigate();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [statusFilter, setStatusFilter] = useState<TaskStatus | ''>('');
  const [search, setSearch] = useState('');

  const { data, isLoading, isRefetching, refetch } = useTasks(page, pageSize);

  const tasks = data?.items || [];
  const total = data?.total || 0;

  const filtered = tasks.filter((t) => {
    if (statusFilter && t.status !== statusFilter) return false;
    if (search) {
      const q = search.toLowerCase();
      if (
        !t.namespace.toLowerCase().includes(q) &&
        !t.pod_name.toLowerCase().includes(q)
      )
        return false;
    }
    return true;
  });

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
      width: 120,
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
      render: (t: string) =>
        t ? <Tag color="geekblue">{t}</Tag> : <Typography.Text type="secondary">-</Typography.Text>,
    },
    {
      title: '告警',
      dataIndex: 'alert_name',
      key: 'alert_name',
      width: 140,
      ellipsis: true,
      render: (t: string) =>
        t ? (
          <Tooltip title={t}>
            <Tag>{t.replace('KubePod', '')}</Tag>
          </Tooltip>
        ) : (
          '-'
        ),
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
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      {/* Header with actions */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          诊断任务
          {total > 0 && (
            <Typography.Text type="secondary" style={{ fontSize: 14, fontWeight: 400, marginLeft: 8 }}>
              共 {total} 条
            </Typography.Text>
          )}
        </Typography.Title>
        <Space>
          <Button
            icon={<ReloadOutlined />}
            onClick={() => refetch()}
            loading={isRefetching}
          >
            刷新
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => navigate('/diagnose')}
            style={{ background: 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)', border: 'none' }}
          >
            新建诊断
          </Button>
        </Space>
      </div>

      {/* Filters + Table */}
      <Card className="task-list-card" bordered={false}>
        <Space style={{ marginBottom: 16 }} wrap>
          <FilterOutlined style={{ color: '#8c8c8c' }} />
          <Select
            placeholder="状态"
            allowClear
            style={{ width: 130 }}
            value={statusFilter || undefined}
            onChange={(v) => setStatusFilter(v || '')}
            options={[
              { value: 'pending', label: '等待中' },
              { value: 'running', label: '运行中' },
              { value: 'success', label: '成功' },
              { value: 'failed', label: '失败' },
            ]}
          />
          <Input.Search
            placeholder="搜索 Namespace / Pod"
            allowClear
            style={{ width: 260 }}
            onSearch={(v) => setSearch(v)}
            onChange={(e) => {
              if (!e.target.value) setSearch('');
            }}
          />
        </Space>

        {filtered.length > 0 || isLoading ? (
          <Table
            dataSource={filtered}
            columns={columns}
            rowKey="id"
            loading={isLoading}
            size="middle"
            pagination={{
              current: page,
              pageSize,
              total,
              showSizeChanger: true,
              showTotal: (t) => `共 ${t} 条`,
              onChange: (p, ps) => {
                setPage(p);
                setPageSize(ps);
              },
            }}
            onRow={(record) => ({
              onClick: () => navigate(`/tasks/${record.id}`),
              style: { cursor: 'pointer' },
            })}
          />
        ) : (
          <Empty description="暂无诊断任务" style={{ padding: '60px 0' }}>
            <Button type="primary" onClick={() => navigate('/diagnose')}>
              创建第一个诊断
            </Button>
          </Empty>
        )}
      </Card>
    </Space>
  );
};

export default TaskList;
