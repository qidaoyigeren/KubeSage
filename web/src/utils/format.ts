export const formatTime = (iso: string | null | undefined): string => {
  if (!iso) return '-';
  return new Date(iso).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
};

export const formatDuration = (ms: number): string => {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60000);
  const s = ((ms % 60000) / 1000).toFixed(0);
  return `${m}m${s}s`;
};

export const formatTaskDuration = (
  start: string,
  end: string | null | undefined,
): string => {
  if (!end) return '进行中...';
  const ms = new Date(end).getTime() - new Date(start).getTime();
  return formatDuration(ms);
};

export const confidencePercent = (score: number): number =>
  Math.round(score * 100);
