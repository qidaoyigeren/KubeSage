import client from './client';
import type { DiagnosisTask, DiagnoseRequest } from './types';

export const startDiagnosis = (req: DiagnoseRequest) =>
  client.post<never, DiagnosisTask>('/diagnose/pod', req);
