import { useState } from 'react';
import { Button, Typography, message } from 'antd';
import { CopyOutlined, CheckOutlined } from '@ant-design/icons';

interface JsonViewerProps {
  data: unknown;
  maxHeight?: number;
}

const JsonViewer = ({ data, maxHeight = 400 }: JsonViewerProps) => {
  const [copied, setCopied] = useState(false);

  if (data === null || data === undefined) {
    return <Typography.Text type="secondary">-</Typography.Text>;
  }

  const formatted = typeof data === 'string' ? data : JSON.stringify(data, null, 2);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(formatted);
      setCopied(true);
      message.success('已复制到剪贴板');
      setTimeout(() => setCopied(false), 2000);
    } catch {
      message.error('复制失败');
    }
  };

  return (
    <div className="json-viewer-container">
      <Button
        type="text"
        size="small"
        icon={copied ? <CheckOutlined /> : <CopyOutlined />}
        onClick={handleCopy}
        className="copy-btn"
      />
      <pre style={{ maxHeight }}>{formatted}</pre>
    </div>
  );
};

export default JsonViewer;
