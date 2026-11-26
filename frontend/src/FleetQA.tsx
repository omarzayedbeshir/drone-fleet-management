import { useState, type FormEvent } from 'react';
import { apiRequest } from './api';

interface Source {
  number: number;
  type: 'aircraft' | 'mission' | 'telemetry';
  id: string;
  data: Record<string, string | number | boolean | null>;
}
interface Result {
  status: 'answered' | 'insufficient_data' | 'limit_reached' | 'unavailable';
  answer: string;
  sources: Source[];
  resets_at?: string;
}

const fieldLabels: Record<string, string> = {
  name: 'Aircraft name', aircraft_name: 'Aircraft name', vendor: 'Vendor', model: 'Model',
  last_recorded_status: 'Last recorded status', last_telemetry_at: 'Last telemetry',
  flight_time_seconds: 'Cumulative flight time (seconds)', battery_cycles: 'Battery cycles',
  starts_at: 'Scheduled start', ends_at: 'Scheduled end', cancelled_at: 'Cancelled at',
  near_populated_area: 'Near a populated area', observed_at: 'Observed at', observed_status: 'Observed status',
  source: 'Telemetry source', fault_count: 'Fault flags recorded', next_observed_at: 'Next observation',
  next_observed_status: 'Next recorded status',
};

const sourceLabels = { aircraft: 'Aircraft', mission: 'Mission', telemetry: 'Telemetry' };

function fieldValue(value: Source['data'][string]) {
  if (value === null) return '—';
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  return String(value);
}

export function FleetQA() {
  const [question, setQuestion] = useState('');
  const [asked, setAsked] = useState('');
  const [result, setResult] = useState<Result | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = question.trim();
    if (busy || !value) return;
    setBusy(true);
    setAsked(value);
    setResult(null);
    try {
      setResult(await apiRequest<Result>('/fleet-qa', { method: 'POST', body: { question: value } }));
    } catch {
      setResult({ status: 'unavailable', answer: '', sources: [] });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="registry" aria-labelledby="fleet-qa-title">
      <div className="registry-heading"><h3 id="fleet-qa-title">Fleet Q&amp;A</h3></div>
      <form className="panel mission-editor" onSubmit={submit} aria-busy={busy}>
        <div className="mission-fields">
          <label className="mission-wide">Question about your fleet
            <textarea value={question} rows={3} required maxLength={500} disabled={busy}
              onChange={event => setQuestion(event.target.value)} aria-describedby="fleet-qa-scope"
              placeholder="What was the last recorded status of Surveyor?" />
          </label>
        </div>
        <p id="fleet-qa-scope">Asking sends your question and selected aircraft, mission, and telemetry records to Anthropic.</p>
        <div className="row-actions">
          <button type="submit" disabled={busy || !question.trim()}>{busy ? 'Asking Claude…' : 'Ask Claude'}</button>
        </div>
      </form>
      {result && <section className="panel" aria-labelledby="fleet-qa-answer">
        <h4 id="fleet-qa-answer">{asked}</h4>
        {result.status === 'unavailable' && <p role="status">Fleet Q&amp;A is unavailable. Try again later.</p>}
        {result.status === 'insufficient_data' && <p role="status">The available aircraft, mission, and telemetry records don’t answer this question.</p>}
        {result.status === 'limit_reached' && <p role="status">
          Your organisation has reached its daily question limit.
          {result.resets_at && ` Resets ${new Date(result.resets_at).toLocaleString()} (local time).`}
        </p>}
        {result.status === 'answered' && <>
          <p role="status" className="fleet-qa-answer">{result.answer}</p>
          <h4>Source records</h4>
          {result.sources.map(source => <details className="audit-event" key={`${source.type}:${source.id}`}>
            <summary>
              [{source.number}] {sourceLabels[source.type]} · {source.data.name ?? source.data.aircraft_name}
            </summary>
            <dl className="audit-identifiers">
              <div><dt>{source.type === 'telemetry' ? 'Aircraft ID' : 'Record ID'}</dt><dd>{source.id}</dd></div>
              {Object.entries(source.data).map(([field, value]) => <div key={field}>
                <dt>{fieldLabels[field] ?? field}</dt><dd>{fieldValue(value)}</dd>
              </div>)}
            </dl>
          </details>)}
        </>}
      </section>}
    </section>
  );
}
