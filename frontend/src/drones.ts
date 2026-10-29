import { apiRequest } from './api';

export type Vendor = 'dji' | 'ardupilot';
export type DroneStatus = 'available' | 'charging' | 'in_mission' | 'grounded' | 'unknown';
export interface DroneMetadata {
  name: string;
  vendor: Vendor;
  model: string;
  serial_number: string;
}
export interface Drone extends DroneMetadata {
  id: string;
  organisation_id: string;
  status: DroneStatus;
  last_telemetry_at: string | null;
  created_at: string;
  updated_at: string;
}
export interface DroneFilters { q: string; vendor: Vendor | ''; status: DroneStatus | ''; }
export interface DronePage {
  drones: Drone[];
  pagination: { limit: number; offset: number; has_more: boolean };
}
export const vendorLabels: Record<Vendor, string> = { dji: 'DJI', ardupilot: 'ArduPilot' };
export const statusLabels: Record<DroneStatus, string> = {
  available: 'Available', charging: 'Charging', in_mission: 'In mission', grounded: 'Grounded', unknown: 'Unknown',
};
export const emptyFilters: DroneFilters = { q: '', vendor: '', status: '' };

export const droneApi = {
  list(filters: DroneFilters, limit: number, offset: number, signal?: AbortSignal) {
    const query = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (filters.q) query.set('q', filters.q);
    if (filters.vendor) query.set('vendor', filters.vendor);
    if (filters.status) query.set('status', filters.status);
    return apiRequest<DronePage>(`/drones?${query}`, { signal });
  },
  get(id: string, signal?: AbortSignal) {
    return apiRequest<{ drone: Drone }>(`/drones/${encodeURIComponent(id)}`, { signal });
  },
  create(body: DroneMetadata) { return apiRequest<{ drone: Drone }>('/drones', { method: 'POST', body }); },
  update(id: string, body: Partial<DroneMetadata>) {
    return apiRequest<{ drone: Drone }>(`/drones/${encodeURIComponent(id)}`, { method: 'PATCH', body });
  },
  remove(id: string) { return apiRequest<void>(`/drones/${encodeURIComponent(id)}`, { method: 'DELETE' }); },
};

export function normalizeMetadata(value: DroneMetadata): DroneMetadata {
  return { name: value.name.trim(), vendor: value.vendor, model: value.model.trim(), serial_number: value.serial_number.trim() };
}
export function validateMetadata(value: DroneMetadata): string | null {
  const labels = { name: 'Drone name', model: 'Model', serial_number: 'Serial number' };
  for (const field of ['name', 'model', 'serial_number'] as const) {
    if (!value[field] || Array.from(value[field]).length > 200) return `${labels[field]} must contain 1–200 characters.`;
    if (/[\u0000-\u001f\u007f-\u009f]/u.test(value[field])) return `${labels[field]} cannot contain control characters.`;
  }
  return null;
}
