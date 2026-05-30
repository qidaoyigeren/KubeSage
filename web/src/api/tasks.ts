import client from './client';
import type { DiagnosisTask, PaginatedResponse } from './types';

export const listTasks = (page = 1, pageSize = 20) =>
  client.get<never, PaginatedResponse<DiagnosisTask>>('/diagnose/tasks', {
    params: { page, page_size: pageSize },
  });

export const getTask = (id: number) =>
  client.get<never, DiagnosisTask>(`/diagnose/tasks/${id}`);
