import { Table, Tag, Typography, Empty } from 'antd';
import type { Evidence, Severity } from '../../../api/types';
import { formatTime } from '../../../utils/format';

const severityConfig: Record<Severity, { color: string; label: string }> = {
  critical: { color: 'red', label: '严重' },
  warning: { color: 'orange', label: '警告' },
  info: { color: 'blue', label: '信息' },
  debug: { color: 'default', label: '调试' },
};

const EvidenceTab = ({ evidences }: { evidences: Evidence[] }) => {
  if (!evidences || evidences.length === 0) {
    return <Empty description="暂无证据数据" />;
  }

  const columns = [
    {
      title: '来源',
      dataIndex: 'source_type',
      key: 'source_type',
      width: 130,
      render: (t: string) => <Tag color="geekblue" style={{ borderRadius: 4 }}>{t}</Tag>,
    },
    {
      title: '标题',
      dataIndex: 'title',
      key: 'title',
      width: 200,
      ellipsis: true,
      render: (t: string) => <Typography.Text strong>{t}</Typography.Text>,
    },
    {
      title: '严重度',
      dataIndex: 'severity',
      key: 'severity',
      width: 80,
      render: (s: Severity) => {
        const config = severityConfig[s] || { color: 'default', label: s };
        return <Tag color={config.color} style={{ borderRadius: 4 }}>{config.label}</Tag>;
      },
    },
    {
      title: '内容预览',
      dataIndex: 'content',
      key: 'content',
      ellipsis: true,
      render: (text: string) => (
        <Typography.Text
          type="secondary"
          ellipsis={{ tooltip: text }}
          style={{ maxWidth: 400, display: 'inline-block', fontSize: 13 }}
        >
          {text}
        </Typography.Text>
      ),
    },
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 160,
      render: (t: string) => (
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {formatTime(t)}
        </Typography.Text>
      ),
    },
  ];

  return (
    <Table
      dataSource={evidences}
      columns={columns}
      rowKey="id"
      size="middle"
      expandable={{
        expandedRowRender: (record) => (
          <pre
            style={{
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              maxHeight: 300,
              overflow: 'auto',
              margin: 0,
              fontSize: 12,
              background: '#1e1e2e',
              color: '#cdd6f4',
              padding: 16,
              borderRadius: 8,
              fontFamily: "'SF Mono', 'Fira Code', Menlo, Consolas, monospace",
            }}
          >
            {record.content}
          </pre>
        ),
      }}
    />
  );
};

export default EvidenceTab;
