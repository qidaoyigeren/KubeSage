import { useMutation, useQueryClient } from '@tanstack/react-query';
import { startDiagnosis } from '../api/diagnosis';
import type { DiagnoseRequest } from '../api/types';

export const useDiagnose = () => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: DiagnoseRequest) => startDiagnosis(req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tasks'] });
    },
  });
};
