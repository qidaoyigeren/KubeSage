import { useQuery } from '@tanstack/react-query';
import { getCurrentUser } from '../api/auth';

export const useCurrentUser = () =>
  useQuery({
    queryKey: ['current-user'],
    queryFn: getCurrentUser,
    retry: false,
  });
