import { useEffect, useRef, useState } from 'react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import type { Drone } from './drones';
import { trailSegments, type PositionSample, type TelemetrySample } from './telemetry';

export interface MapAircraft { drone: Drone; position: PositionSample }
export function TelemetryMap({ aircraft, selectedId, selectedPosition, history, onSelect }: {
  aircraft: MapAircraft[]; selectedId: string; selectedPosition?: PositionSample;
  history: TelemetrySample[]; onSelect: (id: string) => void;
}) {
  const container = useRef<HTMLDivElement>(null);
  const map = useRef<L.Map | null>(null);
  const layers = useRef<L.LayerGroup | null>(null);
  const markers = useRef(new Map<string, { marker: L.CircleMarker; label: HTMLSpanElement }>());
  const trails = useRef<L.Polyline[]>([]);
  const select = useRef(onSelect);
  const fitted = useRef(false);
  const [tileError, setTileError] = useState(false);

  useEffect(() => { select.current = onSelect; }, [onSelect]);

  useEffect(() => {
    const instance = L.map(container.current!, { scrollWheelZoom: false }).setView([20, 0], 2);
    map.current = instance;
    const tiles = L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
    }).addTo(instance);
    tiles.on('tileerror', () => setTileError(true));
    layers.current = L.layerGroup().addTo(instance);
    const observer = new ResizeObserver(() => instance.invalidateSize());
    observer.observe(container.current!);
    return () => {
      observer.disconnect(); instance.remove(); map.current = null; layers.current = null; fitted.current = false;
      markers.current.clear(); trails.current = [];
    };
  }, []);

  useEffect(() => {
    const instance = map.current;
    const group = layers.current;
    if (!instance || !group) return;
    const currentIds = new Set(aircraft.map(({ drone }) => drone.id));
    for (const [id, entry] of markers.current) {
      if (!currentIds.has(id)) { group.removeLayer(entry.marker); markers.current.delete(id); }
    }
    for (const { drone, position } of aircraft) {
      const point = drone.id === selectedId && selectedPosition ? selectedPosition : position;
      const selected = drone.id === selectedId;
      let entry = markers.current.get(drone.id);
      if (!entry) {
        const label = document.createElement('span');
        const marker = L.circleMarker([point.latitude, point.longitude], { color: '#fff', weight: 2, fillOpacity: 1 })
          .bindTooltip(label, { direction: 'top' }).on('click', () => select.current(drone.id)).addTo(group);
        entry = { marker, label };
        markers.current.set(drone.id, entry);
      }
      entry.label.textContent = `${drone.name} · ${new Date(point.observed_at).toLocaleString()}`;
      entry.marker.setLatLng([point.latitude, point.longitude]);
      entry.marker.setStyle({ fillColor: selected ? '#993c25' : '#3e6650' });
      entry.marker.setRadius(selected ? 9 : 6);
    }
    const segments = selectedPosition ? trailSegments(history, selectedPosition.observed_at).filter(segment => segment.length > 1) : [];
    segments.forEach((segment, index) => {
      const coordinates: L.LatLngTuple[] = segment.map(point => [point.latitude, point.longitude]);
      if (trails.current[index]) trails.current[index].setLatLngs(coordinates);
      else trails.current.push(L.polyline(coordinates, { color: '#993c25', weight: 3, opacity: .7 }).addTo(group));
    });
    while (trails.current.length > segments.length) {
      group.removeLayer(trails.current.pop()!);
    }
    if (!fitted.current && aircraft.length) {
      instance.fitBounds(L.latLngBounds(aircraft.map(({ position }) => [position.latitude, position.longitude])), { padding: [45, 45], maxZoom: 16 });
      fitted.current = true;
    }
  }, [aircraft, selectedId, selectedPosition, history]);

  function fit() {
    if (aircraft.length) map.current?.fitBounds(L.latLngBounds(aircraft.map(({ position }) => [position.latitude, position.longitude])), { padding: [45, 45], maxZoom: 16 });
  }
  function locate() { if (selectedPosition) map.current?.setView([selectedPosition.latitude, selectedPosition.longitude], 17); }

  return <div className="map-canvas-wrap">
    <div className="map-tools"><button className="secondary" disabled={!aircraft.length} onClick={fit}>Fit aircraft</button><button className="secondary" disabled={!selectedPosition} onClick={locate}>Locate selected</button></div>
    <div ref={container} className="map-canvas" role="region" aria-label="Drone positions map. Select aircraft using the list for keyboard access." />
    {tileError && <p className="map-tile-error" role="status">Basemap unavailable. Reported coordinates and aircraft details are still available.</p>}
  </div>;
}
