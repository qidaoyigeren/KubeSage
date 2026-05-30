import { Progress, Typography } from 'antd';
import { confidencePercent } from '../utils/format';

const ConfidenceBar = ({ score }: { score: number }) => {
  const pct = confidencePercent(score);
  const color = pct >= 70 ? '#52c41a' : pct >= 40 ? '#faad14' : '#f5576c';

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <Progress
        percent={pct}
        size="small"
        strokeColor={color}
        showInfo={false}
        style={{ width: 80, margin: 0 }}
      />
      <Typography.Text
        style={{ color, fontWeight: 600, fontSize: 13, minWidth: 36 }}
      >
        {pct}%
      </Typography.Text>
    </div>
  );
};

export default ConfidenceBar;
