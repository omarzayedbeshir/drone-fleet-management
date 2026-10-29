import { render, screen } from '@testing-library/react';
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
