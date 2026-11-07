import { useEffect, useState } from 'react';
import { connectorApi, type ConnectorView } from './connectors';

const states = { pending: 'Waiting to start', starting: 'Connecting', running: 'Running', retrying: 'Retrying', completed: 'Recording completed', failed: 'Needs attention', stopped: 'Supervisor unavailable', disabled: 'Disabled' };
function timestamp(value?: string | null) { return !value || value.startsWith('0001-') ? 'Not reported' : new Date(value).toLocaleString(); }
export function ConnectorPanel({ droneId, canManage }: { droneId: string; canManage: boolean }) {
  const [view, setView] = useState<ConnectorView | null>(null);
  const [profile, setProfile] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState('');
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try {
        const data = await connectorApi.get(droneId, controller.signal);
        if (active) {
          setView(previous => previous?.connector && data.connector && previous.connector.generation > data.connector.generation ? previous : data); setError('');
          setProfile(previous => previous || data.connector?.profile_id || data.profiles[0]?.id || '');
        }
      } catch (error) { if (active) setError(error instanceof Error ? error.message : 'Could not load connection.'); }
      finally { if (active) timer = setTimeout(load, 5000); }
    }
    void load();
    return () => { active = false; controller.abort(); clearTimeout(timer); };
  }, [droneId, refresh]);
  async function save(enabled: boolean, chosen = profile) {
    if (busy || !chosen) return;
    setBusy(true); setError(''); setNotice('');
    try {
      const data = await connectorApi.save(droneId, chosen, enabled);
      setView(data); setNotice(enabled ? 'Connection saved. The supervisor will start it shortly.' : 'Connection disabled.');
      setRefresh(value => value + 1);
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not save connection.'); }
    finally { setBusy(false); }
  }
  const c = view?.connector;
  const selected = view?.profiles.find(item => item.id === (c?.profile_id || profile));
  return <section aria-label="Telemetry connection" className="connector-panel">
    <h4>Telemetry connection</h4>
    {error && <p role="alert" className="error">{error}</p>}
    {!view ? !error && <p>Loading connection…</p> : <>
      <p><strong>{c ? states[c.state] : 'Not configured'}</strong>{selected?.simulated ? ' · Simulated source' : ''}</p>
      {c && <>
        <p>Last supervisor update: {timestamp(c.runtime_updated_at)}<br />Last received: {timestamp(c.health.last_frame)}<br />Last stored: {timestamp(c.health.last_emitted)}</p>
        <p>Accepted: {c.health.accepted ?? 0} · Rejected: {c.health.rejected ?? 0} · Dropped: {c.health.dropped ?? 0}</p>
        {c.health.last_error && <p className="map-notice">{c.health.last_error}</p>}
      </>}
      {canManage && <>
        <label htmlFor={`connector-profile-${droneId}`}>Connection profile</label>
        <select id={`connector-profile-${droneId}`} value={profile} disabled={busy} onChange={event => setProfile(event.target.value)}>
          {!profile && <option value="">Select a profile</option>}
          {profile && !view.profiles.some(item => item.id === profile) && <option value={profile}>Unavailable profile</option>}
          {view.profiles.map(item => <option key={item.id} value={item.id}>{item.label}{item.simulated ? ' (simulated)' : ''}</option>)}
        </select>
        {!view.profiles.length && <p>No connection profiles are available for this aircraft. Ask the operator to configure one.</p>}
        <div className="dialog-actions">
          <button className="secondary" disabled={busy || !view.profiles.some(item => item.id === profile)} onClick={() => void save(true)}>{busy ? 'Saving…' : c?.enabled ? 'Save and restart' : 'Enable connection'}</button>
          <button className="secondary" disabled={busy || !c?.enabled} onClick={() => void save(false, c!.profile_id)}>Disable connection</button>
        </div>
      </>}
    </>}
    {notice && <p role="status">{notice}</p>}
  </section>;
}
