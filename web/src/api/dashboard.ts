import client from './client';
import type { DashboardSummary, DashboardTrendPoint } from './types';

export const getDashboardSummary = (days = 7) =>
  client.get<never, DashboardSummary>('/dashboard/summary', { params: { days } });

export const getDashboardTrends = (days = 14) =>
  client.get<never, DashboardTrendPoint[]>('/dashboard/trends', { params: { days } });
