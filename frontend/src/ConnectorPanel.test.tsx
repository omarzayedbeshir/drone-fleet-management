import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ConnectorPanel } from './ConnectorPanel';
import type { ConnectorView } from './connectors';
const profiles = [{ id: 'demo', label: 'Delta recording', source: 'csv' as const, vendor: 'dji', simulated: true }];
const empty: ConnectorView = { connector: null, profiles };
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
function connector(enabled = true): ConnectorView { return { profiles, connector: { drone_id: 'D1', profile_id: 'demo', enabled, generation: 1, state: enabled ? 'running' : 'disabled', updated_at: '', runtime_updated_at: null, health: { connected: enabled, ready: enabled, last_frame: '', last_emitted: '', accepted: 3, rejected: 0, dropped: 0 } } }; }
afterEach(() => vi.useRealTimers());
it('enables a saved profile and disables the configured profile', async () => {
  let view = empty;
  const fetch = vi.fn(async (_url: string, init: RequestInit = {}) => {
    if (init.method === 'PUT') view = connector(JSON.parse(String(init.body)).enabled);
    return response(view);
  }); vi.stubGlobal('fetch', fetch);
  render(<ConnectorPanel droneId="D1" canManage />);
  await screen.findByText('Not configured');
  fireEvent.click(screen.getByRole('button', { name: 'Enable connection' }));
  await screen.findByText('Running');
  expect(fetch.mock.calls.find(([, init]) => init?.method === 'PUT')?.[1]?.body).toBe(JSON.stringify({ profile_id: 'demo', enabled: true }));
  fireEvent.click(screen.getByRole('button', { name: 'Disable connection' }));
  await screen.findByText('Disabled');
  expect(screen.getByRole('button', { name: 'Disable connection' })).toBeDisabled();
});
it('shows health to readers without connection controls', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response(connector())));
  render(<ConnectorPanel droneId="D1" canManage={false} />);
  await screen.findByText('Running');
  expect(screen.getByText(/Simulated source/)).toBeInTheDocument();
  expect(screen.getByText(/Accepted: 3/)).toBeInTheDocument();
  expect(screen.queryByLabelText('Connection profile')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Disable connection' })).not.toBeInTheDocument();
});
it('polls while open and stops when closed', async () => {
  vi.useFakeTimers(); const fetch = vi.fn(async () => response(empty)); vi.stubGlobal('fetch', fetch);
  let unmount!: () => void;
  await act(async () => { ({ unmount } = render(<ConnectorPanel droneId="D1" canManage />)); });
  expect(fetch).toHaveBeenCalledTimes(1);
  await act(async () => vi.advanceTimersByTime(5000)); expect(fetch).toHaveBeenCalledTimes(2);
  unmount(); await act(async () => vi.advanceTimersByTime(10000)); expect(fetch).toHaveBeenCalledTimes(2);
});
it('retains controls and reports a failed save', async () => {
  vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit = {}) => init.method === 'PUT' ? response({ error: 'Profile unavailable' }, 400) : response(empty)));
  render(<ConnectorPanel droneId="D1" canManage />); await screen.findByText('Not configured');
  fireEvent.click(screen.getByRole('button', { name: 'Enable connection' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Profile unavailable');
  await waitFor(() => expect(screen.getByRole('button', { name: 'Enable connection' })).toBeEnabled());
});
