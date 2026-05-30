import { Tag } from 'antd';
import {
  ClockCircleOutlined,
  SyncOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
} from '@ant-design/icons';
import type { TaskStatus } from '../api/types';

const statusConfig: Record<
  TaskStatus,
  { color: string; label: string; icon: React.ReactNode }
> = {
  pending: {
    color: 'default',
    label: '等待中',
    icon: <ClockCircleOutlined />,
  },
  running: {
    color: 'processing',
    label: '运行中',
    icon: <SyncOutlined spin />,
  },
  success: {
    color: 'success',
    label: '成功',
    icon: <CheckCircleOutlined />,
  },
  failed: {
    color: 'error',
    label: '失败',
    icon: <CloseCircleOutlined />,
  },
};

const StatusTag = ({ status }: { status: TaskStatus }) => {
  const config = statusConfig[status] || { color: 'default', label: status, icon: null };
  return (
    <Tag
      color={config.color}
      icon={config.icon}
      style={{ borderRadius: 12, padding: '0 10px' }}
    >
      {config.label}
    </Tag>
  );
};

export default StatusTag;
