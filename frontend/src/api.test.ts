import { describe, expect, it, vi } from 'vitest';
import { apiRequest, AUTH_ACTIVITY_EVENT, AUTH_EXPIRED_EVENT, request } from './api';

describe('API transport', () => {
  it('uses authenticated PATCH requests and tracks registry activity', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ drone: {} })));
    vi.stubGlobal('fetch', fetch);
    const activity = vi.fn(); window.addEventListener(AUTH_ACTIVITY_EVENT, activity);
    try {
      await apiRequest('/drones/id', { method: 'PATCH', body: { name: 'Updated' } });
      expect(fetch).toHaveBeenCalledWith('/api/v1/drones/id', expect.objectContaining({ method: 'PATCH', credentials: 'same-origin', body: '{"name":"Updated"}' }));
      expect(activity).toHaveBeenCalledOnce();
    } finally { window.removeEventListener(AUTH_ACTIVITY_EVENT, activity); }
  });
  it('announces expired registry sessions but does not treat incorrect login credentials as expiry', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(new Response('{"error":"Sign in again"}', { status: 401 }))));
    const expired = vi.fn(); window.addEventListener(AUTH_EXPIRED_EVENT, expired);
    try {
      await expect(apiRequest('/drones')).rejects.toMatchObject({ status: 401 });
      expect(expired).toHaveBeenCalledOnce();
      await expect(request('login', { email: 'a@example.test', password: 'incorrect' })).rejects.toMatchObject({ status: 401 });
      expect(expired).toHaveBeenCalledOnce();
    } finally { window.removeEventListener(AUTH_EXPIRED_EVENT, expired); }
  });
});
