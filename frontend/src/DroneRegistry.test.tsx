import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { DroneRegistry } from './DroneRegistry';
import type { Drone } from './drones';

const aircraft: Drone = {
  id: '8cc0cc3e-d347-4cff-bb0f-156d7bc865f6', organisation_id: 'org-a', name: 'Survey 01', vendor: 'dji',
  model: 'Mavic 3', serial_number: 'DJI-0001', status: 'unknown', last_telemetry_at: null,
  created_at: '2026-10-29T07:00:00Z', updated_at: '2026-10-29T07:00:00Z',
};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const page = (drones: Drone[] = [aircraft], more = false, offset = 0) => json({ drones, pagination: { limit: 20, offset, has_more: more } });
function server(extra?: (url: URL, init: RequestInit) => Response | undefined) {
  const fetch = vi.fn(async (input: string | URL | Request, init: RequestInit = {}) => {
    const url = new URL(String(input), 'http://localhost');
    const response = extra?.(url, init);
    if (response) return response;
    if (url.pathname.endsWith(aircraft.id)) return json({ drone: aircraft });
    return page();
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}
const mutations = (fetch: ReturnType<typeof server>) => fetch.mock.calls.filter(([, init]) => init?.method !== 'GET');

async function fillCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Register drone' }));
  const dialog = screen.getByRole('dialog', { name: 'Register drone' });
  await user.type(within(dialog).getByLabelText('Drone name'), '  New aircraft  ');
  await user.selectOptions(within(dialog).getByLabelText('Vendor'), 'ardupilot');
  await user.type(within(dialog).getByLabelText('Model'), '  Quad  ');
  await user.type(within(dialog).getByLabelText('Serial number'), '  AP-001  ');
  return dialog;
}

describe('drone registry', () => {
  it('registers normalized metadata and refreshes the list', async () => {
    const user = userEvent.setup(); let created: Drone | null = null;
    const fetch = server((_url, init) => {
      if (init.method === 'POST') { created = { ...aircraft, ...JSON.parse(String(init.body)), id: 'new-id' }; return json({ drone: created }, 201); }
      if (created) return page([created, aircraft]);
    });
    render(<DroneRegistry canManage />);
    await screen.findByText('Survey 01');
    const dialog = await fillCreate(user);
    await user.click(within(dialog).getByRole('button', { name: 'Register aircraft' }));
    await screen.findByText('New aircraft registered.');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByText('New aircraft')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Drone registry' })).toHaveFocus());
    expect(JSON.parse(String(mutations(fetch)[0][1]?.body))).toEqual({ name: 'New aircraft', vendor: 'ardupilot', model: 'Quad', serial_number: 'AP-001' });
  });

  it('keeps entered values when the API reports a duplicate', async () => {
    const user = userEvent.setup();
    server((_url, init) => init.method === 'POST' ? json({ error: 'Drone conflicts with an existing serial number or a related record' }, 409) : undefined);
    render(<DroneRegistry canManage />); await screen.findByText('Survey 01');
    const dialog = await fillCreate(user);
    await user.click(within(dialog).getByRole('button', { name: 'Register aircraft' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('conflicts');
    expect(within(dialog).getByLabelText('Serial number')).toHaveValue('  AP-001  ');
    expect(within(dialog).getByRole('button', { name: 'Register aircraft' })).toBeEnabled();
  });

  it('loads fresh details and sends only changed metadata on edit', async () => {
    const user = userEvent.setup();
    const fetch = server((_url, init) => init.method === 'PATCH' ? json({ drone: { ...aircraft, name: 'Renamed' } }) : undefined);
    render(<DroneRegistry canManage />); await screen.findByText('Survey 01');
    await user.click(screen.getByRole('button', { name: 'Edit Survey 01' }));
    const input = await screen.findByLabelText('Drone name');
    await user.clear(input); await user.type(input, 'Renamed');
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await screen.findByText('Renamed updated.');
    expect(JSON.parse(String(mutations(fetch)[0][1]?.body))).toEqual({ name: 'Renamed' });
    expect(mutations(fetch)[0][1]?.method).toBe('PATCH');
    expect(fetch.mock.calls.some(([url]) => String(url).endsWith(`/drones/${aircraft.id}`))).toBe(true);
  });

  it('requires confirmation for removal and supports cancelling', async () => {
    const user = userEvent.setup(); let deleted = false;
    const fetch = server((_url, init) => {
      if (init.method === 'DELETE') { deleted = true; return new Response(null, { status: 204 }); }
      if (deleted) return page([]);
    });
    render(<DroneRegistry canManage />); await screen.findByText('Survey 01');
    await user.click(screen.getByRole('button', { name: 'Remove Survey 01' }));
    await screen.findByRole('button', { name: 'Keep drone' });
    expect(mutations(fetch)).toHaveLength(0);
    await user.click(screen.getByRole('button', { name: 'Keep drone' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Remove Survey 01' }));
    await user.click(await screen.findByRole('button', { name: 'Remove aircraft' }));
    await screen.findByText('Survey 01 removed from the registry.');
    await screen.findByText('Your registry is empty');
    expect(mutations(fetch)).toHaveLength(1);
    expect(mutations(fetch)[0][1]?.method).toBe('DELETE');
  });

  it('gives read-only users details without mutation controls', async () => {
    const user = userEvent.setup(); const fetch = server();
    render(<DroneRegistry canManage={false} />); await screen.findByText('Survey 01');
    expect(screen.queryByRole('button', { name: 'Register drone' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit Survey 01' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Remove Survey 01' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'View details for Survey 01' }));
    const dialog = await screen.findByRole('dialog', { name: 'Aircraft details' });
    await within(dialog).findByText('DJI-0001');
    expect(within(dialog).queryByRole('button', { name: 'Edit drone' })).not.toBeInTheDocument();
    expect(mutations(fetch)).toHaveLength(0);
  });

  it('paginates and applies filters from the first page', async () => {
    const user = userEvent.setup();
    const fetch = server(url => page(Number(url.searchParams.get('offset')) ? [{ ...aircraft, id: 'next-id', name: 'Survey 02' }] : [aircraft], true, Number(url.searchParams.get('offset'))));
    render(<DroneRegistry canManage />); await screen.findByText('Survey 01');
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByText('Survey 02');
    expect(String(fetch.mock.calls.at(-1)?.[0])).toContain('offset=20');
    await user.type(screen.getByLabelText('Search aircraft'), '  Survey  ');
    await user.selectOptions(screen.getByLabelText('Vendor'), 'dji');
    await user.selectOptions(screen.getByLabelText('Status'), 'unknown');
    await user.click(screen.getByRole('button', { name: 'Apply filters' }));
    await screen.findByText('Survey 01');
    const url = new URL(String(fetch.mock.calls.at(-1)?.[0]), 'http://localhost');
    expect(Object.fromEntries(url.searchParams)).toMatchObject({ offset: '0', q: 'Survey', vendor: 'dji', status: 'unknown' });
  });

  it('offers retry after a failed list request', async () => {
    const user = userEvent.setup(); let failed = true;
    server(() => { if (failed) return json({ error: 'Registry unavailable' }, 503); });
    render(<DroneRegistry canManage={false} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Registry unavailable');
    failed = false;
    await user.click(screen.getByRole('button', { name: 'Retry loading fleet' }));
    await screen.findByText('Survey 01');
  });

  it('ignores a late response from an obsolete filter request', async () => {
    const user = userEvent.setup(); let finishFirst!: (value: Response) => void;
    const fetch = vi.fn().mockImplementationOnce(() => new Promise<Response>(resolve => { finishFirst = resolve; })).mockResolvedValue(page([{ ...aircraft, name: 'New search result' }]));
    vi.stubGlobal('fetch', fetch);
    render(<DroneRegistry canManage={false} />);
    await user.type(screen.getByLabelText('Search aircraft'), 'New');
    await user.click(screen.getByRole('button', { name: 'Apply filters' }));
    await screen.findByText('New search result');
    await act(async () => { finishFirst(page([aircraft])); });
    expect(screen.queryByText('Survey 01')).not.toBeInTheDocument();
    expect(screen.getByText('New search result')).toBeInTheDocument();
  });
});
