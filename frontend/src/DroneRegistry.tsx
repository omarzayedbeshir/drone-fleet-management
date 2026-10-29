import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react';
import {
  droneApi, emptyFilters, normalizeMetadata, statusLabels, validateMetadata, vendorLabels,
  type Drone, type DroneFilters, type DroneMetadata, type DronePage, type DroneStatus, type Vendor,
} from './drones';

function message(error: unknown) { return error instanceof Error ? error.message : 'Please try again.'; }
function date(value: string | null) {
  if (!value) return 'No telemetry yet';
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? 'Unknown' : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(parsed);
}
function Status({ value }: { value: DroneStatus }) {
  return <span className={`status-badge status-${value}`}>{statusLabels[value]}</span>;
}

type Selection = { id: string; mode: 'view' | 'edit' | 'delete' } | { mode: 'create' } | null;

export function DroneRegistry({ canManage }: { canManage: boolean }) {
  const [draft, setDraft] = useState<DroneFilters>({ ...emptyFilters });
  const [filters, setFilters] = useState<DroneFilters>({ ...emptyFilters });
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(20);
  const [refresh, setRefresh] = useState(0);
  const [page, setPage] = useState<DronePage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [selection, setSelection] = useState<Selection>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusAfterRefresh = useRef(false);
  const hasFilters = Boolean(filters.q || filters.vendor || filters.status);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true); setError(''); setPage(null);
    droneApi.list(filters, limit, offset, controller.signal)
      .then(data => {
        if (!active) return;
        // Recover a page emptied by a deletion in another tab or session.
        if (data.drones.length === 0 && offset > 0) { setOffset(Math.max(0, offset - limit)); return; }
        setPage(data);
      })
      .catch(error => { if (active) setError(message(error)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; controller.abort(); };
  }, [filters, limit, offset, refresh]);

  useEffect(() => {
    if (!loading && (page || error) && focusAfterRefresh.current) {
      headingRef.current?.focus();
      focusAfterRefresh.current = false;
    }
  }, [loading, page, error]);

  function applyFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setFilters({ ...draft, q: draft.q.trim() }); setOffset(0);
  }
  function clearFilters() { setDraft({ ...emptyFilters }); setFilters({ ...emptyFilters }); setOffset(0); }
  function saved(drone: Drone, created: boolean) {
    focusAfterRefresh.current = true;
    setSelection(null); setNotice(`${drone.name} ${created ? 'registered' : 'updated'}.`);
    if (created) clearFilters();
    setRefresh(value => value + 1);
  }
  function removed(drone: Drone) {
    focusAfterRefresh.current = true;
    setSelection(null); setNotice(`${drone.name} removed from the registry.`);
    if (page?.drones.length === 1 && offset > 0) setOffset(Math.max(0, offset - limit));
    setRefresh(value => value + 1);
  }

  return (
    <section className="registry" aria-labelledby="registry-title">
      <div className="registry-heading">
        <div>
          <p className="eyebrow">Operations / Aircraft</p>
          <h3 id="registry-title" ref={headingRef} tabIndex={-1}>Drone registry</h3>
          <p>{canManage ? 'Aircraft, registration details, and latest reported status.' : 'Browse your organisation’s registered aircraft. You have read-only access.'}</p>
        </div>
        <div className="registry-actions">
          <button className="secondary" disabled={loading} onClick={() => setRefresh(value => value + 1)}>{loading ? 'Refreshing…' : 'Refresh'}</button>
          {canManage && <button onClick={() => setSelection({ mode: 'create' })}><span aria-hidden="true">+ </span>Register drone</button>}
        </div>
      </div>
      <form className="registry-filters" onSubmit={applyFilters} aria-label="Filter drones">
        <div className="search-field">
          <label htmlFor="drone-search">Search aircraft</label>
          <input id="drone-search" type="search" placeholder="Name, model, or serial number" maxLength={200}
            value={draft.q} onChange={event => setDraft({ ...draft, q: event.target.value })} />
        </div>
        <div>
          <label htmlFor="vendor-filter">Vendor</label>
          <select id="vendor-filter" value={draft.vendor} onChange={event => setDraft({ ...draft, vendor: event.target.value as Vendor | '' })}>
            <option value="">All vendors</option>
            {Object.entries(vendorLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </div>
        <div>
          <label htmlFor="status-filter">Status</label>
          <select id="status-filter" value={draft.status} onChange={event => setDraft({ ...draft, status: event.target.value as DroneStatus | '' })}>
            <option value="">All statuses</option>
            {Object.entries(statusLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </div>
        <button className="secondary" type="submit">Apply filters</button>
        {(hasFilters || draft.q || draft.vendor || draft.status) && <button className="text-button" type="button" onClick={clearFilters}>Clear</button>}
      </form>
      {notice && <p className="success" role="status">{notice}</p>}
      <div className="registry-content" aria-busy={loading}>
        <div className="table-heading"><span>Registered aircraft</span><span className="muted">Latest reported values · Refresh to update</span></div>
        {loading ? <p className="registry-state" role="status">Loading your fleet…</p> : error ? (
          <div className="registry-state"><p className="error" role="alert">{error}</p><button className="secondary" onClick={() => setRefresh(value => value + 1)}>Retry loading fleet</button></div>
        ) : page && page.drones.length === 0 ? (
          <div className="registry-state">
            <h4>{hasFilters ? 'No matching aircraft' : 'Your registry is empty'}</h4>
            <p>{hasFilters ? 'Try another search or clear your filters.' : canManage ? 'Register your first drone to start building your fleet.' : 'Your fleet manager has not registered any drones yet.'}</p>
            {hasFilters ? <button className="secondary" onClick={clearFilters}>Clear filters</button> : canManage && <button onClick={() => setSelection({ mode: 'create' })}>Register your first drone</button>}
          </div>
        ) : page && (
          <>
            <div className="table-scroll" tabIndex={0} role="region" aria-label="Registered aircraft table">
              <table>
                <caption className="sr-only">Registered aircraft in your organisation</caption>
                <thead><tr><th scope="col">Aircraft</th><th scope="col">Vendor</th><th scope="col">Serial number</th><th scope="col">Status</th><th scope="col">Last telemetry</th><th scope="col">Actions</th></tr></thead>
                <tbody>{page.drones.map(drone => (
                  <tr key={drone.id}>
                    <th scope="row"><span className="aircraft-name">{drone.name}</span><span className="aircraft-model">{drone.model}</span></th>
                    <td>{vendorLabels[drone.vendor]}</td>
                    <td className="serial">{drone.serial_number}</td>
                    <td><Status value={drone.status} /></td>
                    <td>{date(drone.last_telemetry_at)}</td>
                    <td><div className="row-actions">
                      <button className="text-button" aria-label={`View details for ${drone.name}`} onClick={() => setSelection({ id: drone.id, mode: 'view' })}>View</button>
                      {canManage && <>
                        <button className="text-button" aria-label={`Edit ${drone.name}`} onClick={() => setSelection({ id: drone.id, mode: 'edit' })}>Edit</button>
                        <button className="text-button danger-text" aria-label={`Remove ${drone.name}`} onClick={() => setSelection({ id: drone.id, mode: 'delete' })}>Remove</button>
                      </>}
                    </div></td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
            <div className="pagination">
              <p role="status">Showing {offset + 1}–{offset + page.drones.length}{hasFilters ? ' matching aircraft' : ' aircraft'}</p>
              <div className="page-controls">
                <label htmlFor="page-size">Per page</label>
                <select id="page-size" value={limit} onChange={event => { setLimit(Number(event.target.value)); setOffset(0); }}>
                  {[10, 20, 50, 100].map(value => <option key={value} value={value}>{value}</option>)}
                </select>
                <button className="secondary" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>Previous</button>
                <button className="secondary" disabled={!page.pagination.has_more} onClick={() => setOffset(offset + limit)}>Next</button>
              </div>
            </div>
          </>
        )}
      </div>
      {selection?.mode === 'create' && <DroneEditor onClose={() => setSelection(null)} onSaved={drone => saved(drone, true)} />}
      {selection && selection.mode !== 'create' && <DroneDialog key={`${selection.id}:${selection.mode}`} id={selection.id} mode={selection.mode} canManage={canManage}
        onClose={() => setSelection(null)} onMode={mode => setSelection({ id: selection.id, mode })} onSaved={drone => saved(drone, false)} onRemoved={removed} />}
    </section>
  );
}

function Modal({ title, children, onClose, busy = false }: { title: string; children: ReactNode; onClose: () => void; busy?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  useEffect(() => {
    const dialog = ref.current!;
    const previous = document.activeElement;
    dialog.showModal();
    dialog.querySelector<HTMLElement>('[data-autofocus]')?.focus();
    return () => {
      dialog.close();
      if (previous instanceof HTMLElement && document.contains(previous)) previous.focus();
    };
  }, []);
  return (
    <dialog ref={ref} className="registry-dialog" aria-labelledby={titleID} onCancel={event => { event.preventDefault(); if (!busy) onClose(); }}>
      <div className="dialog-heading"><h2 id={titleID}>{title}</h2><button type="button" className="text-button" aria-label="Close dialog" disabled={busy} onClick={onClose}>Close</button></div>
      {children}
    </dialog>
  );
}

function DroneEditor({ initial, onClose, onSaved }: { initial?: Drone; onClose: () => void; onSaved: (drone: Drone) => void }) {
  const [values, setValues] = useState<DroneMetadata>(initial ? { name: initial.name, vendor: initial.vendor, model: initial.model, serial_number: initial.serial_number } : { name: '', vendor: 'dji', model: '', serial_number: '' });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const prefix = useId();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (busy) return;
    const data = normalizeMetadata(values);
    const invalid = validateMetadata(data);
    if (invalid) { setError(invalid); return; }
    const patch = initial ? Object.fromEntries(Object.entries(data).filter(([key, value]) => value !== initial[key as keyof DroneMetadata])) as Partial<DroneMetadata> : null;
    if (patch && Object.keys(patch).length === 0) { setError('Change at least one field before saving.'); return; }
    setBusy(true); setError('');
    try {
      const response = initial ? await droneApi.update(initial.id, patch!) : await droneApi.create(data);
      onSaved(response.drone);
    } catch (error) { setError(message(error)); }
    finally { setBusy(false); }
  }
  return (
    <Modal title={initial ? 'Edit drone' : 'Register drone'} onClose={onClose} busy={busy}>
      <p>{initial ? 'Update aircraft details. Operational status is managed separately.' : 'Add an aircraft to your organisation. Its status will be Unknown until telemetry is available.'}</p>
      <form onSubmit={submit} aria-busy={busy}>
        <label htmlFor={`${prefix}-name`}>Drone name</label>
        <input id={`${prefix}-name`} autoFocus data-autofocus required maxLength={400} value={values.name} disabled={busy} onChange={event => setValues({ ...values, name: event.target.value })} />
        <label htmlFor={`${prefix}-vendor`}>Vendor</label>
        <select id={`${prefix}-vendor`} value={values.vendor} disabled={busy} onChange={event => setValues({ ...values, vendor: event.target.value as Vendor })}>
          {Object.entries(vendorLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
        </select>
        <label htmlFor={`${prefix}-model`}>Model</label>
        <input id={`${prefix}-model`} required maxLength={400} value={values.model} disabled={busy} onChange={event => setValues({ ...values, model: event.target.value })} />
        <label htmlFor={`${prefix}-serial`}>Serial number</label>
        <input id={`${prefix}-serial`} required maxLength={400} value={values.serial_number} disabled={busy} onChange={event => setValues({ ...values, serial_number: event.target.value })} />
        {error && <p role="alert" className="error">{error}</p>}
        <div className="dialog-actions"><button className="secondary" type="button" disabled={busy} onClick={onClose}>Cancel</button><button type="submit" disabled={busy}>{busy ? 'Saving…' : initial ? 'Save changes' : 'Register aircraft'}</button></div>
      </form>
    </Modal>
  );
}

function DroneDialog({ id, mode, canManage, onClose, onMode, onSaved, onRemoved }: {
  id: string; mode: 'view' | 'edit' | 'delete'; canManage: boolean; onClose: () => void;
  onMode: (mode: 'view' | 'edit' | 'delete') => void; onSaved: (drone: Drone) => void; onRemoved: (drone: Drone) => void;
}) {
  const [drone, setDrone] = useState<Drone | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true; const controller = new AbortController();
    setError(''); setDrone(null);
    droneApi.get(id, controller.signal).then(data => { if (active) setDrone(data.drone); }).catch(error => { if (active) setError(message(error)); });
    return () => { active = false; controller.abort(); };
  }, [id, retry]);
  async function remove() {
    if (!drone || busy) return;
    setBusy(true); setError('');
    try { await droneApi.remove(id); onRemoved(drone); }
    catch (error) { setError(message(error)); }
    finally { setBusy(false); }
  }
  if (mode === 'edit' && drone) return <DroneEditor initial={drone} onClose={onClose} onSaved={onSaved} />;
  return (
    <Modal title={mode === 'delete' ? 'Remove drone?' : 'Aircraft details'} onClose={onClose} busy={busy}>
      {!drone ? error ? <><p role="alert" className="error">{error}</p><button className="secondary" onClick={() => setRetry(value => value + 1)}>Retry loading aircraft</button></> : <p role="status">Loading aircraft…</p> : mode === 'delete' ? (
        <>
          <p>Remove <strong>{drone.name}</strong> ({drone.serial_number}) from your registry? This cannot be undone. Its audit history will be kept.</p>
          {error && <p className="error" role="alert">{error}</p>}
          <div className="dialog-actions"><button className="secondary" disabled={busy} onClick={onClose} autoFocus>Keep drone</button><button className="danger" disabled={busy} onClick={remove}>{busy ? 'Removing…' : 'Remove aircraft'}</button></div>
        </>
      ) : (
        <>
          <p className="detail-name">{drone.name}</p>
          <dl className="drone-details">
            <div><dt>Vendor</dt><dd>{vendorLabels[drone.vendor]}</dd></div><div><dt>Model</dt><dd>{drone.model}</dd></div>
            <div><dt>Serial number</dt><dd>{drone.serial_number}</dd></div><div><dt>Operational status</dt><dd><Status value={drone.status} /></dd></div>
            <div><dt>Last telemetry</dt><dd>{date(drone.last_telemetry_at)}</dd></div><div><dt>Registered</dt><dd>{date(drone.created_at)}</dd></div>
            <div><dt>Last edited</dt><dd>{date(drone.updated_at)}</dd></div>
          </dl>
          <div className="dialog-actions">{canManage && <><button className="secondary" onClick={() => onMode('edit')}>Edit drone</button><button className="text-button danger-text" onClick={() => onMode('delete')}>Remove drone</button></>}<button onClick={onClose}>Done</button></div>
        </>
      )}
    </Modal>
  );
}
