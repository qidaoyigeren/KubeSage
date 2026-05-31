import { useState } from 'react';
import { Card, Table, Button, Space, Tag, message, Typography } from 'antd';
import { DatabaseOutlined, ReloadOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { listDeadLetters, retryDeadLetter } from '../../api/deadLetters';
import type { DiagnosisQueueDeadLetter } from '../../api/deadLetters';
import { formatTime } from '../../utils/format';

const DeadLettersPage = () => {
  const [page, setPage] = useState(1);
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const { data, isLoading } = useQuery({
    queryKey: ['dead-letters', page],
    queryFn: () => listDeadLetters(page, 20),
  });

  const retryMutation = useMutation({
    mutationFn: (id: number) => retryDeadLetter(id),
    onSuccess: (task) => {
      message.success(`Retry scheduled as task #${task.id}`);
      queryClient.invalidateQueries({ queryKey: ['dead-letters'] });
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      navigate(`/tasks/${task.id}`);
    },
    onError: (err: Error) => message.error(err.message),
  });

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: 72,
      render: (id: number) => <Typography.Text code>#{id}</Typography.Text>,
    },
    {
      title: 'Task',
      dataIndex: 'task_id',
      key: 'task_id',
      width: 90,
      render: (taskId: number) =>
        taskId ? <Typography.Link href={`/tasks/${taskId}`}>#{taskId}</Typography.Link> : '-',
    },
    {
      title: 'Stream',
      dataIndex: 'stream',
      key: 'stream',
      width: 180,
      render: (stream: string) => <Tag color="blue">{stream}</Tag>,
    },
    {
      title: 'Attempts',
      dataIndex: 'attempts',
      key: 'attempts',
      width: 90,
    },
    {
      title: 'Error',
      dataIndex: 'error',
      key: 'error',
      ellipsis: true,
      render: (error: string) => <Typography.Text type="danger">{error}</Typography.Text>,
    },
    {
      title: 'Payload',
      dataIndex: 'payload_json',
      key: 'payload_json',
      width: 260,
      ellipsis: true,
      render: (payload: unknown) => (
        <Typography.Text code ellipsis>
          {JSON.stringify(payload)}
        </Typography.Text>
      ),
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 160,
      render: (t: string) => formatTime(t),
    },
    {
      title: 'Action',
      key: 'action',
      width: 120,
      render: (_: unknown, record: DiagnosisQueueDeadLetter) => (
        <Button
          size="small"
          icon={<ReloadOutlined />}
          loading={retryMutation.isPending}
          onClick={() => retryMutation.mutate(record.id)}
        >
          Retry
        </Button>
      ),
    },
  ];

  return (
    <Card
      title={
        <Space>
          <DatabaseOutlined />
          <span>Dead Letter Queue</span>
        </Space>
      }
    >
      <Table
        columns={columns}
        dataSource={data?.items || []}
        rowKey="id"
        loading={isLoading}
        pagination={{
          current: page,
          total: data?.total || 0,
          pageSize: 20,
          onChange: setPage,
          showTotal: (total) => `Total ${total} entries`,
        }}
      />
    </Card>
  );
};

export default DeadLettersPage;
