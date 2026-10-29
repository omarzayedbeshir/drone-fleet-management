export const AUTH_ACTIVITY_EVENT = 'aerosync:auth-activity';

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

export async function request<T>(path: string, body?: unknown): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/v1/auth/${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      credentials: 'same-origin',
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(10000),
    });
  } catch {
    throw new ApiError(0, 'Unable to reach AeroSync. Check your connection and try again.');
  }
  if ((path === 'me' || path === 'password') && response.status !== 401 && response.status < 500) {
    window.dispatchEvent(new Event(AUTH_ACTIVITY_EVENT));
  }
  if (!response.ok) {
    const data = await response.json().catch(() => null) as { error?: string } | null;
    throw new ApiError(response.status, data?.error ?? 'Something went wrong. Please try again.');
  }
  if (response.status === 204) return undefined as T;
  return await response.json() as T;
}
