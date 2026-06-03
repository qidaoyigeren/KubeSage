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
      message.success('已记录人工确认');
      queryClient.invalidateQueries({ queryKey: ['pending-approvals'] });
    },
    onError: (err: Error) => message.error(err.message),
  });

  const rejectMutation = useMutation({
    mutationFn: ({ id, reason }: { id: number; reason: string }) => rejectRemediation(id, reason),
    onSuccess: () => {
      message.success('修复动作已驳回');
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
      title: '任务 ID',
      dataIndex: 'task_id',
      key: 'task_id',
      width: 80,
      render: (taskId: number) => (
        <Typography.Link href={`/tasks/${taskId}`}>{taskId}</Typography.Link>
      ),
    },
    {
      title: '动作',
      dataIndex: 'action_id',
      key: 'action_id',
      width: 160,
    },
    {
      title: '风险',
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
      title: '命令预览',
      dataIndex: 'command_preview',
      key: 'command_preview',
      ellipsis: true,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 160,
      render: (t: string) => formatTime(t),
    },
    {
      title: '操作',
      key: 'action',
      width: 180,
      render: (_: unknown, record: RemediationExecution) => (
        <Space>
          <Popconfirm
            title="确认已读并转人工处理？"
            onConfirm={() => approveMutation.mutate(record.id)}
          >
            <Button type="primary" size="small" icon={<CheckCircleOutlined />}>
              确认已读
            </Button>
          </Popconfirm>
          <Popconfirm
            title="确认驳回该修复动作？"
            onConfirm={() => rejectMutation.mutate({ id: record.id, reason: '诊断员驳回' })}
          >
            <Button danger size="small" icon={<CloseCircleOutlined />}>
              驳回
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
          <span>待人工确认修复动作</span>
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
