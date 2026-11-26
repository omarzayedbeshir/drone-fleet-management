import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { AuditHistory } from './AuditHistory';

it('exports every matching audit page with before and after values', async () => {
  const event = {
    id: '2', organisation_id: 'org-a', actor_id: 'manager', resource_type: 'mission', resource_id: 'mission-a',
    action: 'updated', before_data: { location: 'Old, field' }, after_data: { location: 'New field' },
    created_at: '2026-11-26T11:00:00Z',
  };
  const fetch = vi.fn(async (path: string) => {
    if (path.includes('before_id=2')) return Response.json({ events: [{ ...event, id: '1', action: 'created' }], next_before_id: null });
    return Response.json({ events: [event], next_before_id: '2' });
  });
  const createObjectURL = vi.fn<(blob: Blob) => string>(() => 'blob:audit-download');
  const revokeObjectURL = vi.fn();
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = createObjectURL;
    static revokeObjectURL = revokeObjectURL;
  });
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  try {
    render(<AuditHistory administrator={false} />);
    const actor = userEvent.setup();
    await actor.selectOptions(screen.getByLabelText('Resource type'), 'mission');
    await actor.click(screen.getByRole('button', { name: 'Load events' }));
    await actor.click(await screen.findByRole('button', { name: 'Download CSV' }));
    await waitFor(() => expect(createObjectURL).toHaveBeenCalledTimes(1));
    const csv = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = reject;
      reader.readAsText(createObjectURL.mock.calls[0][0]);
    });
    expect(csv).toContain('"Before JSON","After JSON"');
    expect(csv).toContain('{""location"":""Old, field""}');
    expect(csv).toContain('"1","org-a"');
    expect(fetch.mock.calls.some(([path]) => path.includes('resource_type=mission&before_id=2&limit=100'))).toBe(true);
  } finally {
    click.mockRestore();
  }
});
