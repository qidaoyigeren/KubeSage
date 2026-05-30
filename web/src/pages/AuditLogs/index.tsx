import { Card, Table, Space, Typography, Tag } from 'antd';
import { FileProtectOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { listAuditLogs } from '../../api/runbooks';
import type { AuditLog } from '../../api/runbooks';
import { formatTime } from '../../utils/format';
import { useState } from 'react';

const AuditLogsPage = () => {
  const [page, setPage] = useState(1);
  const { data, isLoading } = useQuery({
    queryKey: ['audit-logs', page],
    queryFn: () => listAuditLogs(page, 20),
  });

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: 60,
    },
    {
      title: 'Actor',
      dataIndex: 'actor',
      key: 'actor',
      width: 120,
      render: (actor: string) => <Tag color="blue">{actor}</Tag>,
    },
    {
      title: 'Action',
      dataIndex: 'action',
      key: 'action',
      width: 200,
      render: (action: string) => {
        let color = 'default';
        if (action.includes('diagnose')) color = 'green';
        if (action.includes('runbook')) color = 'purple';
        if (action.includes('remediation')) color = 'orange';
        if (action.includes('feedback')) color = 'cyan';
        return <Tag color={color}>{action}</Tag>;
      },
    },
    {
      title: 'Resource',
      key: 'resource',
      render: (_: unknown, record: AuditLog) => (
        <Typography.Text>
          {record.resource_kind && record.resource_name
            ? `${record.resource_kind}/${record.resource_name}`
            : '-'}
        </Typography.Text>
      ),
    },
    {
      title: 'Summary',
      dataIndex: 'summary',
      key: 'summary',
      ellipsis: true,
    },
    {
      title: 'Time',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (t: string) => formatTime(t),
    },
  ];

  return (
    <Card
      title={
        <Space>
          <FileProtectOutlined />
          <span>Audit Logs</span>
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

export default AuditLogsPage;
