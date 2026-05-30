import { Tag } from 'antd';
import type { RiskLevel } from '../api/types';

const riskConfig: Record<string, { color: string; label: string }> = {
  low: { color: 'green', label: '低风险' },
  medium: { color: 'orange', label: '中风险' },
  high: { color: 'red', label: '高风险' },
  forbidden: { color: 'volcano', label: '禁止' },
};

const RiskLevelTag = ({ level }: { level: RiskLevel }) => {
  const config = riskConfig[level] || { color: 'default', label: level };
  return <Tag color={config.color}>{config.label}</Tag>;
};

export default RiskLevelTag;
