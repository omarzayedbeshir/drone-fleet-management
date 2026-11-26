import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { App } from './App';

it('returns to sign-in when the registry reports an expired session', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string) => input.endsWith('/auth/me')
    ? new Response(JSON.stringify({ user: { id: 'manager', organisation_id: 'org-a', name: 'Manager', email: 'manager@example.test', role: 'fleet_manager' } }))
    : new Response('{"error":"Session expired"}', { status: 401 })));
  render(<App />);
  await screen.findByRole('heading', { name: 'Sign in to your workspace' });
  expect(screen.getByText('Your session has expired. Sign in again.')).toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'Drone registry' })).not.toBeInTheDocument();
});

it('keeps system administrators outside the tenant registry', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ user: { id: 'admin', organisation_id: null, name: 'Admin', email: 'admin@example.test', role: 'system_administrator' } })));
  vi.stubGlobal('fetch', fetch);
  render(<App />);
  await screen.findByRole('heading', { name: 'Welcome, Admin' });
  expect(screen.getByText('Organisation administration')).toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'Drone registry' })).not.toBeInTheDocument();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('opens audit history for a fleet manager and reveals before and after snapshots', async () => {
  const fetch = vi.fn(async (path: string) => {
    if (path.endsWith('/auth/me')) return Response.json({
      user: { id: 'manager', organisation_id: 'org-a', name: 'Manager', email: 'manager@example.test', role: 'fleet_manager' },
    });
    if (path.includes('/drones?')) return Response.json({ drones: [], pagination: { limit: 20, offset: 0, has_more: false } });
    if (path === '/api/v1/audit-events') return Response.json({
      events: [{
        id: '1', actor_id: 'manager', resource_type: 'mission', resource_id: 'mission-a', action: 'updated',
        before_data: { location: 'Old field' }, after_data: { location: 'New field' }, created_at: '2026-11-26T11:00:00Z',
      }],
      next_before_id: null,
    });
    throw new Error(`Unexpected route: ${path}`);
  });
  vi.stubGlobal('fetch', fetch);
  render(<App />);
  const actor = userEvent.setup();
  await actor.click(await screen.findByRole('button', { name: 'Audit history' }));
  await actor.click(await screen.findByText('mission · updated'));
  expect(screen.getByText(/Old field/)).toBeVisible();
  expect(screen.getByText(/New field/)).toBeVisible();
  expect(screen.queryByLabelText('Organisation ID')).toBeNull();
});
