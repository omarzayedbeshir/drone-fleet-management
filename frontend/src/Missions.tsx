import { useEffect, useRef, useState, type FormEvent } from 'react';
import { apiRequest, type User } from './api';
import { statusLabels, type DroneStatus } from './drones';

interface MissionInput {
  drone_id: string;
  pilot_id: string;
  location: string;
  starts_at: string;
  ends_at: string;
  near_populated_area: boolean;
  safety_rationale: string | null;
  safety_acknowledged: boolean;
}

interface Mission extends MissionInput {
  id: string;
  drone_status: DroneStatus;
  cancelled_at: string | null;
}

interface Option { id: string; name: string; eligible: boolean }
interface Options { drones: Option[]; pilots: Option[] }
interface Page { missions: Mission[]; pagination: { has_more: boolean } }

const pageSize = 20;
const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });
const timeZone = dateFormat.resolvedOptions().timeZone;
const emptyOptions: Options = { drones: [], pilots: [] };

function localInput(value: string) {
  const date = new Date(value);
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 23);
}

function timeFromInput(value: string, original?: string) {
  if (original && Date.parse(value) === Date.parse(localInput(original))) return original;
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? '' : date.toISOString();
}

function message(error: unknown) {
  return error instanceof Error ? error.message : 'Please try again.';
}

export function Missions({ user }: { user: User }) {
  const [page, setPage] = useState<Page | null>(null);
  const [options, setOptions] = useState<Options | null>(null);
  const [offset, setOffset] = useState(0);
  const [refresh, setRefresh] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [actionError, setActionError] = useState('');
  const [notice, setNotice] = useState('');
  const [editor, setEditor] = useState<Mission | 'new' | null>(null);
  const [cancelling, setCancelling] = useState<Mission | null>(null);
  const [busy, setBusy] = useState(false);
  const editorHeading = useRef<HTMLHeadingElement>(null);
  const cancelHeading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (editor) editorHeading.current?.focus();
    if (cancelling) cancelHeading.current?.focus();
  }, [editor, cancelling]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setLoadError('');
    Promise.all([
      apiRequest<Page>(`/missions?limit=${pageSize}&offset=${offset}`, { signal: controller.signal }),
      apiRequest<Options>('/missions/assignment-options', { signal: controller.signal }),
    ]).then(([result, assignments]) => {
      if (controller.signal.aborted) return;
      setPage(result);
      setOptions(assignments);
    }).catch(error => {
      if (!controller.signal.aborted) {
        setPage(null);
        setOptions(null);
        setLoadError(message(error));
      }
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [offset, refresh]);

  function updated(text: string) {
    setEditor(null);
    setCancelling(null);
    setNotice(text);
    setRefresh(value => value + 1);
  }

  async function cancelMission() {
    if (!cancelling || busy) return;
    setBusy(true);
    setActionError('');
    try {
      await apiRequest(`/missions/${cancelling.id}/cancel`, { method: 'POST' });
      updated('Mission cancelled.');
    } catch (error) {
      setActionError(message(error));
    } finally {
      setBusy(false);
    }
  }

  const names = options ?? emptyOptions;
  const droneNames = new Map(names.drones.map(option => [option.id, option.name]));
  const pilotNames = new Map(names.pilots.map(option => [option.id, option.name]));

  return (
    <section className="registry mission-screen" aria-labelledby="missions-heading">
      <div className="registry-heading">
        <div>
          <h3 id="missions-heading">{user.role === 'pilot' ? 'My missions' : 'Missions'}</h3>
          <p>Schedule flights and review current and past assignments. Times are shown in {timeZone}.</p>
        </div>
        <div className="registry-actions">
          <button className="secondary" disabled={loading || busy || Boolean(editor || cancelling)}
            onClick={() => setRefresh(value => value + 1)}>Refresh</button>
          {!editor && !cancelling && <button disabled={loading || !options}
            onClick={() => { setNotice(''); setEditor('new'); }}>Schedule mission</button>}
        </div>
      </div>
      {notice && <p className="success" role="status">{notice}</p>}
      {editor && options && (
        <MissionForm headingRef={editorHeading} user={user} options={options} mission={editor === 'new' ? undefined : editor}
          onClose={() => setEditor(null)} onSave={() => {
            if (editor === 'new') setOffset(0);
            updated(editor === 'new' ? 'Mission scheduled.' : 'Mission updated.');
          }} />
      )}
      {cancelling && (
        <section className="panel mission-editor" aria-labelledby="cancel-mission-title">
          <h4 id="cancel-mission-title" ref={cancelHeading} tabIndex={-1}>Cancel mission at {cancelling.location}?</h4>
          <p>The aircraft and pilot will be free to book during this time.</p>
          {actionError && <p className="error" role="alert">{actionError}</p>}
          <div className="row-actions">
            <button className="danger" disabled={busy} onClick={cancelMission}>{busy ? 'Cancelling…' : 'Confirm cancellation'}</button>
            <button className="secondary" disabled={busy} onClick={() => setCancelling(null)}>Keep mission</button>
          </div>
        </section>
      )}
      {loading ? <p role="status">Loading missions…</p> :
        loadError ? <p role="alert" className="error">{loadError}</p> :
        page && <div className="registry-content">
          {page.missions.length === 0 ? <p className="registry-state">No missions to show.</p> :
            <div className="mission-table-wrap" tabIndex={0} role="region" aria-label="Mission schedule">
              <table className="mission-table">
                <thead><tr><th scope="col">Location and time</th><th scope="col">Aircraft</th>
                  <th scope="col">Pilot</th><th scope="col">Mission status</th><th scope="col">Actions</th></tr></thead>
                <tbody>{page.missions.map(mission => (
                  <tr key={mission.id}>
                    <th scope="row">
                      {mission.location}
                      <span>{dateFormat.formatRange(new Date(mission.starts_at), new Date(mission.ends_at))}</span>
                      {mission.near_populated_area && <span>Near a populated area</span>}
                    </th>
                    <td>{droneNames.get(mission.drone_id) ?? mission.drone_id}
                      <span className={`status-badge status-${mission.drone_status}`}>{statusLabels[mission.drone_status]}</span>
                    </td>
                    <td>{pilotNames.get(mission.pilot_id) ?? mission.pilot_id}</td>
                    <td>{mission.cancelled_at ? 'Cancelled' : Date.parse(mission.ends_at) <= Date.now() ? 'Past' :
                      Date.parse(mission.starts_at) <= Date.now() ? 'In progress' : 'Scheduled'}</td>
                    <td>{!mission.cancelled_at && <div className="row-actions">
                      <button className="text-button" disabled={Boolean(editor || cancelling)}
                        aria-label={`Edit mission at ${mission.location}`}
                        onClick={() => { setNotice(''); setEditor(mission); }}>Edit</button>
                      <button className="text-button danger-text" disabled={Boolean(editor || cancelling)}
                        aria-label={`Cancel mission at ${mission.location}`}
                        onClick={() => { setNotice(''); setActionError(''); setCancelling(mission); }}>Cancel</button>
                    </div>}</td>
                  </tr>
                ))}</tbody>
              </table>
            </div>}
          {(offset > 0 || page.pagination.has_more) && <nav className="mission-pagination" aria-label="Mission pages">
            <button className="secondary" disabled={offset === 0 || Boolean(editor || cancelling)}
              onClick={() => setOffset(value => value - pageSize)}>Previous</button>
            <span>Page {offset / pageSize + 1}</span>
            <button className="secondary" disabled={!page.pagination.has_more || Boolean(editor || cancelling)}
              onClick={() => setOffset(value => value + pageSize)}>Next</button>
          </nav>}
        </div>}
    </section>
  );
}

function MissionForm({ headingRef, user, options, mission, onClose, onSave }: {
  headingRef: React.Ref<HTMLHeadingElement>;
  user: User; options: Options; mission?: Mission; onClose: () => void; onSave: () => void;
}) {
  const [flagged, setFlagged] = useState(mission?.near_populated_area ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const isPilot = user.role === 'pilot';
  const canSchedule = options.drones.some(option => option.eligible) &&
    (isPilot || options.pilots.some(option => option.eligible));

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy || !canSchedule) return;
    setError('');
    const data = new FormData(event.currentTarget);
    const starts = timeFromInput(String(data.get('starts_at')), mission?.starts_at);
    const ends = timeFromInput(String(data.get('ends_at')), mission?.ends_at);
    if (!starts || !ends || Date.parse(ends) <= Date.parse(starts)) {
      setError('The end time must be after the start time.');
      return;
    }
    const droneID = data.get('drone_id');
    const pilotID = isPilot ? user.id : data.get('pilot_id');
    if (!droneID || !pilotID) {
      setError('Select an eligible aircraft and pilot.');
      return;
    }
    const body: MissionInput = {
      drone_id: String(droneID),
      pilot_id: String(pilotID),
      location: String(data.get('location')).trim(),
      starts_at: starts,
      ends_at: ends,
      near_populated_area: flagged,
      safety_rationale: flagged ? String(data.get('safety_rationale')).trim() : null,
      safety_acknowledged: flagged && data.has('safety_acknowledged'),
    };
    setBusy(true);
    try {
      await apiRequest(mission ? `/missions/${mission.id}` : '/missions', {
        method: mission ? 'PUT' : 'POST', body,
      });
      onSave();
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel mission-editor" aria-labelledby="mission-form-title" onSubmit={submit} aria-busy={busy}>
      <h4 id="mission-form-title" ref={headingRef} tabIndex={-1}>{mission ? 'Edit mission' : 'Schedule a mission'}</h4>
      {!canSchedule && <p role="status">Scheduling requires a registered aircraft that can be assigned and an active pilot.</p>}
      <fieldset disabled={busy} className="mission-fields">
        <label>Aircraft
          <select name="drone_id" required defaultValue={mission?.drone_id ?? ''}>
            <option value="">Select an aircraft</option>
            {options.drones.map(option => <option key={option.id} value={option.id} disabled={!option.eligible}>
              {option.name}{option.eligible ? '' : ' · Grounded'}
            </option>)}
          </select>
        </label>
        {isPilot ? <label>Pilot<input value={user.name} readOnly /></label> :
          <label>Pilot
            <select name="pilot_id" required defaultValue={mission?.pilot_id ?? ''}>
              <option value="">Select a pilot</option>
              {options.pilots.map(option => <option key={option.id} value={option.id} disabled={!option.eligible}>
                {option.name}{option.eligible ? '' : ' · Ineligible'}
              </option>)}
            </select>
          </label>}
        <label className="mission-wide">Location
          <input name="location" required maxLength={500} defaultValue={mission?.location} />
        </label>
        <label>Start time
          <input name="starts_at" type="datetime-local" step="0.001" required defaultValue={mission && localInput(mission.starts_at)} />
        </label>
        <label>End time
          <input name="ends_at" type="datetime-local" step="0.001" required defaultValue={mission && localInput(mission.ends_at)} />
        </label>
        <label className="mission-wide mission-checkbox">
          <input type="checkbox" checked={flagged} onChange={event => setFlagged(event.target.checked)} />
          Near a populated area
        </label>
        {flagged && <>
          <label className="mission-wide">Safety rationale
            <textarea name="safety_rationale" required maxLength={2000} rows={3} defaultValue={mission?.safety_rationale ?? ''} />
          </label>
          <label className="mission-wide mission-checkbox">
            <input name="safety_acknowledged" type="checkbox" required defaultChecked={mission?.safety_acknowledged} />
            I acknowledge the safety requirements for operating near a populated area.
          </label>
        </>}
      </fieldset>
      {error && <p className="error" role="alert">{error}</p>}
      <div className="row-actions">
        <button type="submit" disabled={busy || !canSchedule}>{busy ? 'Saving…' : mission ? 'Save changes' : 'Schedule mission'}</button>
        <button className="secondary" type="button" disabled={busy} onClick={onClose}>Close form</button>
      </div>
    </form>
  );
}
