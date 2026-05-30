import client from './client';

export interface Runbook {
  id: number;
  fault_type: string;
  title: string;
  content: string;
  hints_json: string;
  version: number;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
}

export interface RunbookRequest {
  fault_type: string;
  title: string;
  content: string;
  hints?: Record<string, unknown>;
}

export interface AuditLog {
  id: number;
  actor: string;
  action: string;
  namespace: string;
  resource_kind: string;
  resource_name: string;
  task_id?: number;
  summary: string;
  metadata_json: string;
  created_at: string;
}

export const listRunbooks = () =>
  client.get<never, Runbook[]>('/runbooks');

export const createRunbook = (data: RunbookRequest) =>
  client.post<never, Runbook>('/runbooks', data);

export const updateRunbook = (id: number, data: RunbookRequest) =>
  client.put<never, Runbook>(`/runbooks/${id}`, data);

export const listAuditLogs = (page = 1, pageSize = 20) =>
  client.get<never, { items: AuditLog[]; total: number; page: number; page_size: number }>('/audit-logs', {
    params: { page, page_size: pageSize },
  });
