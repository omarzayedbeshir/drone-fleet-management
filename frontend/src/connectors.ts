import { apiRequest } from './api';

export interface ConnectionProfile {
  id: string; label: string; source: 'csv' | 'dji' | 'ardupilot' | 'external'; vendor: string; simulated: boolean;
}
export interface Connector {
  drone_id: string; profile_id: string; enabled: boolean; generation: number;
  state: 'pending' | 'starting' | 'running' | 'retrying' | 'completed' | 'failed' | 'stopped' | 'disabled';
  health: { connected: boolean; ready: boolean; last_frame: string; last_emitted: string; last_error?: string; accepted: number; rejected: number; dropped: number };
  updated_at: string; runtime_updated_at: string | null;
}
export interface ConnectorView { connector: Connector | null; profiles: ConnectionProfile[] }
export const connectorApi = {
  get(id: string, signal?: AbortSignal) { return apiRequest<ConnectorView>(`/drones/${encodeURIComponent(id)}/connector`, { signal }); },
  save(id: string, profile_id: string, enabled: boolean) {
    return apiRequest<ConnectorView>(`/drones/${encodeURIComponent(id)}/connector`, { method: 'PUT', body: { profile_id, enabled } });
  },
};
