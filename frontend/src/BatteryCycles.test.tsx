import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { BatteryCycles } from './BatteryCycles';
const initial = { total: 5, source: 'unknown', recorded_at: null, revision: 0, note: '' };
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
afterEach(() => vi.useRealTimers());
it('identifies an unverified baseline and gives readers no recording control', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ battery_cycles: initial })));
  render(<BatteryCycles droneId="D1" vendor="ardupilot" canRecord={false} />);
  await screen.findByText(/Unverified baseline/);
  expect(screen.getByText(/does not measure battery cycles/)).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Record cycle baseline' })).not.toBeInTheDocument();
});
it('records a referenced reading with optimistic concurrency values', async () => {
  let value = initial;
  const fetch = vi.fn(async (_url: string, init: RequestInit = {}) => {
    if (init.method === 'PUT') { value = { ...initial, total: 7, source: 'manual', revision: 1, note: 'Service log B-7' }; }
    return response({ battery_cycles: value });
  }); vi.stubGlobal('fetch', fetch);
  render(<BatteryCycles droneId="D1" canRecord />);
  await screen.findByText(/Unverified baseline/);
  fireEvent.click(screen.getByRole('button', { name: 'Record cycle baseline' }));
  fireEvent.change(screen.getByLabelText('Verified cumulative cycles'), { target: { value: '7' } });
  fireEvent.change(screen.getByLabelText('Reading source or reference'), { target: { value: 'Service log B-7' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save baseline reading' }));
  await screen.findByText(/Documented manual reading/);
  const put = fetch.mock.calls.find(([, init]) => init?.method === 'PUT');
  expect(JSON.parse(String(put?.[1]?.body))).toEqual({ total: 7, expected_total: 5, expected_revision: 0, reason: 'Service log B-7' });
  expect(screen.getByText('Source/reference: Service log B-7')).toBeInTheDocument();
});
it('requires a reload after a concurrent change without discarding input immediately', async () => {
  let changed = false;
  vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit = {}) => {
    if (init.method === 'PUT') { changed = true; return response({ error: 'Cycle history changed. Reload and verify.' }, 409); }
    return response({ battery_cycles: { ...initial, total: changed ? 6 : 5 } });
  }));
  render(<BatteryCycles droneId="D1" canRecord />); await screen.findByText(/Unverified baseline/);
  fireEvent.click(screen.getByRole('button', { name: 'Record cycle baseline' }));
  fireEvent.change(screen.getByLabelText('Verified cumulative cycles'), { target: { value: '7' } });
  fireEvent.change(screen.getByLabelText('Reading source or reference'), { target: { value: 'Service log B-7' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save baseline reading' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Cycle history changed');
  expect(screen.getByLabelText('Verified cumulative cycles')).toHaveValue(7);
  expect(screen.getByRole('button', { name: 'Save baseline reading' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Reload cycle reading' }));
  await waitFor(() => expect(screen.getByText('6', { selector: 'strong' })).toBeInTheDocument());
});
it('rejects a decreasing reading before sending a write', async () => {
  const fetch = vi.fn(async () => response({ battery_cycles: initial })); vi.stubGlobal('fetch', fetch);
  render(<BatteryCycles droneId="D1" canRecord />); await screen.findByText(/Unverified baseline/);
  fireEvent.click(screen.getByRole('button', { name: 'Record cycle baseline' }));
  fireEvent.change(screen.getByLabelText('Verified cumulative cycles'), { target: { value: '4' } });
  fireEvent.change(screen.getByLabelText('Reading source or reference'), { target: { value: 'Service log B-7' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save baseline reading' }));
  expect(screen.getByRole('alert')).toHaveTextContent('at least as large');
  expect(fetch).toHaveBeenCalledTimes(1);
});
it('pauses polling while editing and stops after unmount', async () => {
  vi.useFakeTimers(); const fetch = vi.fn(async () => response({ battery_cycles: initial })); vi.stubGlobal('fetch', fetch);
  let unmount!: () => void;
  await act(async () => { ({ unmount } = render(<BatteryCycles droneId="D1" canRecord />)); });
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Record cycle baseline' })));
  await act(async () => vi.advanceTimersByTime(10000)); expect(fetch).toHaveBeenCalledTimes(1);
  unmount(); await act(async () => vi.advanceTimersByTime(10000)); expect(fetch).toHaveBeenCalledTimes(1);
});
