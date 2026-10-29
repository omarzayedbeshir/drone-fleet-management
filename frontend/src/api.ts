export const AUTH_ACTIVITY_EVENT = 'aerosync:auth-activity';
export const AUTH_EXPIRED_EVENT = 'aerosync:auth-expired';

export type Role = 'system_administrator' | 'fleet_manager' | 'pilot' | 'maintenance_technician';
export interface User {
  id: string;
  organisation_id: string | null;
  name: string;
  email: string;
  role: Role;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE';
  body?: unknown;
  signal?: AbortSignal;
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  let response: Response;
  try {
    const timeout = AbortSignal.timeout(10000);
    response = await fetch(`/api/v1${path}`, {
      method: options.method ?? 'GET',
      credentials: 'same-origin',
      headers: options.body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal ? AbortSignal.any([options.signal, timeout]) : timeout,
    });
  } catch (error) {
    if (options.signal?.aborted) throw error;
    throw new ApiError(0, 'Unable to reach AeroSync. Check your connection and try again.');
  }
  if (response.status === 401 && path !== '/auth/login' && path !== '/auth/me') {
    window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
  }
  if (path !== '/auth/login' && path !== '/auth/logout' && response.status !== 401 && response.status < 500) {
    window.dispatchEvent(new Event(AUTH_ACTIVITY_EVENT));
  }
  if (!response.ok) {
    const data = await response.json().catch(() => null) as { error?: string } | null;
    throw new ApiError(response.status, data?.error ?? 'Something went wrong. Please try again.');
  }
  if (response.status === 204) return undefined as T;
  return await response.json() as T;
}

// Retain the small authentication helper while sharing transport/session handling.
export function request<T>(path: string, body?: unknown): Promise<T> {
  return apiRequest<T>(`/auth/${path}`, { method: body === undefined ? 'GET' : 'POST', body });
}
