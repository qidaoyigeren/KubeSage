type SSEHandler = (data: string) => void;

export const streamSSE = async (
  url: string,
  handlers: Record<string, SSEHandler>,
  signal: AbortSignal,
) => {
  const headers: Record<string, string> = {
    Accept: 'text/event-stream',
  };
  const token = localStorage.getItem('kubesage_token');
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  const response = await fetch(url, { headers, signal });
  if (!response.ok || !response.body) {
    throw new Error(`stream failed: ${response.status}`);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true });
    buffer = buffer.replace(/\r\n/g, '\n');
    let boundary = buffer.indexOf('\n\n');
    while (boundary >= 0) {
      const block = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      dispatchSSEBlock(block, handlers);
      boundary = buffer.indexOf('\n\n');
    }
  }
};

const dispatchSSEBlock = (block: string, handlers: Record<string, SSEHandler>) => {
  let event = 'message';
  const data: string[] = [];
  for (const line of block.split(/\r?\n/)) {
    if (!line || line.startsWith(':')) {
      continue;
    }
    if (line.startsWith('event:')) {
      event = line.slice('event:'.length).trim();
      continue;
    }
    if (line.startsWith('data:')) {
      data.push(line.slice('data:'.length).trimStart());
    }
  }
  const handler = handlers[event];
  if (handler && data.length > 0) {
    handler(data.join('\n'));
  }
};
