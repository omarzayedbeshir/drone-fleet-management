import { useEffect, useMemo, useState } from 'react';
import type { Drone } from './drones';
import { statusLabels } from './drones';
import { positions, readTelemetry, type TelemetrySample } from './telemetry';
import { TelemetryMap } from './TelemetryMap';

type Feed = { samples: TelemetrySample[]; more: boolean; error?: string };
const timestamp = (value: string) => new Date(value).toLocaleString();
const measurement = (value: number | null | undefined, unit: string) => value == null ? 'Not reported' : `${value.toFixed(1)} ${unit}`;

export function FleetMap({ drones }: { drones: Drone[] }) {
  const [feeds, setFeeds] = useState<Record<string, Feed>>({});
  const [selectedId, setSelectedId] = useState(drones[0]?.id ?? '');
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const hasSnapshot = Object.keys(feeds).length > 0;

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    // Bound both the number of HTTP requests in flight and the history window.
    const results: Record<string, Feed> = {};
    let cursor = 0;
    async function worker() {
      while (cursor < drones.length && active) {
        const drone = drones[cursor++];
        try {
          const page = await readTelemetry(drone.id, controller.signal);
          results[drone.id] = { samples: page.samples, more: page.pagination.has_more };
        } catch (error) {
          if (active) results[drone.id] = { samples: [], more: false, error: error instanceof Error ? error.message : 'Could not load telemetry' };
        }
      }
    }
    void Promise.all(Array.from({ length: Math.min(4, drones.length) }, worker)).then(() => {
      if (active) {
        setFeeds(previous => Object.fromEntries(Object.entries(results).map(([id, result]) => [id,
          result.error && previous[id] ? { ...previous[id], error: result.error } : result,
        ])));
        setLoading(false);
      }
    });
    return () => { active = false; controller.abort(); };
  }, [drones, refresh]);

  // Monitoring runs while the map is mounted and pauses when hidden. It makes
  // authenticated requests, so it intentionally keeps the session active.
  useEffect(() => {
    if (loading) return;
    const timer = window.setTimeout(() => { if (!document.hidden) setRefresh(value => value + 1); }, 5000);
    const visible = () => { if (!document.hidden) setRefresh(value => value + 1); };
    document.addEventListener('visibilitychange', visible);
    return () => { window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible); };
  }, [loading, refresh]);

  const selected = drones.find(drone => drone.id === selectedId) ?? drones[0];
  const feed = selected ? feeds[selected.id] : undefined;
  const history = feed?.samples ?? [];
  const points = useMemo(() => positions(history), [history]);
  const point = points.at(-1);
  const sample = history[0];
  const aircraft = useMemo(() => drones.flatMap(drone => {
    const position = positions(feeds[drone.id]?.samples ?? []).at(-1);
    return position ? [{ drone, position }] : [];
  }), [drones, feeds]);

  return <section className="fleet-map" aria-label="Fleet telemetry">
    <div className="map-heading">
      <div><h4>Position & telemetry</h4><p>Aircraft on this registry page · last 100 observations per aircraft</p></div>
      <div className="map-controls">
        <span className="muted">Live · updates every 5s</span>
        <button className="secondary" disabled={loading && !hasSnapshot} onClick={() => {
          if (loading) return;
          setRefresh(value => value + 1);
        }}>Refresh telemetry</button>
      </div>
    </div>
    <p role="status" className={hasSnapshot ? 'sr-only' : 'map-refresh-status'}>{loading ? 'Updating telemetry…' : ''}</p>
    <div className="map-layout">
      <div className="map-main">
        <TelemetryMap aircraft={aircraft} selectedId={selected?.id ?? ''} selectedPosition={point} history={history} onSelect={setSelectedId} />
      </div>
      <aside className="map-sidebar" aria-label="Aircraft telemetry details">
        <label htmlFor="map-aircraft">Aircraft</label>
        <select id="map-aircraft" value={selected?.id ?? ''} onChange={event => setSelectedId(event.target.value)}>{drones.map(drone => <option key={drone.id} value={drone.id}>{drone.name}</option>)}</select>
        {selected && <>
          <h4>{selected.name}</h4><p className="serial">{selected.serial_number}</p>
          {feed?.error && <p className="error" role="alert">{feed.error}{sample ? ' Showing the last loaded readings.' : ''}</p>}
          {!sample ? <p className="map-notice">{loading && !hasSnapshot ? 'Waiting for telemetry…' : 'No telemetry received. Start the adapter for this aircraft.'}</p> : <>
            <p className="telemetry-time">Latest observation<br /><time dateTime={sample.observed_at}>{timestamp(sample.observed_at)}</time></p>
            <dl className="telemetry-readings">
              <div><dt>Reported status</dt><dd>{statusLabels[sample.status]}</dd></div>
              <div><dt>Battery</dt><dd>{measurement(sample.battery_percent, '%')}</dd></div>
              <div><dt>Altitude · MSL</dt><dd>{measurement(sample.altitude_m, 'm')}</dd></div>
              <div><dt>Speed</dt><dd>{measurement(sample.speed_mps, 'm/s')}</dd></div>
              <div><dt>Flight time</dt><dd>{measurement(sample.flight_time_seconds / 3600, 'h')}</dd></div>
              <div><dt>Battery cycles</dt><dd>{sample.battery_cycles}</dd></div>
            </dl>
            <p className="coordinate-reading">{point ? <>Last GPS: {point.latitude.toFixed(6)}, {point.longitude.toFixed(6)}<br />{timestamp(point.observed_at)}</> : 'GPS not reported in this window.'}</p>
            {sample.latitude == null && point && <p className="map-notice">The latest observation has no GPS fix. The map retains the last known GPS position above.</p>}
            <p className="muted">Faults: {sample.fault_flags.length ? sample.fault_flags.join(', ') : 'None reported'}</p>
          </>}
        </>}
        {Object.values(feeds).some(value => value.error) && <p className="error" role="status">Some aircraft could not load. Select an aircraft for details, then retry Refresh telemetry.</p>}
      </aside>
    </div>
  </section>;
}
