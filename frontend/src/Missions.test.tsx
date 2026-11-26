import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { App } from './App';

it('schedules, edits, and cancels a mission from the manager workspace', async () => {
  const actor = userEvent.setup();
  let mission: Record<string, unknown> | null = null;
  let attempts = 0;
  vi.stubGlobal('fetch', vi.fn(async (path: string, request?: RequestInit) => {
    if (path.endsWith('/auth/me')) return Response.json({
      user: { id: 'manager', name: 'Manager', email: 'manager@example.test', role: 'fleet_manager', organisation_id: 'org' },
    });
    if (path.includes('/drones?')) return Response.json({ drones: [], pagination: { has_more: false } });
    if (path.endsWith('/assignment-options')) return Response.json({
      drones: [{ id: 'aircraft', name: 'Surveyor (S1)', eligible: true }],
      pilots: [{ id: 'pilot', name: 'Alex', eligible: true }],
    });
    if (path.includes('/missions?')) return Response.json({ missions: mission ? [mission] : [], pagination: { has_more: false } });
    if (path.endsWith('/cancel') && request?.method === 'POST') {
      mission = { ...mission, cancelled_at: '2026-11-27T08:00:00Z' };
      return new Response(null, { status: 204 });
    }
    if (request?.method === 'POST' || request?.method === 'PUT') {
      const input = JSON.parse(String(request.body));
      expect(input).toMatchObject({ drone_id: 'aircraft', pilot_id: 'pilot', near_populated_area: true,
        safety_rationale: 'Keep clear of residences', safety_acknowledged: true });
      if (++attempts === 1) return Response.json({ error: 'Drone already booked for this time' }, { status: 409 });
      mission = { ...input, id: 'mission-a', cancelled_at: null, drone_status: 'available' };
      return Response.json({ mission }, { status: request.method === 'POST' ? 201 : 200 });
    }
    throw new Error(`Unexpected request to ${path}`);
  }));

  render(<App />);
  await actor.click(await screen.findByRole('button', { name: 'Missions' }));
  await screen.findByText('No missions to show.');
  await actor.click(screen.getByRole('button', { name: 'Schedule mission' }));
  await actor.selectOptions(screen.getByLabelText('Aircraft'), 'aircraft');
  await actor.selectOptions(screen.getByLabelText('Pilot'), 'pilot');
  await actor.type(screen.getByLabelText('Location'), 'Survey field');
  fireEvent.change(screen.getByLabelText('Start time'), { target: { value: '2026-11-27T09:00' } });
  fireEvent.change(screen.getByLabelText('End time'), { target: { value: '2026-11-27T10:00' } });
  await actor.click(screen.getByLabelText('Near a populated area'));
  await actor.type(screen.getByLabelText('Safety rationale'), 'Keep clear of residences');
  await actor.click(screen.getByLabelText(/I acknowledge the safety/));
  await actor.click(screen.getByRole('button', { name: 'Schedule mission' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Drone already booked');
  await actor.click(screen.getByRole('button', { name: 'Schedule mission' }));
  await actor.click(await screen.findByRole('button', { name: 'Edit mission at Survey field' }));
  await actor.clear(screen.getByLabelText('Location'));
  await actor.type(screen.getByLabelText('Location'), 'North field');
  await actor.click(screen.getByRole('button', { name: 'Save changes' }));
  await actor.click(await screen.findByRole('button', { name: 'Cancel mission at North field' }));
  await actor.click(screen.getByRole('button', { name: 'Confirm cancellation' }));
  expect(await screen.findByRole('cell', { name: 'Cancelled' })).toBeInTheDocument();
});
