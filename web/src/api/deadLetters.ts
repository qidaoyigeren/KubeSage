import client from './client';
import type { DiagnosisTask, PaginatedResponse } from './types';

export interface DiagnosisQueueDeadLetter {
  id: number;
  stream: string;
  message_id: string;
  task_id: number;
  payload_json: unknown;
  error: string;
  attempts: number;
  created_at: string;
}

export const listDeadLetters = (page = 1, pageSize = 20) =>
  client.get<never, PaginatedResponse<DiagnosisQueueDeadLetter>>('/dead-letters', {
    params: { page, page_size: pageSize },
  });

export const retryDeadLetter = (id: number) =>
  client.post<never, DiagnosisTask>(`/dead-letters/${id}/retry`);
