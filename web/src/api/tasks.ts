import client from './client';
import type { DiagnosisTask, FeedbackRequest, PaginatedResponse } from './types';

export const listTasks = (page = 1, pageSize = 20) =>
  client.get<never, PaginatedResponse<DiagnosisTask>>('/diagnose/tasks', {
    params: { page, page_size: pageSize },
  });

export const getTask = (id: number) =>
  client.get<never, DiagnosisTask>(`/diagnose/tasks/${id}`);

export const taskEventsURL = (id: number) =>
  `/api/v1/diagnose/tasks/${id}/events`;

export const taskListEventsURL = (page = 1, pageSize = 20) =>
  `/api/v1/diagnose/task-events?page=${encodeURIComponent(page)}&page_size=${encodeURIComponent(pageSize)}`;

export const submitFeedback = (id: number, payload: FeedbackRequest) =>
  client.post<never, unknown>(`/diagnose/tasks/${id}/feedback`, payload);
