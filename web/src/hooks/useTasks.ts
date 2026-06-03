import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { listTasks, taskListEventsURL } from '../api/tasks';
import { streamSSE } from '../api/stream';
import type { DiagnosisTask, PaginatedResponse } from '../api/types';

export const useTasks = (page = 1, pageSize = 20) => {
  const queryClient = useQueryClient();
  const queryKey = ['tasks', page, pageSize] as const;

  useEffect(() => {
    let stopped = false;
    let retryTimer: number | undefined;
    let controller: AbortController | null = null;

    const run = async () => {
      controller = new AbortController();
      try {
        await streamSSE(taskListEventsURL(page, pageSize), {
          tasks: (raw) => {
            const data = JSON.parse(raw) as PaginatedResponse<DiagnosisTask>;
            queryClient.setQueryData(queryKey, data);
          },
        }, controller.signal);
      } catch {
        // Keep the latest page cache while reconnecting the stream.
      }
      if (!stopped) {
        retryTimer = window.setTimeout(run, 2500);
      }
    };

    void run();
    return () => {
      stopped = true;
      controller?.abort();
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
      }
    };
  }, [page, pageSize, queryClient]);

  return useQuery({
    queryKey,
    queryFn: () => listTasks(page, pageSize),
  });
};
