import { useQuery } from '@tanstack/react-query';
import { listTasks } from '../api/tasks';

export const useTasks = (page = 1, pageSize = 20) =>
  useQuery({
    queryKey: ['tasks', page, pageSize],
    queryFn: () => listTasks(page, pageSize),
    refetchInterval: (query) => {
      const data = query.state.data;
      if (data?.items?.some((t) => t.status === 'running' || t.status === 'pending')) {
        return 5000;
      }
      return false;
    },
  });
