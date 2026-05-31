import client from './client';

export type UserRole = 'viewer' | 'operator' | 'admin';

export interface CurrentUser {
  actor: string;
  role: UserRole;
  allowed_namespaces: string[];
}

const roleRank: Record<UserRole, number> = {
  viewer: 1,
  operator: 2,
  admin: 3,
};

export const getCurrentUser = () => client.get<never, CurrentUser>('/me');

export const canAccessRole = (role: UserRole | undefined, required: UserRole) =>
  roleRank[role || 'viewer'] >= roleRank[required];
