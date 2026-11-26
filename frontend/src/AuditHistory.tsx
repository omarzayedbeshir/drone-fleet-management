import { useEffect, useState, type FormEvent } from 'react';
import { apiRequest } from './api';

interface AuditEvent {
  id: string;
  actor_id: string;
  resource_type: string;
  resource_id: string;
  action: string;
  before_data: Record<string, unknown> | null;
  after_data: Record<string, unknown> | null;
  created_at: string;
}

interface AuditPage {
  events: AuditEvent[];
  next_before_id: string | null;
}

const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' });
const emptyFilter = { organisation: '', resource: '' };
const uuidPattern = '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}';

export function AuditHistory({ administrator }: { administrator: boolean }) {
  const [draft, setDraft] = useState(emptyFilter);
  const [filter, setFilter] = useState(administrator ? null : emptyFilter);
  const [cursors, setCursors] = useState<string[]>([]);
  const [page, setPage] = useState<AuditPage | null>(null);
  const [loading, setLoading] = useState(!administrator);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!filter) return;
    const controller = new AbortController();
    const query = new URLSearchParams();
    if (filter.organisation) query.set('organisation_id', filter.organisation);
    if (filter.resource) query.set('resource_type', filter.resource);
    const before = cursors.at(-1);
    if (before) query.set('before_id', before);
    setLoading(true);
    setPage(null);
    setError('');
    apiRequest<AuditPage>(query.size ? `/audit-events?${query}` : '/audit-events', { signal: controller.signal })
      .then(result => { if (!controller.signal.aborted) setPage(result); })
      .catch(error => {
        if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not load audit history.');
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [filter, cursors]);

  function apply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setCursors([]);
    setFilter({ organisation: draft.organisation.trim(), resource: draft.resource });
  }

  return (
    <section className="registry" aria-labelledby="audit-title">
      <div className="registry-heading">
        <div>
          <h3 id="audit-title">Audit history</h3>
          <p>Review recorded activity and inspect what changed.</p>
        </div>
      </div>
      <form className="registry-filters" onSubmit={apply}>
        {administrator && <div>
          <label htmlFor="audit-organisation">Organisation ID</label>
          <input id="audit-organisation" required pattern={uuidPattern} value={draft.organisation}
            onChange={event => setDraft({ ...draft, organisation: event.target.value })} />
        </div>}
        <div>
          <label htmlFor="audit-resource">Resource type</label>
          <select id="audit-resource" value={draft.resource}
            onChange={event => setDraft({ ...draft, resource: event.target.value })}>
            <option value="">All resource types</option>
            <option value="mission">Missions</option>
            <option value="drone">Aircraft</option>
            <option value="connector">Connectors</option>
            <option value="battery_cycles">Battery cycles</option>
          </select>
        </div>
        <button className="secondary" type="submit">Load events</button>
      </form>
      {loading && <p role="status">Loading audit events…</p>}
      {error && <p role="alert" className="error">{error}</p>}
      {page && <div className="registry-content audit-events">
        {page.events.length === 0 ? <p className="registry-state">No audit events match this selection.</p> :
          page.events.map(event => <details key={event.id} className="audit-event">
            <summary>
              <span><strong>{event.resource_type.replaceAll('_', ' ')} · {event.action.replaceAll('_', ' ')}</strong>
                <time dateTime={event.created_at}>{dateTime.format(new Date(event.created_at))}</time>
              </span>
            </summary>
            <dl className="audit-identifiers">
              <div><dt>Event ID</dt><dd>{event.id}</dd></div>
              <div><dt>Actor ID</dt><dd>{event.actor_id}</dd></div>
              <div><dt>Resource ID</dt><dd>{event.resource_id}</dd></div>
            </dl>
            <div className="audit-snapshots">
              <Snapshot title="Before" data={event.before_data} />
              <Snapshot title="After" data={event.after_data} />
            </div>
          </details>)}
        {(cursors.length > 0 || page.next_before_id) && <nav className="mission-pagination" aria-label="Audit pages">
          <button className="secondary" disabled={cursors.length === 0}
            onClick={() => setCursors(values => values.slice(0, -1))}>Previous</button>
          <span>Page {cursors.length + 1}</span>
          <button className="secondary" disabled={!page.next_before_id}
            onClick={() => {
              if (page.next_before_id) setCursors([...cursors, page.next_before_id]);
            }}>Next</button>
        </nav>}
      </div>}
    </section>
  );
}

function Snapshot({ title, data }: { title: string; data: AuditEvent['before_data'] }) {
  return <div>
    <h4>{title}</h4>
    {data ? <pre>{JSON.stringify(data, null, 2)}</pre> : <p>No snapshot recorded.</p>}
  </div>;
}
