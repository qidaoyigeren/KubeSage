import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getTask, taskEventsURL } from '../api/tasks';
import { streamSSE } from '../api/stream';
import type { DiagnosisTask } from '../api/types';

const terminalTask = (task: DiagnosisTask) =>
  task.status === 'success' || task.status === 'failed';

export const useTask = (id: number) => {
  const queryClient = useQueryClient();
  const queryKey = ['task', id] as const;

  useEffect(() => {
    if (!Number.isFinite(id) || id <= 0) {
      return;
    }
    let stopped = false;
    let terminal = false;
    let retryTimer: number | undefined;
    let controller: AbortController | null = null;

    const run = async () => {
      controller = new AbortController();
      try {
        await streamSSE(taskEventsURL(id), {
          task: (raw) => {
            const task = JSON.parse(raw) as DiagnosisTask;
            queryClient.setQueryData(queryKey, task);
            if (terminalTask(task)) {
              terminal = true;
              controller?.abort();
            }
          },
        }, controller.signal);
      } catch {
        // React Query keeps the last successful snapshot while the stream reconnects.
      }
      if (!stopped && !terminal) {
        retryTimer = window.setTimeout(run, 2500);
      }
    };

    void run();
    return () => {
      stopped = true;
      terminal = true;
      controller?.abort();
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
      }
    };
  }, [id, queryClient]);

  return useQuery({
    queryKey,
    queryFn: () => getTask(id),
    enabled: Number.isFinite(id) && id > 0,
  });
};
