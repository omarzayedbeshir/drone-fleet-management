import { apiRequest } from './api';
import type { DroneStatus } from './drones';

export interface TelemetrySample {
  event_id: string;
  source: 'ardupilot' | 'dji' | 'csv';
  observed_at: string;
  received_at: string;
  status: DroneStatus;
  flight_time_seconds: number;
  battery_cycles: number;
  battery_percent: number | null;
  latitude: number | null;
  longitude: number | null;
  altitude_m: number | null;
  speed_mps: number | null;
  fault_flags: string[];
}
export interface TelemetryPage {
  samples: TelemetrySample[];
  pagination: { limit: number; offset: number; has_more: boolean };
}
export type PositionSample = TelemetrySample & { latitude: number; longitude: number };
export function hasPosition(sample: TelemetrySample): sample is PositionSample {
  return typeof sample.latitude === 'number' && Number.isFinite(sample.latitude) && Math.abs(sample.latitude) <= 90
    && typeof sample.longitude === 'number' && Number.isFinite(sample.longitude) && Math.abs(sample.longitude) <= 180
    && Number.isFinite(Date.parse(sample.observed_at));
}
export function positions(samples: TelemetrySample[]) {
  return samples.filter(hasPosition).sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at));
}
// Do not draw an apparent flight through missing GPS, long gaps, or the date line.
export function trailSegments(samples: TelemetrySample[], through: string) {
  const segments: PositionSample[][] = [];
  let segment: PositionSample[] = [];
  for (const sample of [...samples].sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at))) {
    if (Date.parse(sample.observed_at) > Date.parse(through)) break;
    const previous = segment.at(-1);
    if (!hasPosition(sample) || (previous && (Date.parse(sample.observed_at) - Date.parse(previous.observed_at) > 30_000 || Math.abs(sample.longitude - previous.longitude) > 180))) {
      if (segment.length) segments.push(segment);
      segment = [];
    }
    if (hasPosition(sample)) segment.push(sample);
  }
  if (segment.length) segments.push(segment);
  return segments;
}
export function readTelemetry(id: string, signal?: AbortSignal) {
  return apiRequest<TelemetryPage>(`/drones/${encodeURIComponent(id)}/telemetry?limit=100&offset=0`, { signal });
}
