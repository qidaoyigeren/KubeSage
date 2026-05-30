import { useQuery } from '@tanstack/react-query';
import { getTask } from '../api/tasks';

export const useTask = (id: number) =>
  useQuery({
    queryKey: ['task', id],
    queryFn: () => getTask(id),
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      if (status === 'running' || status === 'pending') {
        return 3000;
      }
      return false;
    },
  });
