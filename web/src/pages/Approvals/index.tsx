import { Card, Table, Button, Space, Tag, message, Popconfirm, Typography } from 'antd';
import { CheckCircleOutlined, CloseCircleOutlined, SafetyOutlined } from '@ant-design/icons';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import client from '../../api/client';
import { formatTime } from '../../utils/format';

interface RemediationExecution {
  id: number;
  task_id: number;
  action_id: string;
  status: string;
  risk_level: string;
  command_preview: string;
  approval_by: string;
  created_at: string;
  updated_at: string;
}

const listPendingApprovals = () =>
  client.get<never, RemediationExecution[]>('/remediation/pending');

const approveRemediation = (id: number) =>
  client.post<never, unknown>(`/remediation/${id}/approve`);

const rejectRemediation = (id: number, reason: string) =>
  client.post<never, unknown>(`/remediation/${id}/reject`, { reason });

const ApprovalsPage = () => {
  const queryClient = useQueryClient();

  const { data: pending, isLoading } = useQuery({
    queryKey: ['pending-approvals'],
    queryFn: listPendingApprovals,
  });

  const approveMutation = useMutation({
    mutationFn: (id: number) => approveRemediation(id),
    onSuccess: () => {
      message.success('Remediation approved');
      queryClient.invalidateQueries({ queryKey: ['pending-approvals'] });
    },
    onError: (err: Error) => message.error(err.message),
  });

  const rejectMutation = useMutation({
    mutationFn: ({ id, reason }: { id: number; reason: string }) => rejectRemediation(id, reason),
    onSuccess: () => {
      message.success('Remediation rejected');
      queryClient.invalidateQueries({ queryKey: ['pending-approvals'] });
    },
    onError: (err: Error) => message.error(err.message),
  });

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: 60,
    },
    {
      title: 'Task ID',
      dataIndex: 'task_id',
      key: 'task_id',
      width: 80,
      render: (taskId: number) => (
        <Typography.Link href={`/tasks/${taskId}`}>{taskId}</Typography.Link>
      ),
    },
    {
      title: 'Action',
      dataIndex: 'action_id',
      key: 'action_id',
      width: 160,
    },
    {
      title: 'Risk',
      dataIndex: 'risk_level',
      key: 'risk_level',
      width: 80,
      render: (risk: string) => {
        const colorMap: Record<string, string> = {
          low: 'green',
          medium: 'orange',
          high: 'red',
        };
        return <Tag color={colorMap[risk] || 'default'}>{risk}</Tag>;
      },
    },
    {
      title: 'Command Preview',
      dataIndex: 'command_preview',
      key: 'command_preview',
      ellipsis: true,
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
      width: 180,
      render: (_: unknown, record: RemediationExecution) => (
        <Space>
          <Popconfirm
            title="Approve this remediation?"
            onConfirm={() => approveMutation.mutate(record.id)}
          >
            <Button type="primary" size="small" icon={<CheckCircleOutlined />}>
              Approve
            </Button>
          </Popconfirm>
          <Popconfirm
            title="Reject this remediation?"
            onConfirm={() => rejectMutation.mutate({ id: record.id, reason: 'Rejected by operator' })}
          >
            <Button danger size="small" icon={<CloseCircleOutlined />}>
              Reject
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card
      title={
        <Space>
          <SafetyOutlined />
          <span>Pending Approvals</span>
        </Space>
      }
    >
      <Table
        columns={columns}
        dataSource={pending || []}
        rowKey="id"
        loading={isLoading}
        pagination={{ pageSize: 20 }}
      />
    </Card>
  );
};

export default ApprovalsPage;
