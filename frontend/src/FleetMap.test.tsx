import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { FleetMap } from './FleetMap';
import type { Drone } from './drones';
import type { TelemetrySample } from './telemetry';

vi.mock('./TelemetryMap', () => ({ TelemetryMap: ({ aircraft, selectedPosition }: { aircraft: unknown[]; selectedPosition?: TelemetrySample }) => <div data-testid="map-output">{aircraft.length} positioned · {selectedPosition?.event_id ?? 'no GPS'}</div> }));
const drone: Drone = { id: 'ap-1', organisation_id: 'org', name: 'SITL quad', model: 'ArduCopter', vendor: 'ardupilot', serial_number: 'AP-001', status: 'grounded', last_telemetry_at: null, created_at: '', updated_at: '' };
function sample(id: string, seconds: number, gps = true): TelemetrySample {
  return { event_id: id, observed_at: new Date(Date.UTC(2020, 9, 29, 12, 0, seconds)).toISOString(), received_at: '', source: 'ardupilot', status: 'available', flight_time_seconds: 0, battery_cycles: 0, fault_flags: [], battery_percent: null, latitude: gps ? 30 : null, longitude: gps ? 31 : null, altitude_m: 100, speed_mps: 0 };
}
function server(samples: TelemetrySample[]) {
  const fetch = vi.fn(async (_url: string) => new Response(JSON.stringify({ samples, pagination: { has_more: false } })));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}
afterEach(() => vi.useRealTimers());

it('preserves observation timestamps and last GPS without inventing unknown values', async () => {
  const fetch = server([sample('no-fix', 2, false), sample('gps', 1)]);
  render(<FleetMap drones={[drone]} />);
  await screen.findByText('100.0 m');
  expect(document.querySelector('time')).toHaveAttribute('datetime', sample('no-fix', 2, false).observed_at);
  expect(screen.getByTestId('map-output')).toHaveTextContent('1 positioned · gps');
  expect(screen.getByText('Not reported')).toBeInTheDocument();
  expect(screen.getByText(/latest observation has no GPS fix/i)).toBeInTheDocument();
  expect(screen.queryByText(/does not clear a grounded registry status/i)).not.toBeInTheDocument();
  expect(String(fetch.mock.calls[0]?.[0])).toContain('/ap-1/telemetry?limit=100&offset=0');
});

it('does not position an aircraft without GPS and still displays telemetry', async () => {
  server([sample('no-fix', 0, false)]);
  render(<FleetMap drones={[drone]} />);
  await screen.findByText('GPS not reported in this window.');
  expect(screen.getByTestId('map-output')).toHaveTextContent('0 positioned · no GPS');
  expect(screen.queryByRole('slider')).not.toBeInTheDocument();
});

it('automatically refreshes despite per-aircraft failures and stops when unmounted', async () => {
  const fetch = vi.fn(async (url: string) => url.includes('failed') ? new Response('{"error":"Telemetry unavailable"}', { status: 503 }) : new Response(JSON.stringify({ samples: [sample('gps', 0)], pagination: { has_more: false } })));
  vi.stubGlobal('fetch', fetch);
  vi.useFakeTimers();
  let unmount!: () => void;
  await act(async () => { ({ unmount } = render(<FleetMap drones={[drone, { ...drone, id: 'failed', name: 'Other drone' }]} />)); });
  expect(screen.getByText(/Some aircraft could not load/)).toBeInTheDocument();
  expect(screen.getByTestId('map-output')).toHaveTextContent('1 positioned');
  fireEvent.change(screen.getByLabelText('Aircraft'), { target: { value: 'failed' } });
  expect(screen.getByRole('heading', { name: 'Other drone' })).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('Telemetry unavailable');
  await act(async () => vi.advanceTimersByTime(5000));
  expect(fetch).toHaveBeenCalledTimes(4);
  unmount();
  await act(async () => vi.advanceTimersByTime(10_000));
  expect(fetch).toHaveBeenCalledTimes(4);
});

it('keeps the map and readings visible during refresh, updates in place, and retains them on failure', async () => {
  const fetch = server([sample('initial', 1), sample('older', 0)]);
  vi.useFakeTimers();
  await act(async () => { render(<FleetMap drones={[drone]} />); });
  expect(screen.getByText('100.0 m')).toBeInTheDocument();
  const mapNode = screen.getByTestId('map-output');
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  expect(screen.queryByText('Latest known positions')).not.toBeInTheDocument();
  const reading = screen.getByText('100.0 m');
  const status = screen.getByRole('status');
  let resolve!: (response: Response) => void;
  fetch.mockImplementationOnce(() => new Promise<Response>(done => { resolve = done; }));
  await act(async () => vi.advanceTimersByTime(5000));
  expect(screen.getByTestId('map-output')).toBe(mapNode);
  expect(mapNode).toHaveTextContent('initial');
  expect(screen.getByText('100.0 m')).toBe(reading);
  expect(screen.getByRole('status')).toBe(status);
  expect(status).toHaveTextContent('Updating telemetry');
  expect(status).toHaveClass('sr-only');
  expect(screen.getByRole('button', { name: 'Refresh telemetry' })).toBeEnabled();
  await act(async () => resolve(new Response(JSON.stringify({ samples: [{ ...sample('updated', 1), altitude_m: 112 }], pagination: { has_more: false } }))));
  expect(screen.getByTestId('map-output')).toBe(mapNode);
  expect(mapNode).toHaveTextContent('updated');
  expect(screen.getByText('112.0 m')).toBeInTheDocument();
  fetch.mockImplementationOnce(async () => new Response('{"error":"Temporary failure"}', { status: 503 }));
  await act(async () => vi.advanceTimersByTime(5000));
  expect(screen.getByRole('alert')).toHaveTextContent('Showing the last loaded readings');
  expect(mapNode).toHaveTextContent('updated');
  expect(screen.getByText('112.0 m')).toBeInTheDocument();
});
