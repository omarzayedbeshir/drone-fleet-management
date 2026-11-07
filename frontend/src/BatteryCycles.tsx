import { useEffect, useState } from 'react';
import { apiRequest, ApiError } from './api';

type Baseline = { total: number; source: 'unknown' | 'telemetry' | 'simulated' | 'manual'; recorded_at: string | null; revision: number; note: string };
type Response = { battery_cycles: Baseline };
const sourceLabels = { unknown: 'Unverified baseline', telemetry: 'Telemetry-provided', simulated: 'Simulated reading', manual: 'Documented manual reading' };
export function BatteryCycles({ droneId, canRecord, vendor }: { droneId: string; canRecord: boolean; vendor?: string }) {
  const [reading, setReading] = useState<Baseline | null>(null);
  const [total, setTotal] = useState('');
  const [reason, setReason] = useState('');
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const [notice, setNotice] = useState('');
  const [reload, setReload] = useState(0);
  useEffect(() => {
    if (editing) return;
    const controller = new AbortController(); let active = true; let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try {
        const data = await apiRequest<Response>(`/drones/${encodeURIComponent(droneId)}/battery-cycles`, { signal: controller.signal });
        if (active) { setReading(data.battery_cycles); setTotal(String(data.battery_cycles.total)); setError(''); }
      } catch (error) { if (active) setError(error instanceof Error ? error.message : 'Could not load cycle reading.'); }
      finally { if (active) timer = setTimeout(load, 5000); }
    }
    void load(); return () => { active = false; controller.abort(); clearTimeout(timer); };
  }, [droneId, editing, reload]);
  async function record() {
    if (!reading || busy) return;
    const value = Number(total);
    if (!/^\d+$/.test(total) || !Number.isSafeInteger(value) || value < reading.total || value > 1000000000 || reason.trim().length < 5 || reason.trim().length > 500) {
      setError('Enter a whole-number total at least as large as the current total, and a 5–500 character source/reference.'); return;
    }
    setBusy(true); setError(''); setConflict(false);
    try {
      const data = await apiRequest<Response>(`/drones/${encodeURIComponent(droneId)}/battery-cycles`, { method: 'PUT', body: { total: value, expected_total: reading.total, expected_revision: reading.revision, reason: reason.trim() } });
      setReading(data.battery_cycles); setEditing(false); setReason(''); setNotice('Cycle baseline recorded. Active managed connectors will reload it automatically.');
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not record the baseline.'); setConflict(error instanceof ApiError && error.status === 409); }
    finally { setBusy(false); }
  }
  return <section aria-label="Battery cycle baseline" className="connector-panel">
    <h4>Battery cycles</h4>
    {error && <p className="error" role="alert">{error}</p>}
    {!reading ? <p>{error ? 'Cycle reading unavailable.' : 'Loading cycle reading…'}</p> : <>
      <p><strong>{reading.total}</strong> · {sourceLabels[reading.source]}</p>
      {reading.source === 'unknown' && <p className="map-notice">This total has no verified source.{vendor === 'ardupilot' ? ' ArduPilot/SITL preserves a baseline and does not measure battery cycles.' : ' Record a documented baseline when a reliable reading is available.'}</p>}
      {reading.recorded_at && <p>Recorded: <time dateTime={reading.recorded_at}>{new Date(reading.recorded_at).toLocaleString()}</time></p>}
      {reading.note && <p>Source/reference: {reading.note}</p>}
      {canRecord && (!editing ? <button className="secondary" onClick={() => { setTotal(String(reading.total)); setEditing(true); setNotice(''); setError(''); }}>Record cycle baseline</button> : <>
        <p>Use a verified service log or battery record to establish the cumulative drone total. This leaves telemetry timestamps and grounding unchanged.</p>
        <label htmlFor={`cycle-total-${droneId}`}>Verified cumulative cycles</label>
        <input id={`cycle-total-${droneId}`} type="number" min={reading.total} max={1000000000} step="1" value={total} disabled={busy} onChange={e => setTotal(e.target.value)} />
        <label htmlFor={`cycle-reason-${droneId}`}>Reading source or reference</label>
        <input id={`cycle-reason-${droneId}`} type="text" maxLength={500} value={reason} disabled={busy} onChange={e => setReason(e.target.value)} />
        <div className="dialog-actions">
          <button className="secondary" disabled={busy || conflict} onClick={() => void record()}>{busy ? 'Recording…' : 'Save baseline reading'}</button>
          <button className="secondary" disabled={busy} onClick={() => { setEditing(false); setError(''); setConflict(false); }}>Cancel reading</button>
          {conflict && <button className="secondary" disabled={busy} onClick={() => { setEditing(false); setConflict(false); setReload(value => value + 1); }}>Reload cycle reading</button>}
        </div>
      </>)}
    </>}
    {!reading && error && <button className="secondary" onClick={() => setReload(value => value + 1)}>Retry cycle reading</button>}
    {notice && <p role="status">{notice}</p>}
  </section>;
}
