import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { FlightSummary } from './FlightSummary';

it('enables daily delivery and shows the saved report', async () => {
  let enabled = false;
  const report = {
    day: '2026-11-25', status: 'no_data', narrative: 'No telemetry was recorded.',
    facts: { aircraft_with_telemetry: 0, aircraft_observed_in_mission: 0, observations: 0, in_mission_observations: 0 },
    sources: [],
  };
  const fetch = vi.fn(async (path: string, request?: RequestInit) => {
    expect(path).toBe('/api/v1/flight-summary/schedule');
    if (request?.method === 'PUT') {
      enabled = JSON.parse(String(request.body)).enabled;
      return new Response(null, { status: 204 });
    }
    return Response.json({ enabled, latest: enabled ? { report, delivered_at: '2026-11-26T00:01:00Z' } : null });
  });
  vi.stubGlobal('fetch', fetch);
  render(<FlightSummary />);
  const actor = userEvent.setup();
  const toggle = await screen.findByRole('checkbox', { name: 'Enable daily delivery for this organisation' });
  expect(toggle).not.toBeChecked();
  await actor.click(toggle);
  await waitFor(() => expect(toggle).toBeChecked());
  await actor.click(await screen.findByText('Latest delivery · 2026-11-25 UTC'));
  expect(screen.getByText('No telemetry was recorded.')).toBeVisible();
});
