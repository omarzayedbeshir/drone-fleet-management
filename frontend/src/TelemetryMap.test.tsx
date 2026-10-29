import { render } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import L from 'leaflet';
import { TelemetryMap } from './TelemetryMap';
import type { PositionSample } from './telemetry';
import type { Drone } from './drones';

vi.mock('leaflet', () => {
  const instance = { setView: vi.fn().mockReturnThis(), fitBounds: vi.fn(), remove: vi.fn(), invalidateSize: vi.fn() };
  const group = { addTo: vi.fn().mockReturnThis(), removeLayer: vi.fn() };
  const layer = () => ({ addTo: vi.fn().mockReturnThis(), bindTooltip: vi.fn().mockReturnThis(), on: vi.fn().mockReturnThis(),
    setLatLng: vi.fn(), setStyle: vi.fn(), setRadius: vi.fn(), setLatLngs: vi.fn() });
  return { default: { map: vi.fn(() => instance), tileLayer: vi.fn(layer), layerGroup: vi.fn(() => group), circleMarker: vi.fn(layer), polyline: vi.fn(layer), latLngBounds: vi.fn(value => value) } };
});

it('retains the basemap, marker, trail, and viewport across coordinate updates', () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  const first: PositionSample = { event_id: 'first', observed_at: '2026-10-29T12:00:00Z', received_at: '', source: 'ardupilot', status: 'available', latitude: 30, longitude: 31, altitude_m: 100, speed_mps: 0, battery_percent: null, flight_time_seconds: 0, battery_cycles: 0, fault_flags: [] };
  const second = { ...first, event_id: 'second', observed_at: '2026-10-29T12:00:01Z', latitude: 30.001 };
  const third = { ...second, event_id: 'third', observed_at: '2026-10-29T12:00:02Z', latitude: 30.002 };
  const drone = { id: 'ap', name: 'Quad' } as Drone;
  const { rerender, unmount } = render(<TelemetryMap aircraft={[{ drone, position: second }]} selectedId="ap" selectedPosition={second} history={[second, first]} onSelect={() => {}} />);
  const map = vi.mocked(L.map).mock.results[0].value;
  const marker = vi.mocked(L.circleMarker).mock.results[0].value;
  const trail = vi.mocked(L.polyline).mock.results[0].value;
  rerender(<TelemetryMap aircraft={[{ drone, position: third }]} selectedId="ap" selectedPosition={third} history={[third, second, first]} onSelect={() => {}} />);
  expect(L.map).toHaveBeenCalledTimes(1);
  expect(L.tileLayer).toHaveBeenCalledTimes(1);
  expect(L.circleMarker).toHaveBeenCalledTimes(1);
  expect(L.polyline).toHaveBeenCalledTimes(1);
  expect(marker.setLatLng).toHaveBeenLastCalledWith([30.002, 31]);
  expect(trail.setLatLngs).toHaveBeenLastCalledWith([[30, 31], [30.001, 31], [30.002, 31]]);
  expect(map.fitBounds).toHaveBeenCalledTimes(1);
  expect(map.remove).not.toHaveBeenCalled();
  unmount();
  expect(map.remove).toHaveBeenCalledTimes(1);
});
