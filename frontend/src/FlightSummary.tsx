import { useEffect, useId, useState, type FormEvent } from 'react';
import { apiRequest } from './api';

interface Counts {
  observations: number;
  in_mission_observations: number;
}

interface Report {
  day: string;
  status: 'no_data' | 'facts_only' | 'ai_generated' | 'ai_unavailable' | 'ai_not_configured';
  narrative: string;
  facts: Counts & { aircraft_with_telemetry: number; aircraft_observed_in_mission: number };
  sources: (Counts & {
    drone_id: string;
    drone_name: string;
    first_observation: string;
    last_observation: string;
  })[];
}

interface Schedule {
  enabled: boolean;
  latest: { report: Report; delivered_at: string } | null;
}

const schedulePath = '/flight-summary/schedule';
const utcTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' });

export function FlightSummary() {
  const [day, setDay] = useState(() => new Date(Date.now() - 86_400_000).toISOString().slice(0, 10));
  const [useAI, setUseAI] = useState(false);
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function generate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setReport(null);
    setError('');
    try {
      setReport(await apiRequest<Report>('/flight-summary', { method: 'POST', body: { day, use_ai: useAI } }));
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Could not generate the flight summary.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="registry" aria-labelledby="flight-summary-title">
      <div className="registry-heading"><h3 id="flight-summary-title">Daily flight summary</h3></div>
      <ScheduledSummary />
      <form className="panel mission-editor" onSubmit={generate} aria-busy={busy}>
        <fieldset className="mission-fields" disabled={busy}>
          <label>Reporting day (UTC)
            <input type="date" value={day} min="1970-01-01" max={new Date().toISOString().slice(0, 10)}
              onChange={event => setDay(event.target.value)} required />
          </label>
          <label className="mission-wide mission-checkbox">
            <input type="checkbox" checked={useAI} onChange={event => setUseAI(event.target.checked)} />
            Send aggregate counts to Anthropic to write the summary
          </label>
        </fieldset>
        <div className="row-actions">
          <button type="submit" disabled={busy}>{busy ? 'Generating…' : 'Generate summary'}</button>
        </div>
      </form>
      {error && <p role="alert" className="error">{error}</p>}
      {report && <SummaryReport report={report} />}
    </section>
  );
}

function ScheduledSummary() {
  const [schedule, setSchedule] = useState<Schedule | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    apiRequest<Schedule>(schedulePath, { signal: controller.signal })
      .then(result => { if (!controller.signal.aborted) setSchedule(result); })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load daily delivery settings.'); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [refresh]);

  async function toggle(enabled: boolean) {
    if (saving || loading) return;
    setSaving(true);
    setError('');
    try {
      await apiRequest<void>(schedulePath, { method: 'PUT', body: { enabled } });
      setSchedule(value => value && { ...value, enabled });
      setRefresh(value => value + 1);
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Could not save daily delivery settings.');
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="panel mission-editor" aria-labelledby="daily-delivery-title">
      <h4 id="daily-delivery-title">Automatic daily delivery</h4>
      <p id="daily-delivery-help">
        Receive the previous day’s report here by 01:00 UTC. When Claude is configured, the report sends aggregate counts to Anthropic.
      </p>
      {loading && <p role="status">Checking daily delivery…</p>}
      {error && <p role="alert" className="error">{error}</p>}
      {schedule && <div className="mission-fields">
        <label className="mission-wide mission-checkbox">
          <input type="checkbox" checked={schedule.enabled} disabled={loading || saving} aria-describedby="daily-delivery-help"
            onChange={event => void toggle(event.target.checked)} />
          Enable daily delivery for this organisation
        </label>
      </div>}
      <div className="row-actions">
        <button className="secondary" disabled={loading || saving} onClick={() => setRefresh(value => value + 1)}>Refresh deliveries</button>
      </div>
      {schedule?.latest ? <details className="audit-event">
        <summary>Latest delivery · {schedule.latest.report.day} UTC</summary>
        <SummaryReport report={schedule.latest.report} />
      </details> : schedule?.enabled && <p>No report has arrived yet.</p>}
    </section>
  );
}

function SummaryReport({ report }: { report: Report }) {
  const title = useId();
  const sourcesTitle = useId();
  return <section className="panel" aria-labelledby={title}>
    <h4 id={title}>Summary for {report.day} UTC</h4>
    {report.status === 'ai_generated' && <p>Wording generated by Claude</p>}
    {report.status === 'ai_not_configured' && <p role="status">Claude wording is disabled until Anthropic is configured on the server.</p>}
    {report.status === 'ai_unavailable' && <p role="status">Claude wording is unavailable. The recorded figures are shown below.</p>}
    <p role="status">{report.narrative}</p>
    {report.sources.length > 0 && <>
      <dl className="audit-identifiers">
        <div><dt>Aircraft with telemetry</dt><dd>{report.facts.aircraft_with_telemetry}</dd></div>
        <div><dt>Aircraft observed in mission</dt><dd>{report.facts.aircraft_observed_in_mission}</dd></div>
        <div><dt>Telemetry observations</dt><dd>{report.facts.observations}</dd></div>
        <div><dt>In-mission observations</dt><dd>{report.facts.in_mission_observations}</dd></div>
      </dl>
      <h4 id={sourcesTitle}>Source telemetry</h4>
      <div className="mission-table-wrap" tabIndex={0} role="region" aria-labelledby={sourcesTitle}>
        <table className="mission-table">
          <thead><tr><th scope="col">Aircraft</th><th scope="col">First observation (UTC)</th>
            <th scope="col">Last observation (UTC)</th><th scope="col">Observations</th><th scope="col">In mission</th></tr></thead>
          <tbody>{report.sources.map(source => <tr key={source.drone_id}>
            <th scope="row">{source.drone_name}</th>
            <td>{utcTime.format(new Date(source.first_observation))}</td>
            <td>{utcTime.format(new Date(source.last_observation))}</td>
            <td>{source.observations}</td><td>{source.in_mission_observations}</td>
          </tr>)}</tbody>
        </table>
      </div>
    </>}
  </section>;
}
