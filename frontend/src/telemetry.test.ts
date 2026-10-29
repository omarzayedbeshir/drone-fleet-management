import { expect, it } from 'vitest';
import { hasPosition, positions, trailSegments, type TelemetrySample } from './telemetry';

const sample = (seconds: number, latitude: number | null = 30, longitude: number | null = 31): TelemetrySample => ({
  event_id: `test-${seconds}`, source: 'ardupilot', observed_at: new Date(Date.UTC(2026, 9, 29, 12, 0, seconds)).toISOString(),
  received_at: '2026-10-29T12:00:00Z', status: 'available', flight_time_seconds: 0, battery_cycles: 0,
  battery_percent: null, latitude, longitude, altitude_m: 100, speed_mps: 0, fault_flags: [],
});

it('retains legitimate zero coordinates and excludes missing or invalid positions', () => {
  expect(hasPosition(sample(0, 0, 0))).toBe(true);
  expect(positions([sample(3), sample(1), sample(2, null, null), sample(4, NaN), sample(5, 91)]).map(value => value.event_id)).toEqual(['test-1', 'test-3']);
});

it('breaks trails for GPS loss, large time gaps, and the date line', () => {
  const samples = [sample(0), sample(1), sample(2, null, null), sample(3), sample(40), sample(41, 30, 179), sample(42, 30, -179)];
  expect(trailSegments(samples, sample(42).observed_at).map(segment => segment.map(point => point.event_id))).toEqual([
    ['test-0', 'test-1'], ['test-3'], ['test-40', 'test-41'], ['test-42'],
  ]);
  expect(trailSegments(samples, sample(1).observed_at)[0]).toHaveLength(2);
});
